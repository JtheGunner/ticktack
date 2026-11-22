package ledger

import (
	"github.com/JtheGunner/ticktack/internal/resolver"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
)

// ItemKey returns the ledger key an item is booked under. Time on a standard
// task is booked on the real issue, so its placeholder is replaced by the
// known task key of the project. ok is false for an item without a ticket and
// for a placeholder whose task is not known.
func ItemKey(it sessionizer.DayItem, defaultTasks map[string]string) (key string, ok bool) {
	ticket := it.Ticket
	if project, isPlaceholder := resolver.ParseDefaultRef(ticket); isPlaceholder {
		ticket = defaultTasks[project]
	}
	if ticket == "" {
		return "", false
	}
	typ := it.Type
	if typ == "" {
		typ = sessionizer.DefaultWorkItemType
	}
	return Key(ticket, it.Date.String(), typ), true
}
