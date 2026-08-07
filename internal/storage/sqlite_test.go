package storage

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/toolazytoname/lodge/internal/domain"
)

func openTestSQLite(t *testing.T) (*SQLite, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "data", "lodge.db")
	store, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close sqlite: %v", err)
		}
	})
	return store, path
}

func sampleObservation(at time.Time) domain.Observation {
	startedAt := at.Add(-time.Hour)
	return domain.Observation{
		HostID:       "host-a",
		ObservedAt:   at,
		Online:       true,
		Hostname:     "host-a.internal",
		AgentVersion: "0.2.0",
		Resources: &domain.Resources{
			CPUs: 4, Load1: 0.5, Load5: 0.25, Load15: 0.1,
			Memory: domain.MemoryResources{TotalBytes: 1024, AvailableBytes: 768, UsedBytes: 256},
			Disks:  []domain.DiskResources{{Mount: "/", Filesystem: "ext4", TotalBytes: 4096, FreeBytes: 2048, UsedBytes: 2048}},
		},
		Workloads: []domain.Workload{{
			HostID: "host-a", Key: "docker:web", Kind: domain.WorkloadDocker,
			Name: "web", State: "running", Image: "nginx:stable",
			ComposeProject: "site", ComposeService: "web", StartedAt: &startedAt,
		}},
		Endpoints: []domain.Endpoint{{
			HostID: "host-a", WorkloadKey: "docker:web", Key: "tcp://0.0.0.0:443",
			Protocol: "tcp", Bind: "0.0.0.0", Port: 443,
			Binding: domain.BindingWildcard, Reachability: domain.ReachabilityUnknown,
		}},
		Routes: []domain.ProxyRoute{{
			HostID: "host-a", WorkloadKey: "docker:web", Key: "https://web.example.test:443/",
			Scheme: "https", Host: "web.example.test", Port: 443, Path: "/",
			Upstreams: []string{"127.0.0.1:8080"},
		}},
		Warnings: []string{"partial fixture warning"},
	}
}

func TestSQLiteMigrationAndHostSync(t *testing.T) {
	store, path := openTestSQLite(t)
	ctx := context.Background()
	var version int
	if err := store.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != currentSchemaVersion {
		t.Fatalf("schema version = %d, want %d", version, currentSchemaVersion)
	}
	var migrationsApplied int
	if err := store.db.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations").Scan(&migrationsApplied); err != nil {
		t.Fatal(err)
	}
	if migrationsApplied != len(migrations) {
		t.Fatalf("migration ledger count = %d, want %d", migrationsApplied, len(migrations))
	}

	hosts := []domain.Host{
		{ID: "host-a", Name: "Host A", PublicHost: "a.example.test"},
		{ID: "host-b", Name: "Host B"},
	}
	if err := store.SyncHosts(ctx, hosts); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Hosts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded, hosts) {
		t.Fatalf("hosts round trip mismatch:\n got: %+v\nwant: %+v", loaded, hosts)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("database mode = %04o, want 0600", info.Mode().Perm())
	}
	var sensitiveColumns int
	if err := store.db.QueryRowContext(ctx,
		"SELECT count(*) FROM pragma_table_info('hosts') WHERE lower(name) IN ('token', 'agent_token', 'password')",
	).Scan(&sensitiveColumns); err != nil {
		t.Fatal(err)
	}
	if sensitiveColumns != 0 {
		t.Fatal("host schema must not contain Agent credentials or passwords")
	}
}

func TestWebLinkChecksReplaceAtomically(t *testing.T) {
	store, _ := openTestSQLite(t)
	ctx := context.Background()
	if err := store.SyncHosts(ctx, []domain.Host{{ID: "host-a", Name: "Host A"}}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	checks := []domain.WebLinkCheck{
		{HostID: "host-a", WorkloadKey: "docker:admin", URL: "https://admin.example.test/", State: domain.WebLinkUnreachable, ErrorKind: "timeout", LatencyMS: 3000, CheckedAt: now},
		{HostID: "host-a", WorkloadKey: "docker:web", URL: "https://app.example.test/", State: domain.WebLinkReachable, HTTPStatus: 204, LatencyMS: 14, CheckedAt: now},
	}
	if err := store.ReplaceWebLinkChecks(ctx, checks); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.WebLinkChecks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded, checks) {
		t.Fatalf("Web link checks mismatch:\n got: %+v\nwant: %+v", loaded, checks)
	}
	if err := store.ReplaceWebLinkChecks(ctx, checks[:1]); err != nil {
		t.Fatal(err)
	}
	loaded, err = store.WebLinkChecks(ctx)
	if err != nil || len(loaded) != 1 || loaded[0].URL != checks[0].URL {
		t.Fatalf("stale Web link checks were not replaced: %+v, %v", loaded, err)
	}
	invalid := checks[1]
	invalid.HTTPStatus = 0
	if err := store.ReplaceWebLinkChecks(ctx, []domain.WebLinkCheck{invalid}); err == nil {
		t.Fatal("invalid replacement was accepted")
	}
	loaded, err = store.WebLinkChecks(ctx)
	if err != nil || len(loaded) != 1 || loaded[0].URL != checks[0].URL {
		t.Fatalf("invalid replacement modified durable checks: %+v, %v", loaded, err)
	}
}

func TestSQLiteEventLifecycleDedupeAndAtomicity(t *testing.T) {
	store, _ := openTestSQLite(t)
	ctx := context.Background()
	if err := store.SyncHosts(ctx, []domain.Host{{ID: "host-a", Name: "Host A"}}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	signal := domain.EventSignal{
		HostID: "host-a", Kind: "resource.memory", Severity: domain.SeverityWarning,
		DedupeKey: "host-a:resource:memory", Title: "Memory pressure", Detail: "86% used",
	}

	firstID, transitions, err := store.RecordObservationWithEvents(ctx, sampleObservation(now), []domain.EventSignal{signal})
	if err != nil {
		t.Fatal(err)
	}
	if firstID == 0 || len(transitions) != 1 || transitions[0].Type != domain.EventOpened {
		t.Fatalf("first signal did not open one event: id=%d transitions=%+v", firstID, transitions)
	}
	opened := transitions[0].Event
	if opened.State != domain.EventActive || opened.ID == "" {
		t.Fatalf("opened event is invalid: %+v", opened)
	}

	secondAt := now.Add(time.Minute)
	signal.Severity = domain.SeverityCritical
	signal.Detail = "96% used"
	_, transitions, err = store.RecordObservationWithEvents(ctx, sampleObservation(secondAt), []domain.EventSignal{signal})
	if err != nil {
		t.Fatal(err)
	}
	if len(transitions) != 0 {
		t.Fatalf("continuing signal should not create a transition: %+v", transitions)
	}
	active, err := store.ActiveEvents(ctx, "host-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].ID != opened.ID || active[0].Severity != domain.SeverityCritical || !active[0].LastObservedAt.Equal(secondAt) {
		t.Fatalf("active event was not updated in place: %+v", active)
	}

	ackAt := secondAt.Add(30 * time.Second)
	acknowledged, found, err := store.AcknowledgeEvent(ctx, opened.ID, ackAt)
	if err != nil || !found || acknowledged.State != domain.EventAcknowledged || acknowledged.AcknowledgedAt == nil || !acknowledged.AcknowledgedAt.Equal(ackAt) {
		t.Fatalf("acknowledge event failed: found=%v event=%+v err=%v", found, acknowledged, err)
	}
	idempotent, found, err := store.AcknowledgeEvent(ctx, opened.ID, ackAt.Add(time.Minute))
	if err != nil || !found || idempotent.AcknowledgedAt == nil || !idempotent.AcknowledgedAt.Equal(ackAt) {
		t.Fatalf("idempotent acknowledgement changed audit time: found=%v event=%+v err=%v", found, idempotent, err)
	}

	recoveredAt := now.Add(2 * time.Minute)
	_, transitions, err = store.RecordObservationWithEvents(ctx, sampleObservation(recoveredAt), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(transitions) != 1 || transitions[0].Type != domain.EventRecovered || transitions[0].Event.State != domain.EventResolved {
		t.Fatalf("missing signal did not recover the event: %+v", transitions)
	}
	if active, err = store.ActiveEvents(ctx, "host-a"); err != nil || len(active) != 0 {
		t.Fatalf("resolved event remained active: %+v, %v", active, err)
	}
	if _, found, err := store.AcknowledgeEvent(ctx, opened.ID, recoveredAt.Add(time.Minute)); !found || !errors.Is(err, ErrEventResolved) {
		t.Fatalf("resolved acknowledgement should conflict: found=%v err=%v", found, err)
	}

	recurredAt := now.Add(3 * time.Minute)
	_, transitions, err = store.RecordObservationWithEvents(ctx, sampleObservation(recurredAt), []domain.EventSignal{signal})
	if err != nil {
		t.Fatal(err)
	}
	if len(transitions) != 1 || transitions[0].Type != domain.EventOpened || transitions[0].Event.ID == opened.ID {
		t.Fatalf("recurrence should open a new event: %+v", transitions)
	}
	timeline, err := store.Events(ctx, "host-a", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(timeline) != 2 || timeline[0].State != domain.EventActive || timeline[1].State != domain.EventResolved {
		t.Fatalf("event timeline is incomplete or misordered: %+v", timeline)
	}

	var observationsBefore int
	if err := store.db.QueryRowContext(ctx, "SELECT count(*) FROM observations").Scan(&observationsBefore); err != nil {
		t.Fatal(err)
	}
	invalidObservation := sampleObservation(now.Add(4 * time.Minute))
	if _, _, err := store.RecordObservationWithEvents(ctx, invalidObservation, []domain.EventSignal{signal, signal}); err == nil {
		t.Fatal("duplicate signals were accepted")
	}
	staleObservation := sampleObservation(now.Add(150 * time.Second))
	if _, _, err := store.RecordObservationWithEvents(ctx, staleObservation, []domain.EventSignal{signal}); err == nil {
		t.Fatal("stale event update was accepted")
	}
	var observationsAfter int
	if err := store.db.QueryRowContext(ctx, "SELECT count(*) FROM observations").Scan(&observationsAfter); err != nil {
		t.Fatal(err)
	}
	if observationsAfter != observationsBefore {
		t.Fatalf("failed event reconciliation left observations behind: before=%d after=%d", observationsBefore, observationsAfter)
	}
}

func TestSQLiteEventQueriesAreBounded(t *testing.T) {
	store, _ := openTestSQLite(t)
	if _, err := store.Events(context.Background(), "", 0); err == nil {
		t.Fatal("zero event limit was accepted")
	}
	if _, err := store.Events(context.Background(), "", 501); err == nil {
		t.Fatal("oversized event limit was accepted")
	}
	if _, found, err := store.AcknowledgeEvent(context.Background(), "missing", time.Now().UTC()); err != nil || found {
		t.Fatalf("missing event acknowledgement mismatch: found=%v err=%v", found, err)
	}
}

func TestMigrationPlanMustBeContiguousAndComplete(t *testing.T) {
	tests := []struct {
		name string
		plan []migration
	}{
		{name: "missing version", plan: []migration{{version: 2, name: "second", sql: "SELECT 1"}}},
		{name: "missing sql", plan: []migration{{version: 1, name: "first"}}},
		{name: "duplicate name", plan: []migration{
			{version: 1, name: "same", sql: "SELECT 1"},
			{version: 2, name: "same", sql: "SELECT 2"},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateMigrationPlan(test.plan, len(test.plan)); err == nil {
				t.Fatal("invalid migration plan was accepted")
			}
		})
	}
	if err := validateMigrationPlan(migrations, currentSchemaVersion); err != nil {
		t.Fatalf("repository migration plan is invalid: %v", err)
	}
}

func createVersionOneDatabase(t *testing.T, path string) {
	t.Helper()
	if err := prepareDatabasePath(path); err != nil {
		t.Fatal(err)
	}
	dsn, err := sqliteDSN(path)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(migrations[0].sql); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		"INSERT INTO schema_migrations(version, name, checksum, applied_at) VALUES (1, ?, ?, ?)",
		migrations[0].name, migrations[0].checksum(), formatTime(time.Now()),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version = 1"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
INSERT INTO hosts(id, name, created_at, updated_at) VALUES ('host-a', 'Host A', ?, ?)`,
		formatTime(time.Now()), formatTime(time.Now())); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteUpgradesVersionOneAndPreservesData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lodge.db")
	createVersionOneDatabase(t, path)

	store, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var version int
	if err := store.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != currentSchemaVersion {
		t.Fatalf("schema version = %d, want %d", version, currentSchemaVersion)
	}
	hosts, err := store.Hosts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 1 || hosts[0].ID != "host-a" {
		t.Fatalf("v1 host was not preserved: %+v", hosts)
	}
}

func TestSQLiteUpgradesVersionFourAndAddsWebLinkChecks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lodge.db")
	createVersionOneDatabase(t, path)
	dsn, err := sqliteDSN(path)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range migrations[1:4] {
		if _, err := db.Exec(migration.sql); err != nil {
			db.Close()
			t.Fatal(err)
		}
		if _, err := db.Exec(
			"INSERT INTO schema_migrations(version, name, checksum, applied_at) VALUES (?, ?, ?, ?)",
			migration.version, migration.name, migration.checksum(), formatTime(time.Now()),
		); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("PRAGMA user_version = 4"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var version int
	if err := store.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != currentSchemaVersion {
		t.Fatalf("schema version = %d, want %d", version, currentSchemaVersion)
	}
	var tableCount int
	if err := store.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'web_link_checks'").Scan(&tableCount); err != nil {
		t.Fatal(err)
	}
	if tableCount != 1 {
		t.Fatal("v4 to v5 migration did not create web_link_checks")
	}
	hosts, err := store.Hosts(context.Background())
	if err != nil || len(hosts) != 1 || hosts[0].ID != "host-a" {
		t.Fatalf("v4 host was not preserved: %+v, %v", hosts, err)
	}
}

func TestSQLiteUpgradesVersionTwoWorkloadsWithEmptyComposeIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lodge.db")
	createVersionOneDatabase(t, path)
	dsn, err := sqliteDSN(path)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(migrations[1].sql); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(
		"INSERT INTO schema_migrations(version, name, checksum, applied_at) VALUES (2, ?, ?, ?)",
		migrations[1].name, migrations[1].checksum(), formatTime(time.Now()),
	); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version = 2"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	result, err := db.Exec(`
INSERT INTO observations(host_id, observed_at, online, hostname, agent_version)
VALUES ('host-a', ?, 1, 'host-a', '0.2.0')`, formatTime(time.Now()))
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	observationID, err := result.LastInsertId()
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`
INSERT INTO workloads(observation_id, workload_key, kind, name)
VALUES (?, 'docker:legacy', 'docker', 'legacy')`, observationID); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	latest, found, err := store.LatestObservation(context.Background(), "host-a")
	if err != nil || !found || len(latest.Workloads) != 1 {
		t.Fatalf("migrated v2 observation was not preserved: found=%v err=%v observation=%+v", found, err, latest)
	}
	workload := latest.Workloads[0]
	if workload.ComposeProject != "" || workload.ComposeService != "" {
		t.Fatalf("legacy workload should receive empty Compose identity: %+v", workload)
	}
}

func TestBackupSQLiteFileRequiresExistingSourceAndDoesNotMigrateIt(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.db")
	if _, err := BackupSQLiteFile(context.Background(), missing, filepath.Join(dir, "missing-backup.db")); err == nil {
		t.Fatal("missing backup source should be rejected")
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("backup created a missing source: %v", err)
	}

	source := filepath.Join(dir, "v1.db")
	destination := filepath.Join(dir, "v1-backup.db")
	createVersionOneDatabase(t, source)
	version, err := BackupSQLiteFile(context.Background(), source, destination)
	if err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Fatalf("reported source version = %d, want 1", version)
	}
	for _, path := range []string{source, destination} {
		dsn, err := sqliteDSN(path)
		if err != nil {
			t.Fatal(err)
		}
		db, err := sql.Open("sqlite", dsn)
		if err != nil {
			t.Fatal(err)
		}
		var got int
		if err := db.QueryRow("PRAGMA user_version").Scan(&got); err != nil {
			db.Close()
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		if got != 1 {
			t.Fatalf("%s schema was changed during backup: version=%d", filepath.Base(path), got)
		}
	}
}

func TestSQLiteAnnotationsAndIdempotentImport(t *testing.T) {
	store, _ := openTestSQLite(t)
	ctx := context.Background()
	if err := store.SyncHosts(ctx, []domain.Host{{ID: "host-a", Name: "Host A"}}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	durable := domain.Annotation{HostID: "host-a", WorkloadKey: "docker:web", Alias: "Durable", UpdatedAt: now}
	if err := store.UpsertAnnotation(ctx, durable); err != nil {
		t.Fatal(err)
	}
	legacy := []domain.Annotation{
		{HostID: "host-a", WorkloadKey: "docker:web", Alias: "Legacy", UpdatedAt: now.Add(-time.Hour)},
		{HostID: "host-a", WorkloadKey: "systemd:caddy.service", URL: "https://admin.example.test", UpdatedAt: now},
	}
	performed, count, err := store.ImportAnnotations(ctx, "sha256:test", legacy)
	if err != nil {
		t.Fatal(err)
	}
	if !performed || count != 1 {
		t.Fatalf("import result: performed=%v count=%d", performed, count)
	}
	performed, count, err = store.ImportAnnotations(ctx, "sha256:test", legacy)
	if err != nil || performed || count != 0 {
		t.Fatalf("repeat import was not idempotent: performed=%v count=%d err=%v", performed, count, err)
	}
	loaded, err := store.Annotations(ctx, "host-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 2 || loaded[0].Alias != "Durable" || loaded[1].URL != "https://admin.example.test" {
		t.Fatalf("annotation merge mismatch: %+v", loaded)
	}
}

func TestSQLiteObservationRoundTripHistoryAndPrune(t *testing.T) {
	store, _ := openTestSQLite(t)
	ctx := context.Background()
	if err := store.SyncHosts(ctx, []domain.Host{{ID: "host-a", Name: "Host A"}}); err != nil {
		t.Fatal(err)
	}
	first := sampleObservation(time.Date(2026, 8, 7, 1, 2, 3, 456000000, time.UTC))
	second := sampleObservation(first.ObservedAt.Add(time.Minute))
	second.Online = false
	second.LastError = "test timeout"
	second.Workloads[0].State = "failed"
	if _, err := store.RecordObservation(ctx, first); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordObservation(ctx, second); err != nil {
		t.Fatal(err)
	}

	latest, found, err := store.LatestObservation(ctx, "host-a")
	if err != nil || !found {
		t.Fatalf("latest observation: found=%v err=%v", found, err)
	}
	if !reflect.DeepEqual(latest, second) {
		t.Fatalf("observation round trip mismatch:\n got: %#v\nwant: %#v", latest, second)
	}
	history, err := store.ObservationHistory(ctx, "host-a", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || !history[0].ObservedAt.Equal(second.ObservedAt) || !history[1].ObservedAt.Equal(first.ObservedAt) {
		t.Fatalf("history order is wrong: %+v", history)
	}
	summaries, err := store.ObservationSummaryHistory(ctx, "host-a", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 2 || !summaries[0].ObservedAt.Equal(second.ObservedAt) || summaries[0].Online {
		t.Fatalf("summary history order/state is wrong: %+v", summaries)
	}
	if summaries[0].WorkloadCount != 1 || summaries[0].FailedWorkloadCount != 1 || summaries[0].WildcardEndpointCount != 1 || summaries[0].WarningCount != 1 {
		t.Fatalf("summary history counts are wrong: %+v", summaries[0])
	}
	if summaries[0].MemoryUsedPct != 25 || summaries[0].DiskUsedPct != 50 || summaries[0].CPUs != 4 || summaries[0].Load1 != 0.5 {
		t.Fatalf("summary history resources are wrong: %+v", summaries[0])
	}
	if _, err := store.ObservationSummaryHistory(ctx, "host-a", 1001); err == nil {
		t.Fatal("oversized summary history limit was accepted")
	}

	deleted, err := store.PruneObservations(ctx, second.ObservedAt)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("pruned %d observations, want 1", deleted)
	}
	var workloadRows, endpointRows, routeRows int
	if err := store.db.QueryRowContext(ctx, "SELECT count(*) FROM workloads").Scan(&workloadRows); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, "SELECT count(*) FROM endpoints").Scan(&endpointRows); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, "SELECT count(*) FROM proxy_routes").Scan(&routeRows); err != nil {
		t.Fatal(err)
	}
	if workloadRows != 1 || endpointRows != 1 || routeRows != 1 {
		t.Fatalf("cascade prune failed: workloads=%d endpoints=%d routes=%d", workloadRows, endpointRows, routeRows)
	}
}

func TestSQLiteOrdersWholeAndFractionalSecondObservationsChronologically(t *testing.T) {
	store, _ := openTestSQLite(t)
	ctx := context.Background()
	if err := store.SyncHosts(ctx, []domain.Host{{ID: "host-a", Name: "Host A"}}); err != nil {
		t.Fatal(err)
	}
	wholeSecond := sampleObservation(time.Date(2026, 8, 7, 1, 2, 3, 0, time.UTC))
	fractionalSecond := sampleObservation(wholeSecond.ObservedAt.Add(time.Nanosecond))
	if _, err := store.RecordObservation(ctx, fractionalSecond); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordObservation(ctx, wholeSecond); err != nil {
		t.Fatal(err)
	}
	latest, found, err := store.LatestObservation(ctx, "host-a")
	if err != nil || !found {
		t.Fatalf("latest observation: found=%v err=%v", found, err)
	}
	if !latest.ObservedAt.Equal(fractionalSecond.ObservedAt) {
		t.Fatalf("latest timestamp = %s, want %s", latest.ObservedAt, fractionalSecond.ObservedAt)
	}
}

func TestSQLiteRejectsUnknownHostAndDuplicateHostConfig(t *testing.T) {
	store, _ := openTestSQLite(t)
	ctx := context.Background()
	if err := store.SyncHosts(ctx, []domain.Host{{ID: "same", Name: "A"}, {ID: "same", Name: "B"}}); err == nil {
		t.Fatal("duplicate configured host IDs should be rejected")
	}
	if _, err := store.RecordObservation(ctx, sampleObservation(time.Now().UTC())); err == nil {
		t.Fatal("observation for an unknown host should violate the foreign key")
	}
}

func TestSQLiteBackupIsConsistentAndDoesNotOverwrite(t *testing.T) {
	store, _ := openTestSQLite(t)
	ctx := context.Background()
	if err := store.SyncHosts(ctx, []domain.Host{{ID: "host-a", Name: "Host A"}}); err != nil {
		t.Fatal(err)
	}
	observation := sampleObservation(time.Now().UTC())
	if _, err := store.RecordObservation(ctx, observation); err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(t.TempDir(), "backups", "lodge.db")
	if err := store.Backup(ctx, backupPath); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode = %04o, want 0600", info.Mode().Perm())
	}
	backup, err := OpenSQLite(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	loaded, found, err := backup.LatestObservation(ctx, "host-a")
	if err != nil || !found || !reflect.DeepEqual(loaded, observation) {
		t.Fatalf("backup lost observation: found=%v err=%v got=%+v", found, err, loaded)
	}
	if err := store.Backup(ctx, backupPath); err == nil {
		t.Fatal("backup must not overwrite an existing destination")
	}
}

func TestSQLiteRejectsTamperedMigrationLedger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lodge.db")
	store, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("UPDATE schema_migrations SET checksum = 'tampered' WHERE version = 1"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSQLite(path); err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("tampered migration ledger was accepted: %v", err)
	}
}

func TestSQLiteRejectsNewerSchemaVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lodge.db")
	store, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("PRAGMA user_version = 999"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSQLite(path); err == nil || !strings.Contains(err.Error(), "newer than supported") {
		t.Fatalf("newer schema version was accepted: %v", err)
	}
}

func TestSQLiteRejectsLoosePermissionsAndSymlinks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lodge.db")
	store, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSQLite(path); err == nil {
		t.Fatal("group/world-readable database should be rejected")
	}
	link := filepath.Join(dir, "link.db")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSQLite(link); err == nil {
		t.Fatal("symlinked database should be rejected")
	}
}

func TestSQLiteAuxiliaryFilesAreOwnerOnly(t *testing.T) {
	store, path := openTestSQLite(t)
	if err := store.SyncHosts(context.Background(), []domain.Host{{ID: "host-a", Name: "Host A"}}); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		info, err := os.Stat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %04o, want 0600", filepath.Base(candidate), info.Mode().Perm())
		}
	}
}
