package review

import (
	"sort"
	"strings"
	"time"

	"github.com/JtheGunner/ticktack/internal/collector/git"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
)

// commitRefs returns, per ticket, the commits that belong to that ticket's
// work item, within [from, to), oldest first. A commit event carries the
// repository and, in its key, the commit hash; nothing else about the commit
// is stored.
//
// A commit belongs to a ticket only when both hold: it was made while a block
// of that ticket was running, and the commit's own ticket (taken from its
// branch when it was scanned) is that same ticket. Time alone is not enough:
// time pinned, folded or reassigned to a ticket says nothing about the
// repository the user committed in, and its subject must not end up in the
// work item of another project. A commit on a branch without a ticket is left
// out as well, even when a rule maps its repository to the block's ticket:
// the conservative reading keeps private repositories out of work item text.
// Commits made outside any block (idle time, discarded time) belong to no
// ticket.
func commitRefs(events []event.Event, blocks []sessionizer.Block, from, to time.Time) map[string][]sessionizer.CommitRef {
	out := map[string][]sessionizer.CommitRef{}
	seen := map[string]bool{}
	for _, e := range events {
		if e.Source != git.Source || e.At.Before(from) || !e.At.Before(to) {
			continue
		}
		hash, ok := git.CommitHash(e)
		if !ok {
			continue
		}
		ticket, ok := ticketAt(blocks, e.At)
		if !ok || normalizeTicket(e.Ticket) != ticket {
			continue
		}
		id := ticket + "\x00" + e.Repo + "\x00" + hash
		if seen[id] {
			continue
		}
		seen[id] = true
		out[ticket] = append(out[ticket], sessionizer.CommitRef{Repo: e.Repo, Hash: hash, At: e.At})
	}
	for _, refs := range out {
		sort.SliceStable(refs, func(i, j int) bool { return refs[i].At.Before(refs[j].At) })
	}
	return out
}

// ticketAt returns the ticket of the block that contains at. Unassigned
// blocks yield no ticket.
func ticketAt(blocks []sessionizer.Block, at time.Time) (string, bool) {
	for _, b := range blocks {
		if b.Ticket == "" || at.Before(b.Start) || !at.Before(b.End) {
			continue
		}
		return normalizeTicket(b.Ticket), true
	}
	return "", false
}

// normalizeTicket returns the ticket in the form ticketid.Extract produces:
// trimmed and upper-cased.
func normalizeTicket(ticket string) string {
	return strings.ToUpper(strings.TrimSpace(ticket))
}
