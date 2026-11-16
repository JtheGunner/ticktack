-- A learned rule says: time in this repository (path prefix) on this branch
-- belongs to this ticket. It is visible, editable data written from review
-- corrections; it never changes events. An empty branch_pattern matches every
-- branch of the repository, an empty repo every repository, but never both.
CREATE TABLE learned_rules (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    repo           TEXT    NOT NULL,
    branch_pattern TEXT    NOT NULL,
    ticket         TEXT    NOT NULL,
    created_at     INTEGER NOT NULL
);
