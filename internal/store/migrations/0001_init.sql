CREATE TABLE events (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    at           INTEGER NOT NULL,
    source       TEXT    NOT NULL,
    kind         TEXT    NOT NULL,
    repo         TEXT    NOT NULL DEFAULT '',
    branch       TEXT    NOT NULL DEFAULT '',
    ticket       TEXT    NOT NULL DEFAULT '',
    app          TEXT    NOT NULL DEFAULT '',
    idle_seconds INTEGER NOT NULL DEFAULT 0,
    dedup_key    TEXT
);

CREATE INDEX idx_events_at ON events (at);

CREATE UNIQUE INDEX idx_events_dedup_key ON events (dedup_key) WHERE dedup_key IS NOT NULL;

CREATE TABLE overrides (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    start   INTEGER NOT NULL,
    "end"   INTEGER NOT NULL,
    ticket  TEXT    NOT NULL DEFAULT '',
    discard INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX idx_overrides_start ON overrides (start);

CREATE TABLE ledger (
    issue        TEXT    NOT NULL,
    date         TEXT    NOT NULL,
    type         TEXT    NOT NULL,
    work_item_id TEXT    NOT NULL,
    minutes      INTEGER NOT NULL,
    content_hash TEXT    NOT NULL,
    booked_at    INTEGER NOT NULL,
    PRIMARY KEY (issue, date, type)
);
