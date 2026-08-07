// Package storage implements durable adapters for Lodge domain contracts.
package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/toolazytoname/lodge/internal/domain"
	_ "modernc.org/sqlite"
)

const (
	currentSchemaVersion = 7
	// SQLite compares these TEXT timestamps lexically. A fixed-width fractional
	// component keeps whole-second and sub-second values in chronological order.
	databaseTimeLayout = "2006-01-02T15:04:05.000000000Z"
)

type SQLite struct {
	db   *sql.DB
	path string
}

func OpenSQLite(path string) (*SQLite, error) {
	if path == "" {
		return nil, errors.New("database path must not be empty")
	}
	if err := validateMigrationPlan(migrations, currentSchemaVersion); err != nil {
		return nil, err
	}
	if err := prepareDatabasePath(path); err != nil {
		return nil, err
	}
	dsn, err := sqliteDSN(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// A single connection makes connection-local PRAGMAs deterministic and is
	// sufficient for a five-host control plane. WAL still permits backup reads.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	store := &SQLite{db: db, path: path}
	if err := store.configure(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := secureDatabaseFiles(path); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := secureDatabaseFiles(path); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *SQLite) Close() error {
	return s.db.Close()
}

func (s *SQLite) configure(ctx context.Context) error {
	for _, statement := range []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
	} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("configure sqlite with %q: %w", statement, err)
		}
	}
	var foreignKeys int
	if err := s.db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		return fmt.Errorf("read sqlite foreign key setting: %w", err)
	}
	if foreignKeys != 1 {
		return fmt.Errorf("sqlite foreign keys are not active: value=%d", foreignKeys)
	}
	return nil
}

func (s *SQLite) migrate(ctx context.Context) error {
	var current int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("read sqlite schema version: %w", err)
	}
	if current > currentSchemaVersion {
		return fmt.Errorf("database schema %d is newer than supported version %d", current, currentSchemaVersion)
	}
	for _, migration := range migrations {
		if migration.version <= current {
			continue
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", migration.version, err)
		}
		if _, err := tx.ExecContext(ctx, migration.sql); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %d (%s): %w", migration.version, migration.name, err)
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO schema_migrations(version, name, checksum, applied_at) VALUES (?, ?, ?, ?)",
			migration.version, migration.name, migration.checksum(), formatTime(time.Now()),
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %d: %w", migration.version, err)
		}
		if _, err := tx.ExecContext(ctx, "PRAGMA user_version = "+strconv.Itoa(migration.version)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("set schema version %d: %w", migration.version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", migration.version, err)
		}
		current = migration.version
	}
	return s.verifyMigrations(ctx)
}

func (s *SQLite) verifyMigrations(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, "SELECT version, name, checksum FROM schema_migrations ORDER BY version")
	if err != nil {
		return fmt.Errorf("read migration ledger: %w", err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var version int
		var name, checksum string
		if err := rows.Scan(&version, &name, &checksum); err != nil {
			return err
		}
		if version < 1 || version > len(migrations) {
			return fmt.Errorf("unknown applied migration %d", version)
		}
		expected := migrations[version-1]
		if expected.version != version || expected.name != name || expected.checksum() != checksum {
			return fmt.Errorf("migration %d checksum or name mismatch", version)
		}
		seen++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if seen != currentSchemaVersion {
		return fmt.Errorf("migration ledger has %d entries, expected %d", seen, currentSchemaVersion)
	}
	return nil
}

// SyncHosts stores display metadata only. Agent URLs and bearer tokens remain
// exclusively in the owner-private runtime configuration and process memory.
func (s *SQLite) SyncHosts(ctx context.Context, hosts []domain.Host) error {
	known := make(map[domain.HostID]struct{}, len(hosts))
	for _, host := range hosts {
		if err := host.Validate(); err != nil {
			return err
		}
		if _, duplicate := known[host.ID]; duplicate {
			return fmt.Errorf("duplicate host id %q", host.ID)
		}
		known[host.ID] = struct{}{}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE hosts SET configured = 0"); err != nil {
		_ = tx.Rollback()
		return err
	}
	now := formatTime(time.Now())
	for position, host := range hosts {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO hosts(id, name, public_host, configured, display_order, created_at, updated_at)
VALUES (?, ?, ?, 1, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    name = excluded.name,
    public_host = excluded.public_host,
    configured = 1,
    display_order = excluded.display_order,
    updated_at = excluded.updated_at`,
			string(host.ID), host.Name, host.PublicHost, position, now, now,
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("sync host %s: %w", host.ID, err)
		}
	}
	return tx.Commit()
}

func (s *SQLite) Hosts(ctx context.Context) ([]domain.Host, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, name, public_host FROM hosts WHERE configured = 1 ORDER BY display_order, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var hosts []domain.Host
	for rows.Next() {
		var host domain.Host
		if err := rows.Scan(&host.ID, &host.Name, &host.PublicHost); err != nil {
			return nil, err
		}
		hosts = append(hosts, host)
	}
	return hosts, rows.Err()
}

func (s *SQLite) Annotations(ctx context.Context, hostID domain.HostID) ([]domain.Annotation, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT workload_key, alias, url, hidden, notes, updated_at
FROM annotations WHERE host_id = ? ORDER BY workload_key`, string(hostID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var annotations []domain.Annotation
	for rows.Next() {
		annotation := domain.Annotation{HostID: hostID}
		var hidden int
		var updatedAt string
		if err := rows.Scan(&annotation.WorkloadKey, &annotation.Alias, &annotation.URL, &hidden, &annotation.Notes, &updatedAt); err != nil {
			return nil, err
		}
		annotation.Hidden = hidden == 1
		annotation.UpdatedAt, err = parseTime(updatedAt)
		if err != nil {
			return nil, err
		}
		if err := annotation.Validate(); err != nil {
			return nil, fmt.Errorf("stored annotation %s/%s is invalid: %w", hostID, annotation.WorkloadKey, err)
		}
		annotations = append(annotations, annotation)
	}
	return annotations, rows.Err()
}

func (s *SQLite) UpsertAnnotation(ctx context.Context, annotation domain.Annotation) error {
	if err := annotation.Validate(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO annotations(host_id, workload_key, alias, url, hidden, notes, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(host_id, workload_key) DO UPDATE SET
    alias = excluded.alias,
    url = excluded.url,
    hidden = excluded.hidden,
    notes = excluded.notes,
    updated_at = excluded.updated_at`,
		string(annotation.HostID), annotation.WorkloadKey, annotation.Alias, annotation.URL,
		boolInt(annotation.Hidden), annotation.Notes, formatTime(annotation.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("upsert annotation %s/%s: %w", annotation.HostID, annotation.WorkloadKey, err)
	}
	return nil
}

// ReplaceWebLinkChecks atomically replaces the latest full probe set. A stale
// URL disappears as soon as it is no longer part of the current service view.
func (s *SQLite) ReplaceWebLinkChecks(ctx context.Context, checks []domain.WebLinkCheck) error {
	seen := make(map[[3]string]struct{}, len(checks))
	for _, check := range checks {
		if err := check.Validate(); err != nil {
			return err
		}
		key := [3]string{string(check.HostID), check.WorkloadKey, check.URL}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("duplicate Web link check %s/%s", check.HostID, check.WorkloadKey)
		}
		seen[key] = struct{}{}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM web_link_checks"); err != nil {
		return err
	}
	for _, check := range checks {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO web_link_checks(host_id, workload_key, url, state, http_status, latency_ms, error_kind, checked_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			string(check.HostID), check.WorkloadKey, check.URL, string(check.State), check.HTTPStatus,
			check.LatencyMS, check.ErrorKind, formatTime(check.CheckedAt),
		); err != nil {
			return fmt.Errorf("insert Web link check %s/%s: %w", check.HostID, check.WorkloadKey, err)
		}
	}
	return tx.Commit()
}

func (s *SQLite) WebLinkChecks(ctx context.Context) ([]domain.WebLinkCheck, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT host_id, workload_key, url, state, http_status, latency_ms, error_kind, checked_at
FROM web_link_checks ORDER BY host_id, workload_key, url`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var checks []domain.WebLinkCheck
	for rows.Next() {
		var check domain.WebLinkCheck
		var checkedAt string
		if err := rows.Scan(&check.HostID, &check.WorkloadKey, &check.URL, &check.State, &check.HTTPStatus,
			&check.LatencyMS, &check.ErrorKind, &checkedAt); err != nil {
			return nil, err
		}
		check.CheckedAt, err = parseTime(checkedAt)
		if err != nil {
			return nil, err
		}
		if err := check.Validate(); err != nil {
			return nil, fmt.Errorf("stored Web link check is invalid: %w", err)
		}
		checks = append(checks, check)
	}
	return checks, rows.Err()
}

// ImportAnnotations atomically imports legacy annotations exactly once per
// opaque import ID. Existing durable annotations win over imported values.
func (s *SQLite) ImportAnnotations(ctx context.Context, importID string, annotations []domain.Annotation) (bool, int64, error) {
	if importID == "" || len(importID) > 128 {
		return false, 0, errors.New("annotation import ID must be between 1 and 128 bytes")
	}
	for _, annotation := range annotations {
		if err := annotation.Validate(); err != nil {
			return false, 0, err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, 0, err
	}
	defer tx.Rollback()
	var exists int
	err = tx.QueryRowContext(ctx, "SELECT 1 FROM annotation_imports WHERE id = ?", importID).Scan(&exists)
	if err == nil {
		return false, 0, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, 0, err
	}
	var imported int64
	for _, annotation := range annotations {
		result, err := tx.ExecContext(ctx, `
INSERT INTO annotations(host_id, workload_key, alias, url, hidden, notes, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(host_id, workload_key) DO NOTHING`,
			string(annotation.HostID), annotation.WorkloadKey, annotation.Alias, annotation.URL,
			boolInt(annotation.Hidden), annotation.Notes, formatTime(annotation.UpdatedAt),
		)
		if err != nil {
			return false, 0, fmt.Errorf("import annotation %s/%s: %w", annotation.HostID, annotation.WorkloadKey, err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return false, 0, err
		}
		imported += rows
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO annotation_imports(id, imported_at, imported_count) VALUES (?, ?, ?)",
		importID, formatTime(time.Now()), imported,
	); err != nil {
		return false, 0, err
	}
	if err := tx.Commit(); err != nil {
		return false, 0, err
	}
	return true, imported, nil
}

func (s *SQLite) RecordObservation(ctx context.Context, observation domain.Observation) (int64, error) {
	if err := observation.Validate(); err != nil {
		return 0, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	observationID, err := recordObservationTx(ctx, tx, observation)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return observationID, nil
}

// RecordObservationWithEvents persists telemetry and reconciles the current
// rule truth in one transaction. A failed event write can therefore never
// leave an observation without its corresponding lifecycle transition.
func (s *SQLite) RecordObservationWithEvents(ctx context.Context, observation domain.Observation, signals []domain.EventSignal) (int64, []domain.EventTransition, error) {
	return s.recordObservationWithEvents(ctx, observation, signals, nil)
}

// RecordObservationWithNotifications adds durable outbox rows in the same
// transaction as the observation and event transitions.
func (s *SQLite) RecordObservationWithNotifications(ctx context.Context, observation domain.Observation, signals []domain.EventSignal, policies []NotificationChannelPolicy) (int64, []domain.EventTransition, error) {
	return s.recordObservationWithEvents(ctx, observation, signals, policies)
}

func (s *SQLite) recordObservationWithEvents(ctx context.Context, observation domain.Observation, signals []domain.EventSignal, policies []NotificationChannelPolicy) (int64, []domain.EventTransition, error) {
	if err := observation.Validate(); err != nil {
		return 0, nil, err
	}
	if err := validateNotificationPolicies(policies); err != nil {
		return 0, nil, err
	}
	seen := make(map[string]struct{}, len(signals))
	for _, signal := range signals {
		if err := signal.Validate(); err != nil {
			return 0, nil, err
		}
		if signal.HostID != observation.HostID {
			return 0, nil, errors.New("event signal belongs to a different host")
		}
		if _, duplicate := seen[signal.DedupeKey]; duplicate {
			return 0, nil, fmt.Errorf("duplicate event signal %q", signal.DedupeKey)
		}
		seen[signal.DedupeKey] = struct{}{}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, nil, err
	}
	defer tx.Rollback()
	observationID, err := recordObservationTx(ctx, tx, observation)
	if err != nil {
		return 0, nil, err
	}
	transitions, err := reconcileEventSignalsTx(ctx, tx, observation.HostID, observation.ObservedAt, signals)
	if err != nil {
		return 0, nil, err
	}
	if err := enqueueEventNotificationsTx(ctx, tx, transitions, policies, observation.ObservedAt); err != nil {
		return 0, nil, err
	}
	if err := tx.Commit(); err != nil {
		return 0, nil, err
	}
	return observationID, transitions, nil
}

func recordObservationTx(ctx context.Context, tx *sql.Tx, observation domain.Observation) (int64, error) {
	var resourcesJSON any
	if observation.Resources != nil {
		encoded, err := json.Marshal(observation.Resources)
		if err != nil {
			return 0, err
		}
		resourcesJSON = string(encoded)
	}
	warningsJSON, err := json.Marshal(observation.Warnings)
	if err != nil {
		return 0, err
	}
	var sshAuthJSON any
	if observation.SSH != nil {
		encoded, err := json.Marshal(observation.SSH)
		if err != nil {
			return 0, err
		}
		sshAuthJSON = string(encoded)
	}
	result, err := tx.ExecContext(ctx, `
INSERT INTO observations(host_id, observed_at, online, last_error, hostname, agent_version, resources_json, ssh_auth_json, warnings_json)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(observation.HostID), formatTime(observation.ObservedAt), boolInt(observation.Online), observation.LastError,
		observation.Hostname, observation.AgentVersion, resourcesJSON, sshAuthJSON, string(warningsJSON),
	)
	if err != nil {
		return 0, fmt.Errorf("insert observation: %w", err)
	}
	observationID, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	for _, workload := range observation.Workloads {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO workloads(observation_id, workload_key, kind, name, state, image, unit, compose_project, compose_service, health, pid, started_at, unidentified)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			observationID, workload.Key, string(workload.Kind), workload.Name, workload.State,
			workload.Image, workload.Unit, workload.ComposeProject, workload.ComposeService,
			workload.Health, workload.PID, nullableTime(workload.StartedAt), boolInt(workload.Unidentified),
		); err != nil {
			return 0, fmt.Errorf("insert workload %s: %w", workload.Key, err)
		}
	}
	for _, endpoint := range observation.Endpoints {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO endpoints(observation_id, workload_key, endpoint_key, protocol, bind, port, binding, reachability, reachability_source, reachability_checked_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			observationID, endpoint.WorkloadKey, endpoint.Key, endpoint.Protocol, endpoint.Bind, endpoint.Port,
			string(endpoint.Binding), string(endpoint.Reachability), endpoint.ReachabilitySource, nullableTime(endpoint.ReachabilityCheckedAt),
		); err != nil {
			return 0, fmt.Errorf("insert endpoint %s: %w", endpoint.Key, err)
		}
	}
	for _, route := range observation.Routes {
		upstreamsJSON, err := json.Marshal(route.Upstreams)
		if err != nil {
			return 0, fmt.Errorf("encode proxy route %s upstreams: %w", route.Key, err)
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO proxy_routes(observation_id, workload_key, route_key, scheme, host, port, path, upstreams_json)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			observationID, route.WorkloadKey, route.Key, route.Scheme, route.Host, route.Port, route.Path, string(upstreamsJSON),
		); err != nil {
			return 0, fmt.Errorf("insert proxy route %s: %w", route.Key, err)
		}
	}
	return observationID, nil
}

func reconcileEventSignalsTx(ctx context.Context, tx *sql.Tx, hostID domain.HostID, observedAt time.Time, signals []domain.EventSignal) ([]domain.EventTransition, error) {
	active, err := loadEventsTx(ctx, tx, `
WHERE host_id = ? AND state != 'resolved'
ORDER BY dedupe_key`, string(hostID))
	if err != nil {
		return nil, err
	}
	activeByKey := make(map[string]domain.Event, len(active))
	for _, event := range active {
		activeByKey[event.DedupeKey] = event
	}
	signalsByKey := make(map[string]domain.EventSignal, len(signals))
	for _, signal := range signals {
		signalsByKey[signal.DedupeKey] = signal
	}
	keys := make([]string, 0, len(signalsByKey))
	for key := range signalsByKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	transitions := make([]domain.EventTransition, 0)
	for _, key := range keys {
		signal := signalsByKey[key]
		if event, exists := activeByKey[key]; exists {
			if observedAt.Before(event.LastObservedAt) {
				return nil, fmt.Errorf("event signal %q predates its latest observation", key)
			}
			event.Kind = signal.Kind
			event.Severity = signal.Severity
			event.Title = signal.Title
			event.Detail = signal.Detail
			event.LastObservedAt = observedAt.UTC()
			if err := event.Validate(); err != nil {
				return nil, err
			}
			if _, err := tx.ExecContext(ctx, `
UPDATE events SET kind = ?, severity = ?, title = ?, detail = ?, last_observed_at = ?
WHERE id = ?`, event.Kind, event.Severity, event.Title, event.Detail, formatTime(event.LastObservedAt), event.ID); err != nil {
				return nil, fmt.Errorf("update active event %s: %w", event.ID, err)
			}
			continue
		}
		id, err := newEventID()
		if err != nil {
			return nil, err
		}
		event := domain.Event{
			ID: id, HostID: hostID, Kind: signal.Kind, Severity: signal.Severity,
			State: domain.EventActive, DedupeKey: signal.DedupeKey,
			Title: signal.Title, Detail: signal.Detail,
			FirstObservedAt: observedAt.UTC(), LastObservedAt: observedAt.UTC(),
		}
		if err := event.Validate(); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO events(id, host_id, kind, severity, state, dedupe_key, title, detail, first_observed_at, last_observed_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, event.ID, event.HostID, event.Kind, event.Severity,
			event.State, event.DedupeKey, event.Title, event.Detail,
			formatTime(event.FirstObservedAt), formatTime(event.LastObservedAt)); err != nil {
			return nil, fmt.Errorf("insert active event %s: %w", event.ID, err)
		}
		transitions = append(transitions, domain.EventTransition{Type: domain.EventOpened, Event: event})
	}
	staleKeys := make([]string, 0)
	for key := range activeByKey {
		if _, current := signalsByKey[key]; !current {
			staleKeys = append(staleKeys, key)
		}
	}
	sort.Strings(staleKeys)
	for _, key := range staleKeys {
		event := activeByKey[key]
		if observedAt.Before(event.LastObservedAt) {
			return nil, fmt.Errorf("event recovery %q predates its latest observation", key)
		}
		resolvedAt := observedAt.UTC()
		event.State = domain.EventResolved
		event.ResolvedAt = &resolvedAt
		if err := event.Validate(); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE events SET state = 'resolved', resolved_at = ? WHERE id = ?`, formatTime(resolvedAt), event.ID); err != nil {
			return nil, fmt.Errorf("resolve event %s: %w", event.ID, err)
		}
		transitions = append(transitions, domain.EventTransition{Type: domain.EventRecovered, Event: event})
	}
	return transitions, nil
}

// ActiveEvents returns the unresolved rule state used by the next evaluation.
func (s *SQLite) ActiveEvents(ctx context.Context, hostID domain.HostID) ([]domain.Event, error) {
	if strings.TrimSpace(string(hostID)) == "" {
		return nil, errors.New("event host id must not be empty")
	}
	return loadEvents(ctx, s.db, `
WHERE host_id = ? AND state != 'resolved'
ORDER BY dedupe_key`, string(hostID))
}

// Events returns a bounded operator timeline. Empty hostID means every host.
func (s *SQLite) Events(ctx context.Context, hostID domain.HostID, limit int) ([]domain.Event, error) {
	if limit < 1 || limit > 500 {
		return nil, errors.New("event limit must be between 1 and 500")
	}
	order := `ORDER BY CASE state WHEN 'active' THEN 0 WHEN 'acknowledged' THEN 1 ELSE 2 END,
last_observed_at DESC, id DESC LIMIT ?`
	if hostID == "" {
		return loadEvents(ctx, s.db, order, limit)
	}
	return loadEvents(ctx, s.db, "WHERE host_id = ?\n"+order, string(hostID), limit)
}

var ErrEventResolved = errors.New("event already resolved")

// AcknowledgeEvent is idempotent. It records operator awareness while leaving
// the event unresolved until a later observation proves recovery.
func (s *SQLite) AcknowledgeEvent(ctx context.Context, id string, acknowledgedAt time.Time) (domain.Event, bool, error) {
	if strings.TrimSpace(id) == "" || len(id) > 128 || acknowledgedAt.IsZero() {
		return domain.Event{}, false, errors.New("event acknowledgement is invalid")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Event{}, false, err
	}
	defer tx.Rollback()
	events, err := loadEventsTx(ctx, tx, "WHERE id = ?", id)
	if err != nil {
		return domain.Event{}, false, err
	}
	if len(events) == 0 {
		return domain.Event{}, false, nil
	}
	event := events[0]
	if event.State == domain.EventResolved {
		return domain.Event{}, true, ErrEventResolved
	}
	if event.State == domain.EventAcknowledged {
		if err := tx.Commit(); err != nil {
			return domain.Event{}, false, err
		}
		return event, true, nil
	}
	acknowledgedAt = acknowledgedAt.UTC()
	if acknowledgedAt.Before(event.FirstObservedAt) {
		return domain.Event{}, true, errors.New("event acknowledgement predates the event")
	}
	event.State = domain.EventAcknowledged
	event.AcknowledgedAt = &acknowledgedAt
	if err := event.Validate(); err != nil {
		return domain.Event{}, true, err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE events SET state = 'acknowledged', acknowledged_at = ? WHERE id = ? AND state = 'active'`,
		formatTime(acknowledgedAt), event.ID); err != nil {
		return domain.Event{}, true, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Event{}, true, err
	}
	return event, true, nil
}

const eventColumns = `id, host_id, kind, severity, state, dedupe_key, title, detail,
first_observed_at, last_observed_at, acknowledged_at, resolved_at`

type eventQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func loadEvents(ctx context.Context, queryer eventQueryer, suffix string, args ...any) ([]domain.Event, error) {
	rows, err := queryer.QueryContext(ctx, "SELECT "+eventColumns+" FROM events\n"+suffix, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]domain.Event, 0)
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func loadEventsTx(ctx context.Context, tx *sql.Tx, suffix string, args ...any) ([]domain.Event, error) {
	return loadEvents(ctx, tx, suffix, args...)
}

type rowScanner interface {
	Scan(...any) error
}

func scanEvent(scanner rowScanner) (domain.Event, error) {
	var event domain.Event
	var firstObservedAt, lastObservedAt string
	var acknowledgedAt, resolvedAt sql.NullString
	if err := scanner.Scan(&event.ID, &event.HostID, &event.Kind, &event.Severity, &event.State,
		&event.DedupeKey, &event.Title, &event.Detail, &firstObservedAt, &lastObservedAt,
		&acknowledgedAt, &resolvedAt); err != nil {
		return domain.Event{}, err
	}
	var err error
	event.FirstObservedAt, err = parseTime(firstObservedAt)
	if err != nil {
		return domain.Event{}, err
	}
	event.LastObservedAt, err = parseTime(lastObservedAt)
	if err != nil {
		return domain.Event{}, err
	}
	if acknowledgedAt.Valid {
		parsed, err := parseTime(acknowledgedAt.String)
		if err != nil {
			return domain.Event{}, err
		}
		event.AcknowledgedAt = &parsed
	}
	if resolvedAt.Valid {
		parsed, err := parseTime(resolvedAt.String)
		if err != nil {
			return domain.Event{}, err
		}
		event.ResolvedAt = &parsed
	}
	if err := event.Validate(); err != nil {
		return domain.Event{}, fmt.Errorf("stored event is invalid: %w", err)
	}
	return event, nil
}

func newEventID() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate event id: %w", err)
	}
	return "evt_" + hex.EncodeToString(random[:]), nil
}

func (s *SQLite) LatestObservation(ctx context.Context, hostID domain.HostID) (domain.Observation, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Observation{}, false, err
	}
	defer tx.Rollback()
	var id int64
	err = tx.QueryRowContext(ctx,
		"SELECT id FROM observations WHERE host_id = ? ORDER BY observed_at DESC, id DESC LIMIT 1", string(hostID),
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Observation{}, false, nil
	}
	if err != nil {
		return domain.Observation{}, false, err
	}
	observation, err := s.loadObservation(ctx, tx, id)
	if err != nil {
		return domain.Observation{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Observation{}, false, err
	}
	return observation, true, nil
}

func (s *SQLite) ObservationHistory(ctx context.Context, hostID domain.HostID, limit int) ([]domain.Observation, error) {
	if limit < 1 || limit > 1000 {
		return nil, errors.New("history limit must be between 1 and 1000")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx,
		"SELECT id FROM observations WHERE host_id = ? ORDER BY observed_at DESC, id DESC LIMIT ?", string(hostID), limit,
	)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	observations := make([]domain.Observation, 0, len(ids))
	for _, id := range ids {
		observation, err := s.loadObservation(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		observations = append(observations, observation)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return observations, nil
}

// ObservationSummaryHistory loads timeline-sized projections in one query.
// Full workload, endpoint, route, and resource payloads stay out of the API
// path even when an operator asks for hundreds of points.
func (s *SQLite) ObservationSummaryHistory(ctx context.Context, hostID domain.HostID, limit int) ([]domain.ObservationSummary, error) {
	if limit < 1 || limit > 1000 {
		return nil, errors.New("history limit must be between 1 and 1000")
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT o.host_id, o.observed_at, o.online, o.last_error, o.agent_version,
       o.resources_json, o.warnings_json,
       (SELECT count(*) FROM workloads w WHERE w.observation_id = o.id),
       (SELECT count(*) FROM workloads w WHERE w.observation_id = o.id
          AND (lower(trim(w.state)) = 'failed' OR lower(trim(w.health)) = 'unhealthy')),
       (SELECT count(*) FROM endpoints e WHERE e.observation_id = o.id AND e.binding = 'wildcard')
FROM observations o
WHERE o.host_id = ?
ORDER BY o.observed_at DESC, o.id DESC
LIMIT ?`, string(hostID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	summaries := make([]domain.ObservationSummary, 0, limit)
	for rows.Next() {
		var summary domain.ObservationSummary
		var observedAt string
		var online int
		var resourcesJSON, warningsJSON sql.NullString
		if err := rows.Scan(
			&summary.HostID, &observedAt, &online, &summary.LastError, &summary.AgentVersion,
			&resourcesJSON, &warningsJSON, &summary.WorkloadCount, &summary.FailedWorkloadCount,
			&summary.WildcardEndpointCount,
		); err != nil {
			return nil, err
		}
		summary.ObservedAt, err = parseTime(observedAt)
		if err != nil {
			return nil, err
		}
		summary.Online = online == 1
		if resourcesJSON.Valid && resourcesJSON.String != "" {
			var resources domain.Resources
			if err := json.Unmarshal([]byte(resourcesJSON.String), &resources); err != nil {
				return nil, fmt.Errorf("decode summary resources: %w", err)
			}
			summary.CPUs = resources.CPUs
			summary.Load1 = resources.Load1
			summary.MemoryUsedPct = domain.UsagePercent(resources.Memory.UsedBytes, resources.Memory.TotalBytes)
			for _, disk := range resources.Disks {
				if disk.Mount == "/" {
					summary.DiskUsedPct = domain.UsagePercent(disk.UsedBytes, disk.TotalBytes)
					break
				}
			}
		}
		if warningsJSON.Valid && warningsJSON.String != "" {
			var warnings []string
			if err := json.Unmarshal([]byte(warningsJSON.String), &warnings); err != nil {
				return nil, fmt.Errorf("decode summary warnings: %w", err)
			}
			summary.WarningCount = len(warnings)
		}
		if err := summary.Validate(); err != nil {
			return nil, fmt.Errorf("stored observation summary is invalid: %w", err)
		}
		summaries = append(summaries, summary)
	}
	return summaries, rows.Err()
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *SQLite) loadObservation(ctx context.Context, query queryer, id int64) (domain.Observation, error) {
	var observation domain.Observation
	var observedAt, resourcesJSON, sshAuthJSON, warningsJSON sql.NullString
	var online int
	err := query.QueryRowContext(ctx, `
SELECT host_id, observed_at, online, last_error, hostname, agent_version, resources_json, ssh_auth_json, warnings_json
FROM observations WHERE id = ?`, id).Scan(
		&observation.HostID, &observedAt, &online, &observation.LastError, &observation.Hostname,
		&observation.AgentVersion, &resourcesJSON, &sshAuthJSON, &warningsJSON,
	)
	if err != nil {
		return domain.Observation{}, err
	}
	observation.ObservedAt, err = parseTime(observedAt.String)
	if err != nil {
		return domain.Observation{}, err
	}
	observation.Online = online == 1
	if resourcesJSON.Valid {
		observation.Resources = &domain.Resources{}
		if err := json.Unmarshal([]byte(resourcesJSON.String), observation.Resources); err != nil {
			return domain.Observation{}, fmt.Errorf("decode resources: %w", err)
		}
	}
	if sshAuthJSON.Valid {
		observation.SSH = &domain.SSHAuthObservation{}
		if err := json.Unmarshal([]byte(sshAuthJSON.String), observation.SSH); err != nil {
			return domain.Observation{}, fmt.Errorf("decode SSH authentication summary: %w", err)
		}
	}
	if warningsJSON.Valid && warningsJSON.String != "" {
		if err := json.Unmarshal([]byte(warningsJSON.String), &observation.Warnings); err != nil {
			return domain.Observation{}, fmt.Errorf("decode warnings: %w", err)
		}
	}

	workloadRows, err := query.QueryContext(ctx, `
SELECT workload_key, kind, name, state, image, unit, compose_project, compose_service, health, pid, started_at, unidentified
FROM workloads WHERE observation_id = ? ORDER BY workload_key`, id)
	if err != nil {
		return domain.Observation{}, err
	}
	for workloadRows.Next() {
		var workload domain.Workload
		var startedAt sql.NullString
		var unidentified int
		workload.HostID = observation.HostID
		if err := workloadRows.Scan(&workload.Key, &workload.Kind, &workload.Name, &workload.State, &workload.Image,
			&workload.Unit, &workload.ComposeProject, &workload.ComposeService,
			&workload.Health, &workload.PID, &startedAt, &unidentified); err != nil {
			_ = workloadRows.Close()
			return domain.Observation{}, err
		}
		if startedAt.Valid {
			parsed, err := parseTime(startedAt.String)
			if err != nil {
				_ = workloadRows.Close()
				return domain.Observation{}, err
			}
			workload.StartedAt = &parsed
		}
		workload.Unidentified = unidentified == 1
		observation.Workloads = append(observation.Workloads, workload)
	}
	if err := workloadRows.Err(); err != nil {
		_ = workloadRows.Close()
		return domain.Observation{}, err
	}
	if err := workloadRows.Close(); err != nil {
		return domain.Observation{}, err
	}

	endpointRows, err := query.QueryContext(ctx, `
SELECT workload_key, endpoint_key, protocol, bind, port, binding, reachability, reachability_source, reachability_checked_at
FROM endpoints WHERE observation_id = ? ORDER BY workload_key, endpoint_key`, id)
	if err != nil {
		return domain.Observation{}, err
	}
	for endpointRows.Next() {
		var endpoint domain.Endpoint
		var checkedAt sql.NullString
		endpoint.HostID = observation.HostID
		if err := endpointRows.Scan(&endpoint.WorkloadKey, &endpoint.Key, &endpoint.Protocol, &endpoint.Bind, &endpoint.Port,
			&endpoint.Binding, &endpoint.Reachability, &endpoint.ReachabilitySource, &checkedAt); err != nil {
			_ = endpointRows.Close()
			return domain.Observation{}, err
		}
		if checkedAt.Valid {
			parsed, err := parseTime(checkedAt.String)
			if err != nil {
				_ = endpointRows.Close()
				return domain.Observation{}, err
			}
			endpoint.ReachabilityCheckedAt = &parsed
		}
		observation.Endpoints = append(observation.Endpoints, endpoint)
	}
	if err := endpointRows.Err(); err != nil {
		_ = endpointRows.Close()
		return domain.Observation{}, err
	}
	if err := endpointRows.Close(); err != nil {
		return domain.Observation{}, err
	}

	routeRows, err := query.QueryContext(ctx, `
SELECT workload_key, route_key, scheme, host, port, path, upstreams_json
FROM proxy_routes WHERE observation_id = ? ORDER BY workload_key, route_key`, id)
	if err != nil {
		return domain.Observation{}, err
	}
	for routeRows.Next() {
		var route domain.ProxyRoute
		var upstreamsJSON string
		route.HostID = observation.HostID
		if err := routeRows.Scan(&route.WorkloadKey, &route.Key, &route.Scheme, &route.Host, &route.Port, &route.Path, &upstreamsJSON); err != nil {
			_ = routeRows.Close()
			return domain.Observation{}, err
		}
		if err := json.Unmarshal([]byte(upstreamsJSON), &route.Upstreams); err != nil {
			_ = routeRows.Close()
			return domain.Observation{}, fmt.Errorf("decode proxy route %s upstreams: %w", route.Key, err)
		}
		observation.Routes = append(observation.Routes, route)
	}
	if err := routeRows.Err(); err != nil {
		_ = routeRows.Close()
		return domain.Observation{}, err
	}
	if err := routeRows.Close(); err != nil {
		return domain.Observation{}, err
	}
	if err := observation.Validate(); err != nil {
		return domain.Observation{}, fmt.Errorf("stored observation %d is invalid: %w", id, err)
	}
	return observation, nil
}

func (s *SQLite) PruneObservations(ctx context.Context, before time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, "DELETE FROM observations WHERE observed_at < ?", formatTime(before))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// Backup creates a transactionally consistent standalone database. Directly
// copying the main file is unsafe while WAL mode is active.
func (s *SQLite) Backup(ctx context.Context, destination string) error {
	return backupSQLite(ctx, s.db, destination, currentSchemaVersion)
}

// BackupSQLiteFile backs up an existing database without running migrations on
// the source. This preserves a true pre-upgrade rollback point.
func BackupSQLiteFile(ctx context.Context, source, destination string) (int, error) {
	if source == "" || source == ":memory:" {
		return 0, errors.New("backup source must be an existing file path")
	}
	if _, err := os.Lstat(source); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, fmt.Errorf("backup source does not exist: %s", source)
		}
		return 0, err
	}
	if err := prepareDatabasePath(source); err != nil {
		return 0, err
	}
	dsn, err := sqliteDSN(source)
	if err != nil {
		return 0, err
	}
	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		return 0, err
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	store := &SQLite{db: database, path: source}
	if err := store.configure(ctx); err != nil {
		return 0, err
	}
	if err := secureDatabaseFiles(source); err != nil {
		return 0, err
	}
	var sourceVersion int
	if err := database.QueryRowContext(ctx, "PRAGMA user_version").Scan(&sourceVersion); err != nil {
		return 0, fmt.Errorf("read source schema version: %w", err)
	}
	if sourceVersion < 1 {
		return 0, errors.New("backup source is not an initialized Lodge database")
	}
	if err := backupSQLite(ctx, database, destination, sourceVersion); err != nil {
		return 0, err
	}
	return sourceVersion, nil
}

func backupSQLite(ctx context.Context, database *sql.DB, destination string, expectedVersion int) error {
	if destination == "" || destination == ":memory:" {
		return errors.New("backup destination must be a file path")
	}
	if _, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("backup destination already exists: %s", destination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		return err
	}
	absDestination, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	if _, err := database.ExecContext(ctx, "VACUUM INTO ?", absDestination); err != nil {
		_ = os.Remove(destination)
		return fmt.Errorf("backup sqlite: %w", err)
	}
	backupComplete := false
	defer func() {
		if !backupComplete {
			_ = os.Remove(destination)
		}
	}()
	if err := os.Chmod(destination, 0o600); err != nil {
		return err
	}
	backupDSN, err := sqliteDSN(absDestination)
	if err != nil {
		return err
	}
	backupDB, err := sql.Open("sqlite", backupDSN)
	if err != nil {
		return err
	}
	defer backupDB.Close()
	var integrity string
	if err := backupDB.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
		return fmt.Errorf("read backup integrity: %w", err)
	}
	if integrity != "ok" {
		return fmt.Errorf("backup integrity check failed: result=%q", integrity)
	}
	var version int
	if err := backupDB.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read backup schema version: %w", err)
	}
	if version != expectedVersion {
		return fmt.Errorf("backup schema check failed: version=%d, source=%d", version, expectedVersion)
	}
	backupComplete = true
	return nil
}

func prepareDatabasePath(path string) error {
	if path == ":memory:" {
		return nil
	}
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return errors.New("database must be a regular file, not a symlink")
		}
		if info.Mode().Perm() != 0o600 {
			return fmt.Errorf("database %s must be owner-only (0600)", path)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("create owner-only database: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close new database: %w", err)
	}
	return nil
}

func sqliteDSN(path string) (string, error) {
	if path == ":memory:" {
		return ":memory:", nil
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return (&url.URL{Scheme: "file", Path: absPath}).String(), nil
}

func secureDatabaseFiles(path string) error {
	if path == ":memory:" {
		return nil
	}
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Chmod(candidate, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("secure sqlite file %s: %w", candidate, err)
		}
	}
	return nil
}

func formatTime(value time.Time) string {
	return value.UTC().Format(databaseTimeLayout)
}

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse database time %q: %w", value, err)
	}
	return parsed.UTC(), nil
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatTime(*value)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
