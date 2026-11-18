-- rule_changes records every manual change to the learned rules made with
-- `ticktack map add/edit/rm`: when it happened and which action it was, never
-- the rule itself. The trust gate of auto mode counts each row as one
-- correction on the calendar day of the change. Rows are only ever appended.
CREATE TABLE rule_changes (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    changed_at INTEGER NOT NULL,
    action     TEXT    NOT NULL CHECK (action IN ('add', 'edit', 'rm'))
);
