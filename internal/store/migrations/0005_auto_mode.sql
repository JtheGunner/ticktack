-- review_log records what each review session did to a day: how many
-- corrections (reassign, discard, break, split, remembered rule, suggestion
-- pick) and how many confirmed items. One row per session, updated while the
-- session runs so an aborted session still leaves its record. It is the track
-- record that unlocks auto mode and never touches events.
CREATE TABLE review_log (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    day         TEXT    NOT NULL,
    session_key INTEGER NOT NULL,
    finished_at INTEGER NOT NULL,
    corrections INTEGER NOT NULL,
    confirmed   INTEGER NOT NULL,
    UNIQUE (day, session_key)
);

-- digests keeps the text of every auto run so it can be shown again.
CREATE TABLE digests (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    day        TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    text       TEXT    NOT NULL
);
