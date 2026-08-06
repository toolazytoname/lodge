// Package storage implements durable adapters for Lodge domain contracts.
package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/toolazytoname/lodge/internal/domain"
	_ "modernc.org/sqlite"
)

const (
	currentSchemaVersion = 2
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `
INSERT INTO observations(host_id, observed_at, online, last_error, hostname, agent_version, resources_json, warnings_json)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		string(observation.HostID), formatTime(observation.ObservedAt), boolInt(observation.Online), observation.LastError,
		observation.Hostname, observation.AgentVersion, resourcesJSON, string(warningsJSON),
	)
	if err != nil {
		_ = tx.Rollback()
		return 0, fmt.Errorf("insert observation: %w", err)
	}
	observationID, err := result.LastInsertId()
	if err != nil {
		_ = tx.Rollback()
		return 0, err
	}
	for _, workload := range observation.Workloads {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO workloads(observation_id, workload_key, kind, name, state, image, unit, health, pid, started_at, unidentified)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			observationID, workload.Key, string(workload.Kind), workload.Name, workload.State,
			workload.Image, workload.Unit, workload.Health, workload.PID, nullableTime(workload.StartedAt), boolInt(workload.Unidentified),
		); err != nil {
			_ = tx.Rollback()
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
			_ = tx.Rollback()
			return 0, fmt.Errorf("insert endpoint %s: %w", endpoint.Key, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return observationID, nil
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

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *SQLite) loadObservation(ctx context.Context, query queryer, id int64) (domain.Observation, error) {
	var observation domain.Observation
	var observedAt, resourcesJSON, warningsJSON sql.NullString
	var online int
	err := query.QueryRowContext(ctx, `
SELECT host_id, observed_at, online, last_error, hostname, agent_version, resources_json, warnings_json
FROM observations WHERE id = ?`, id).Scan(
		&observation.HostID, &observedAt, &online, &observation.LastError, &observation.Hostname,
		&observation.AgentVersion, &resourcesJSON, &warningsJSON,
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
	if warningsJSON.Valid && warningsJSON.String != "" {
		if err := json.Unmarshal([]byte(warningsJSON.String), &observation.Warnings); err != nil {
			return domain.Observation{}, fmt.Errorf("decode warnings: %w", err)
		}
	}

	workloadRows, err := query.QueryContext(ctx, `
SELECT workload_key, kind, name, state, image, unit, health, pid, started_at, unidentified
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
			&workload.Unit, &workload.Health, &workload.PID, &startedAt, &unidentified); err != nil {
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
