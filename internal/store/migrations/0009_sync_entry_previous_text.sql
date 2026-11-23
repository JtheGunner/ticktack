-- previous_text is the booked text a work item had before an update, so undo
-- can restore the text together with the minutes. Empty for entries of earlier
-- versions and for actions that replace nothing.
ALTER TABLE sync_entries ADD COLUMN previous_text TEXT NOT NULL DEFAULT '';
