-- Autopilot: a day whose booking the user undid is held. The autopilot skips a
-- held day until the user books it again by hand (ticktack auto --day), so an
-- undo stays undone. It touches no other table.
CREATE TABLE autopilot_holds (
    day     TEXT    PRIMARY KEY,
    held_at INTEGER NOT NULL
);
