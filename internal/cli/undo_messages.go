package cli

import "fmt"

// User-facing strings of the undo command.
const msgNothingToUndo = "Nothing to undo."

func msgUndoResult(deleted, restored, recreated, forgotten int) string {
	return fmt.Sprintf("Deleted %d, restored %d, re-created %d, forgotten %d.", deleted, restored, recreated, forgotten)
}
