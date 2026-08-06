package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

type migration struct {
	version int
	name    string
	sql     string
}

func validateMigrationPlan(plan []migration, target int) error {
	if target < 1 || len(plan) != target {
		return fmt.Errorf("migration plan has %d entries, expected %d", len(plan), target)
	}
	seenNames := make(map[string]struct{}, len(plan))
	for index, item := range plan {
		expectedVersion := index + 1
		if item.version != expectedVersion {
			return fmt.Errorf("migration at index %d has version %d, expected %d", index, item.version, expectedVersion)
		}
		if strings.TrimSpace(item.name) == "" || strings.TrimSpace(item.sql) == "" {
			return fmt.Errorf("migration %d must have a name and SQL", item.version)
		}
		if _, duplicate := seenNames[item.name]; duplicate {
			return fmt.Errorf("duplicate migration name %q", item.name)
		}
		seenNames[item.name] = struct{}{}
	}
	return nil
}

func (m migration) checksum() string {
	sum := sha256.Sum256([]byte(m.sql))
	return hex.EncodeToString(sum[:])
}

var migrations = []migration{
	{
		version: 1,
		name:    "durable_fleet_domain",
		sql: `
CREATE TABLE schema_migrations (
    version INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    checksum TEXT NOT NULL,
    applied_at TEXT NOT NULL
) STRICT;

CREATE TABLE hosts (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    public_host TEXT NOT NULL DEFAULT '',
    configured INTEGER NOT NULL DEFAULT 1 CHECK (configured IN (0, 1)),
    display_order INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;

CREATE TABLE observations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    host_id TEXT NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    observed_at TEXT NOT NULL,
    online INTEGER NOT NULL CHECK (online IN (0, 1)),
    last_error TEXT NOT NULL DEFAULT '',
    hostname TEXT NOT NULL DEFAULT '',
    agent_version TEXT NOT NULL DEFAULT '',
    resources_json TEXT,
    warnings_json TEXT NOT NULL DEFAULT '[]'
) STRICT;

CREATE INDEX observations_host_time_idx
    ON observations(host_id, observed_at DESC, id DESC);

CREATE TABLE workloads (
    observation_id INTEGER NOT NULL REFERENCES observations(id) ON DELETE CASCADE,
    workload_key TEXT NOT NULL,
    kind TEXT NOT NULL,
    name TEXT NOT NULL,
    state TEXT NOT NULL DEFAULT '',
    image TEXT NOT NULL DEFAULT '',
    unit TEXT NOT NULL DEFAULT '',
    health TEXT NOT NULL DEFAULT '',
    pid INTEGER NOT NULL DEFAULT 0,
    started_at TEXT,
    unidentified INTEGER NOT NULL DEFAULT 0 CHECK (unidentified IN (0, 1)),
    PRIMARY KEY (observation_id, workload_key)
) STRICT;

CREATE TABLE endpoints (
    observation_id INTEGER NOT NULL,
    workload_key TEXT NOT NULL,
    endpoint_key TEXT NOT NULL,
    protocol TEXT NOT NULL,
    bind TEXT NOT NULL,
    port INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535),
    binding TEXT NOT NULL,
    reachability TEXT NOT NULL,
    reachability_source TEXT NOT NULL DEFAULT '',
    reachability_checked_at TEXT,
    PRIMARY KEY (observation_id, workload_key, endpoint_key),
    FOREIGN KEY (observation_id, workload_key)
        REFERENCES workloads(observation_id, workload_key) ON DELETE CASCADE
) STRICT;

CREATE INDEX endpoints_binding_idx ON endpoints(binding, port);
CREATE INDEX endpoints_reachability_idx ON endpoints(reachability, port);

CREATE TABLE annotations (
    host_id TEXT NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    workload_key TEXT NOT NULL,
    alias TEXT NOT NULL DEFAULT '',
    url TEXT NOT NULL DEFAULT '',
    hidden INTEGER NOT NULL DEFAULT 0 CHECK (hidden IN (0, 1)),
    notes TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL,
    PRIMARY KEY (host_id, workload_key)
) STRICT;

CREATE TABLE events (
    id TEXT PRIMARY KEY,
    host_id TEXT NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    severity TEXT NOT NULL,
    state TEXT NOT NULL,
    dedupe_key TEXT NOT NULL,
    title TEXT NOT NULL,
    detail TEXT NOT NULL DEFAULT '',
    first_observed_at TEXT NOT NULL,
    last_observed_at TEXT NOT NULL,
    acknowledged_at TEXT,
    resolved_at TEXT
) STRICT;

CREATE UNIQUE INDEX events_active_dedupe_idx
    ON events(dedupe_key) WHERE state != 'resolved';
CREATE INDEX events_state_time_idx ON events(state, last_observed_at DESC);

CREATE TABLE operations (
    id TEXT PRIMARY KEY,
    host_id TEXT NOT NULL REFERENCES hosts(id) ON DELETE RESTRICT,
    workload_key TEXT NOT NULL DEFAULT '',
    kind TEXT NOT NULL,
    state TEXT NOT NULL,
    requested_by TEXT NOT NULL,
    requested_at TEXT NOT NULL,
    started_at TEXT,
    finished_at TEXT,
    result_summary TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX operations_requested_at_idx ON operations(requested_at DESC);
`,
	},
}
