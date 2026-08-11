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
	{
		version: 2,
		name:    "annotation_import_ledger",
		sql: `
CREATE TABLE annotation_imports (
    id TEXT PRIMARY KEY,
    imported_at TEXT NOT NULL,
    imported_count INTEGER NOT NULL CHECK (imported_count >= 0)
) STRICT;
`,
	},
	{
		version: 3,
		name:    "compose_workload_identity",
		sql: `
ALTER TABLE workloads ADD COLUMN compose_project TEXT NOT NULL DEFAULT '';
ALTER TABLE workloads ADD COLUMN compose_service TEXT NOT NULL DEFAULT '';
`,
	},
	{
		version: 4,
		name:    "redacted_proxy_routes",
		sql: `
CREATE TABLE proxy_routes (
    observation_id INTEGER NOT NULL,
    workload_key TEXT NOT NULL,
    route_key TEXT NOT NULL,
    scheme TEXT NOT NULL CHECK (scheme IN ('http', 'https')),
    host TEXT NOT NULL DEFAULT '',
    port INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535),
    path TEXT NOT NULL,
    upstreams_json TEXT NOT NULL DEFAULT '[]',
    PRIMARY KEY (observation_id, workload_key, route_key),
    FOREIGN KEY (observation_id, workload_key)
        REFERENCES workloads(observation_id, workload_key) ON DELETE CASCADE
) STRICT;
`,
	},
	{
		version: 5,
		name:    "web_link_checks",
		sql: `
CREATE TABLE web_link_checks (
    host_id TEXT NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    workload_key TEXT NOT NULL,
    url TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('reachable', 'degraded', 'unreachable')),
    http_status INTEGER NOT NULL DEFAULT 0 CHECK (http_status BETWEEN 0 AND 599),
    latency_ms INTEGER NOT NULL CHECK (latency_ms >= 0),
    error_kind TEXT NOT NULL DEFAULT '',
    checked_at TEXT NOT NULL,
    PRIMARY KEY (host_id, workload_key, url)
) STRICT;

CREATE INDEX web_link_checks_state_time_idx
    ON web_link_checks(state, checked_at DESC);
`,
	},
	{
		version: 6,
		name:    "event_notification_outbox",
		sql: `
CREATE TABLE event_notification_outbox (
    id INTEGER PRIMARY KEY,
    event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    transition TEXT NOT NULL CHECK (transition IN ('opened', 'recovered')),
    channel TEXT NOT NULL,
    dedupe_key TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('pending', 'sending', 'delivered', 'cancelled', 'failed')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    not_before TEXT NOT NULL,
    next_attempt_at TEXT NOT NULL,
    last_error_kind TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    delivered_at TEXT,
    UNIQUE(event_id, transition, channel)
) STRICT;

CREATE INDEX event_notification_due_idx
    ON event_notification_outbox(channel, state, next_attempt_at, id);
CREATE INDEX event_notification_cooldown_idx
    ON event_notification_outbox(channel, dedupe_key, transition, delivered_at DESC);
`,
	},
	{
		version: 7,
		name:    "ssh_auth_failure_summary",
		sql: `
ALTER TABLE observations ADD COLUMN ssh_auth_json TEXT;
`,
	},
	{
		version: 8,
		name:    "web_route_kind",
		sql: `
ALTER TABLE proxy_routes ADD COLUMN route_kind TEXT NOT NULL DEFAULT 'unknown'
    CHECK (route_kind IN ('unknown', 'proxy', 'static', 'site'));
UPDATE proxy_routes SET route_kind = 'proxy' WHERE upstreams_json != '[]';
`,
	},
}
