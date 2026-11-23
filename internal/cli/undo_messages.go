package cli

import "fmt"

// User-facing strings of the undo command.
const msgNothingToUndo = "Nothing to undo."

func msgUndoResult(deleted, restored, recreated, forgotten int) string {
	return fmt.Sprintf("Deleted %d, restored %d, re-created %d, forgotten %d.", deleted, restored, recreated, forgotten)
}

// Help texts and flag descriptions of the undo command.
const (
	msgUndoUndoShort = "Revert the last sync"
	msgUndoUndoLong  = `Revert the last booking run: items it created are deleted from YouTrack,
items it updated are restored to their previous minutes and items it deleted
are booked again. Running undo again reverts the run before it.` + msgBookingLockHelp
)
