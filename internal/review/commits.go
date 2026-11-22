package review

import (
	"sort"
	"strings"
	"time"

	"github.com/JtheGunner/ticktack/internal/collector/git"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
)

// commitRefs returns, per ticket, the commits that were made while a block of
// that ticket was running, within [from, to), oldest first. A commit event
// carries the repository and, in its key, the commit hash; nothing else about
// the commit is stored. Commits made outside any block (idle time, time that
// was discarded) belong to no ticket.
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
		if !ok {
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
		return strings.TrimSpace(b.Ticket), true
	}
	return "", false
}
