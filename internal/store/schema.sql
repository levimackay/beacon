-- Beacon store schema. Applied with CREATE TABLE IF NOT EXISTS so Open is
-- idempotent against an already-initialized database file.

CREATE TABLE IF NOT EXISTS targets (
    id               TEXT PRIMARY KEY,
    kind             TEXT NOT NULL,
    name             TEXT NOT NULL,
    address          TEXT NOT NULL,
    interval_seconds INTEGER NOT NULL,
    expect_status    INTEGER NOT NULL DEFAULT 0,
    enabled          INTEGER NOT NULL DEFAULT 1,
    allow_private    INTEGER NOT NULL DEFAULT 0,
    contains_text    TEXT NOT NULL DEFAULT '',
    warn_after_ms    INTEGER NOT NULL DEFAULT 0
);

-- One row per (target, metric) reading. bucket is 0 for raw samples, 300 for
-- five-minute rollup means, 3600 for hourly rollup means. A sample with no
-- metrics is still stored, as a single row with metric = '' (the sentinel),
-- so state/latency/error survive even when there is nothing to graph.
CREATE TABLE IF NOT EXISTS samples (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    target_id   TEXT NOT NULL,
    at          INTEGER NOT NULL,
    bucket      INTEGER NOT NULL DEFAULT 0,
    state       TEXT NOT NULL,
    latency_ms  REAL NOT NULL DEFAULT 0,
    metric      TEXT NOT NULL DEFAULT '',
    value       REAL NOT NULL DEFAULT 0,
    error       TEXT NOT NULL DEFAULT '',
    cert_expiry INTEGER
);

CREATE INDEX IF NOT EXISTS idx_samples_target_metric_bucket_at
    ON samples (target_id, metric, bucket, at);

CREATE TABLE IF NOT EXISTS incidents (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    target_id   TEXT NOT NULL,
    target_name TEXT NOT NULL,
    state       TEXT NOT NULL,
    started_at  INTEGER NOT NULL,
    resolved_at INTEGER,
    summary     TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_incidents_target_started
    ON incidents (target_id, started_at);

CREATE INDEX IF NOT EXISTS idx_incidents_open
    ON incidents (target_id) WHERE resolved_at IS NULL;

CREATE TABLE IF NOT EXISTS audit (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    at        INTEGER NOT NULL,
    principal TEXT NOT NULL,
    action    TEXT NOT NULL,
    target    TEXT NOT NULL,
    result    TEXT NOT NULL
);
