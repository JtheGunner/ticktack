-- A confirmation says: the user reviewed this item with exactly these minutes.
-- It is only honoured while the derived minutes still match.
CREATE TABLE confirmations (
    day          TEXT    NOT NULL,
    issue        TEXT    NOT NULL,
    type         TEXT    NOT NULL,
    minutes      INTEGER NOT NULL,
    confirmed_at INTEGER NOT NULL,
    PRIMARY KEY (day, issue, type)
);

-- An acknowledgement says: the user accepted a day total above the daily cap.
-- It is only honoured while the derived total still matches.
CREATE TABLE cap_acknowledgements (
    day             TEXT    PRIMARY KEY,
    total_minutes   INTEGER NOT NULL,
    acknowledged_at INTEGER NOT NULL
);
