-- overrides gain a kind so a break is stored apart from a discard and idle
-- time can be filled with a ticket. Existing rows keep their meaning: rows
-- that dropped time become 'discard', every other row stays 'reassign'.
ALTER TABLE overrides ADD COLUMN kind TEXT NOT NULL DEFAULT 'reassign'
    CHECK (kind IN ('reassign', 'discard', 'break', 'fill'));

UPDATE overrides SET kind = 'discard' WHERE discard <> 0;
