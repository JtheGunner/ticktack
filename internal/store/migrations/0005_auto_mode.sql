-- review_log records what each review session did to a day: how many
-- corrections (reassign, discard, break, split, remembered rule, suggestion
-- pick) and how many items the session itself confirmed. One row per session
-- (its own id), updated while the session runs so an aborted session still
-- leaves its record; sessions of the same day never overwrite each other. It
-- is the track record that unlocks auto mode and never touches events.
CREATE TABLE review_log (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    day         TEXT    NOT NULL,
    started_at  INTEGER NOT NULL,
    finished_at INTEGER NOT NULL,
    corrections INTEGER NOT NULL,
    confirmed   INTEGER NOT NULL
);

-- digests keeps the text of every auto run so it can be shown again.
CREATE TABLE digests (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    day        TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    text       TEXT    NOT NULL
);
