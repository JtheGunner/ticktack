CREATE TABLE sync_runs (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    started_at INTEGER NOT NULL
);

CREATE TABLE sync_entries (
    run_id           INTEGER NOT NULL REFERENCES sync_runs (id),
    issue            TEXT    NOT NULL,
    date             TEXT    NOT NULL,
    type             TEXT    NOT NULL,
    action           TEXT    NOT NULL,
    work_item_id     TEXT    NOT NULL,
    previous_minutes INTEGER NOT NULL DEFAULT 0,
    new_minutes      INTEGER NOT NULL,
    text             TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (run_id, issue, date, type)
);
