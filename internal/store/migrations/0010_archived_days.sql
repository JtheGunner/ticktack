-- Retention: a day whose raw events were deleted after it was fully booked is
-- archived. It is frozen: it is no longer derived, reviewed or synced, so the
-- bookings of its ledger rows are never reconciled against a day without
-- events. start_at and end_at are the UTC nanosecond bounds of the local day
-- [start_at, end_at), so new events of an archived day can be refused.
CREATE TABLE archived_days (
    day            TEXT    PRIMARY KEY,
    start_at       INTEGER NOT NULL,
    end_at         INTEGER NOT NULL,
    archived_at    INTEGER NOT NULL,
    events_deleted INTEGER NOT NULL
);
