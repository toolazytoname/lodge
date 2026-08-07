package hub

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/toolazytoname/lodge/internal/domain"
	"github.com/toolazytoname/lodge/internal/shared"
)

func openTestSQLiteStore(t *testing.T, agents []AgentConfig) (*SQLiteStore, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "data", "lodge.db")
	store, err := OpenSQLiteStore(context.Background(), path, agents)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close sqlite store: %v", err)
		}
	})
	return store, path
}

func TestSQLiteStorePersistsHistoryAndAnnotationsWithoutCredentials(t *testing.T) {
	const secretToken = "agent-token-must-never-reach-sqlite-7d1048"
	agents := []AgentConfig{{
		ID: "host-a", Name: "Host A", URL: "http://host-a.tailnet:8443",
		Token: secretToken, PublicHost: "host-a.example.test",
	}}
	store, path := openTestSQLiteStore(t, agents)
	ctx := context.Background()
	observedAt := time.Date(2026, 8, 7, 6, 7, 8, 9, time.UTC)
	status := &shared.Status{
		Hostname: "host-a.internal", Load: shared.Load{CPUs: 2, One: 0.2},
		Memory: shared.Memory{TotalBytes: 1024, AvailableBytes: 768, UsedBytes: 256},
	}
	services := []shared.Service{{
		Key: "docker:web", Kind: shared.KindDocker, Name: "web", Status: "running",
		Ports: []shared.Port{{Proto: "tcp", Bind: "0.0.0.0", Port: 8080, Exposure: shared.ExposurePublic}},
	}}
	if err := store.Update(ctx, "host-a", true, "", shared.Ping{AgentVer: "0.2.0"}, status, services, observedAt); err != nil {
		t.Fatal(err)
	}
	if err := store.SetAnnotation(ctx, "host-a", "docker:web", Annotation{Alias: "Main Web", URL: "https://app.example.test"}); err != nil {
		t.Fatal(err)
	}
	history, err := store.ObservationHistory(ctx, domain.HostID("host-a"), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || !history[0].ObservedAt.Equal(observedAt) || len(history[0].Endpoints) != 1 {
		t.Fatalf("durable history mismatch: %+v", history)
	}
	if history[0].Endpoints[0].Binding != domain.BindingWildcard || history[0].Endpoints[0].Reachability != domain.ReachabilityUnknown {
		t.Fatalf("binding/reachability projection mismatch: %+v", history[0].Endpoints[0])
	}
	if got := store.Annotations("host-a")["docker:web"].Alias; got != "Main Web" {
		t.Fatalf("runtime annotation = %q", got)
	}
	assertSQLiteFilesExclude(t, path, secretToken)
}

func TestSQLiteStoreReloadsAnnotationsButNotStaleOnlineState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lodge.db")
	agents := []AgentConfig{{ID: "host-a", Name: "Host A", URL: "http://agent", Token: "runtime-only"}}
	first, err := OpenSQLiteStore(context.Background(), path, agents)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Update(context.Background(), "host-a", true, "", shared.Ping{}, nil, nil, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := first.SetAnnotation(context.Background(), "host-a", "systemd:caddy.service", Annotation{Alias: "Proxy"}); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := OpenSQLiteStore(context.Background(), path, agents)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	snapshots := second.Snapshot()
	if len(snapshots) != 1 || snapshots[0].Online {
		t.Fatalf("startup must await a fresh observation: %+v", snapshots)
	}
	if got := second.Annotations("host-a")["systemd:caddy.service"].Alias; got != "Proxy" {
		t.Fatalf("durable annotation was not reloaded: %q", got)
	}
}

func TestSQLiteStoreFailsInterruptedOperationOnRestartWithoutRetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lodge.db")
	agents := []AgentConfig{{ID: "host-a", Name: "Host A", URL: "http://agent", Token: "runtime-only"}}
	first, err := OpenSQLiteStore(context.Background(), path, agents)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(-time.Minute)
	operation := domain.Operation{
		ID: "op_interrupted", HostID: "host-a", WorkloadKey: "docker:api", Kind: domain.OperationRestart,
		State: domain.OperationRequested, RequestedBy: "session:0123456789abcdef", RequestedAt: now,
	}
	if err := first.CreateOperation(context.Background(), operation); err != nil {
		t.Fatal(err)
	}
	if _, found, err := first.StartOperation(context.Background(), operation.ID, now.Add(time.Second)); err != nil || !found {
		t.Fatalf("start interrupted operation: found=%v err=%v", found, err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := OpenSQLiteStore(context.Background(), path, agents)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	operations, err := second.Operations(context.Background(), "host-a", 10)
	if err != nil || len(operations) != 1 || operations[0].State != domain.OperationFailed || operations[0].Error != "hub_restarted" || operations[0].FinishedAt == nil {
		t.Fatalf("interrupted operation recovery mismatch: %+v err=%v", operations, err)
	}
}

func TestSQLiteStoreEvaluatesAndPersistsEventLifecycle(t *testing.T) {
	store, _ := openTestSQLiteStore(t, []AgentConfig{{ID: "host-a", Name: "Host A"}})
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	status := &shared.Status{
		Load:   shared.Load{CPUs: 2, One: 0.5},
		Memory: shared.Memory{TotalBytes: 100, UsedBytes: 86, AvailableBytes: 14},
		Disks:  []shared.Disk{{Mount: "/", TotalBytes: 100, UsedBytes: 40, FreeBytes: 60}},
	}
	services := []shared.Service{{
		Key: "docker:web", Kind: shared.KindDocker, Name: "web", Status: "running",
		Ports: []shared.Port{{Proto: "tcp", Bind: "0.0.0.0", Port: 443, Exposure: shared.ExposurePublic}},
	}}
	if err := store.Update(ctx, "host-a", true, "", shared.Ping{}, status, services, now); err != nil {
		t.Fatal(err)
	}
	active, err := store.database.ActiveEvents(ctx, "host-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].Kind != "resource.memory" {
		t.Fatalf("first observation should baseline listeners and open memory only: %+v", active)
	}

	services[0].Status = "failed"
	services[0].Ports = append(services[0].Ports, shared.Port{Proto: "tcp", Bind: "0.0.0.0", Port: 8443, Exposure: shared.ExposurePublic})
	if err := store.Update(ctx, "host-a", true, "", shared.Ping{}, status, services, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	active, err = store.database.ActiveEvents(ctx, "host-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 3 {
		t.Fatalf("failed workload and new listener should join memory event: %+v", active)
	}

	status.Memory.UsedBytes = 50
	status.Memory.AvailableBytes = 50
	if err := store.Update(ctx, "host-a", true, "", shared.Ping{}, status, nil, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	active, err = store.database.ActiveEvents(ctx, "host-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 2 || active[0].Kind == "resource.memory" || active[1].Kind == "resource.memory" {
		t.Fatalf("partial service collection should carry service risks while allowing measured memory recovery: %+v", active)
	}

	if err := store.Update(ctx, "host-a", false, "timeout", shared.Ping{}, nil, nil, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	active, err = store.database.ActiveEvents(ctx, "host-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 3 {
		t.Fatalf("offline collection should carry service risks and add host event: %+v", active)
	}

	services[0].Status = "running"
	services[0].Ports = services[0].Ports[:1]
	if err := store.Update(ctx, "host-a", true, "", shared.Ping{}, status, services, now.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	active, err = store.database.ActiveEvents(ctx, "host-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 0 {
		t.Fatalf("healthy recovery should resolve all active events: %+v", active)
	}
	timeline, err := store.database.Events(ctx, "host-a", 20)
	if err != nil || len(timeline) != 4 {
		t.Fatalf("event history should retain all four resolved incidents: %+v, %v", timeline, err)
	}
}

func TestSQLiteStoreQueuesRuleTransitionsForConfiguredNotifications(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lodge.db")
	store, err := OpenSQLiteStore(context.Background(), path,
		[]AgentConfig{{ID: "host-a", Name: "Host A"}},
		NotificationPolicy{Channel: webhookChannel, Cooldown: 15 * time.Minute},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC().Truncate(time.Second)
	status := &shared.Status{
		Load:   shared.Load{CPUs: 2, One: 0.5},
		Memory: shared.Memory{TotalBytes: 100, UsedBytes: 86, AvailableBytes: 14},
	}
	if err := store.Update(context.Background(), "host-a", true, "", shared.Ping{}, status, []shared.Service{}, now); err != nil {
		t.Fatal(err)
	}
	delivery, found, err := store.ClaimEventNotification(context.Background(), webhookChannel, now, now.Add(webhookLease))
	if err != nil || !found || delivery.Event.Kind != "resource.memory" || delivery.Transition != domain.EventOpened {
		t.Fatalf("rule transition was not queued: found=%v delivery=%+v err=%v", found, delivery, err)
	}
}

func TestSQLiteStorePersistsAndQueuesSSHAttackSourceEvent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lodge.db")
	store, err := OpenSQLiteStore(context.Background(), path,
		[]AgentConfig{{ID: "host-a", Name: "Host A"}},
		NotificationPolicy{Channel: webhookChannel, Cooldown: 15 * time.Minute},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC().Truncate(time.Second)
	status := &shared.Status{SSH: &shared.SSHAuthSummary{
		WindowStart: now.Add(-10 * time.Minute).Format(time.RFC3339), WindowEnd: now.Format(time.RFC3339),
		FailedTotal: 61, Sources: []shared.SSHAuthSource{{Address: "203.0.113.44", Count: 51}, {Address: "2001:db8::7", Count: 10}},
	}}
	if err := store.Update(context.Background(), "host-a", true, "", shared.Ping{}, status, []shared.Service{}, now); err != nil {
		t.Fatal(err)
	}
	observation, found, err := store.database.LatestObservation(context.Background(), "host-a")
	if err != nil || !found || observation.SSH == nil || observation.SSH.FailedTotal != 61 {
		t.Fatalf("SSH observation was not durable: found=%v observation=%+v err=%v", found, observation.SSH, err)
	}
	events, err := store.Events(context.Background(), "host-a", 10)
	if err != nil || len(events) != 1 || events[0].Kind != "ssh.bruteforce" || events[0].Severity != domain.SeverityCritical || !strings.Contains(events[0].Detail, "203.0.113.44 × 51") {
		t.Fatalf("SSH source event mismatch: events=%+v err=%v", events, err)
	}
	delivery, found, err := store.ClaimEventNotification(context.Background(), webhookChannel, now, now.Add(webhookLease))
	if err != nil || !found || delivery.Event.Kind != "ssh.bruteforce" {
		t.Fatalf("SSH event was not queued for notification: found=%v delivery=%+v err=%v", found, delivery, err)
	}
}

func TestLegacyStateImportsOnlyAnnotationsExactlyOnce(t *testing.T) {
	const legacyToken = "legacy-token-must-not-enter-database-a483be"
	agents := []AgentConfig{{ID: "host-a", Name: "Host A", URL: "http://agent", Token: "current-token"}}
	store, databasePath := openTestSQLiteStore(t, agents)
	legacyPath := filepath.Join(t.TempDir(), "state.json")
	legacyJSON := `{
  "agents": [{"id":"host-a","name":"old","url":"http://old","token":"` + legacyToken + `"}],
  "annotations": {
    "host-a": {"docker:web": {"alias":"Legacy Web","url":"https://legacy.example.test"}},
    "removed-host": {"docker:old": {"alias":"Skip me"}}
  }
}`
	if err := os.WriteFile(legacyPath, []byte(legacyJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := store.ImportLegacyState(context.Background(), legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Found || !result.Performed || result.ImportedAnnotations != 1 || result.SkippedUnknownHosts != 1 || result.LegacyAgentRecords != 1 {
		t.Fatalf("legacy import result: %+v", result)
	}
	if got := store.Annotations("host-a")["docker:web"].Alias; got != "Legacy Web" {
		t.Fatalf("legacy annotation was not loaded: %q", got)
	}
	repeated, err := store.ImportLegacyState(context.Background(), legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if repeated.Performed || repeated.ImportedAnnotations != 0 {
		t.Fatalf("legacy import was not idempotent: %+v", repeated)
	}
	assertSQLiteFilesExclude(t, databasePath, legacyToken)
}

func TestLegacyStateRejectsLoosePermissionsAndUnsafeAnnotations(t *testing.T) {
	store, _ := openTestSQLiteStore(t, []AgentConfig{{ID: "host-a", Name: "Host A"}})
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(`{"annotations":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ImportLegacyState(context.Background(), path); err == nil {
		t.Fatal("group/world-readable legacy state should be rejected")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"annotations":{"host-a":{"docker:web":{"url":"javascript:alert(1)"}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ImportLegacyState(context.Background(), path); err == nil {
		t.Fatal("unsafe legacy annotation should be rejected")
	}
}

type annotationFailingStore struct {
	*MemStore
}

func (s *annotationFailingStore) SetAnnotation(context.Context, string, string, Annotation) error {
	return errors.New("simulated database failure containing internal detail")
}

func TestAnnotationPersistenceFailureIsReportedWithoutInternalDetail(t *testing.T) {
	store := &annotationFailingStore{MemStore: NewMemStore()}
	if err := store.SetAgents(context.Background(), []AgentConfig{{ID: "host-a", Name: "Host A"}}); err != nil {
		t.Fatal(err)
	}
	server := newTestServer(t, store, "")
	request := newJSONRequest("POST", "/api/annotation?agent=host-a&key=docker:web", `{"alias":"Web"}`)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != 500 {
		t.Fatalf("persistence failure HTTP status = %d, want 500", response.Code)
	}
	if bytes.Contains(response.Body.Bytes(), []byte("internal detail")) {
		t.Fatal("database detail leaked to the client")
	}
}

func assertSQLiteFilesExclude(t *testing.T, databasePath, secret string) {
	t.Helper()
	for _, path := range []string{databasePath, databasePath + "-wal", databasePath + "-shm"} {
		contents, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(contents, []byte(secret)) {
			t.Fatalf("secret credential found in %s", filepath.Base(path))
		}
	}
}
