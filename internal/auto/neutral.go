package auto

import (
	"cloud.google.com/go/civil"

	"github.com/JtheGunner/ticktack/internal/sessionizer"
)

// Origin says where minutes moved to the neutral item came from.
type Origin struct {
	Ticket  string // "" = time without a ticket
	Minutes int
}

// neutralKey identifies the neutral item of one day and work item type.
type neutralKey struct {
	date civil.Date
	typ  string
}

// MoveUncertain returns items of one day with all uncertain time moved to the
// neutral ticket, plus the origin of every moved minute.
//
// Time on the explicit and rule tiers stays on its ticket. Everything else
// (learned, suggested, unknown, and time without a ticket) is added to the
// neutral item of the same date and type, so there are never two items with
// the same booking key. The input is not modified.
func MoveUncertain(items []sessionizer.DayItem, neutralTicket string) (moved []sessionizer.DayItem, from []Origin) {
	out := make([]sessionizer.DayItem, 0, len(items))
	index := map[neutralKey]int{}
	// movedOnly marks neutral items that hold nothing but moved time; only
	// those get the generated description.
	movedOnly := map[int]bool{}
	fromIndex := map[string]int{}

	moveTo := func(src sessionizer.DayItem, minutes, raw int) {
		if minutes <= 0 {
			return
		}
		k := neutralKey{src.Date, src.Type}
		i, seen := index[k]
		if !seen {
			index[k] = len(out)
			movedOnly[len(out)] = true
			out = append(out, sessionizer.DayItem{
				Date:        src.Date,
				Ticket:      neutralTicket,
				Type:        src.Type,
				Description: neutralTicket + " — uncertain time",
			})
			i = len(out) - 1
		}
		n := &out[i]
		n.Minutes += minutes
		n.RawMinutes += raw
		n.Certainty.Rule += minutes
		n.OverCap = n.OverCap || src.OverCap

		o, seen := fromIndex[src.Ticket]
		if !seen {
			fromIndex[src.Ticket] = len(from)
			from = append(from, Origin{Ticket: src.Ticket})
			o = len(from) - 1
		}
		from[o].Minutes += minutes
	}

	for _, it := range items {
		switch {
		case neutralTicket != "" && it.Ticket == neutralTicket:
			k := neutralKey{it.Date, it.Type}
			i, seen := index[k]
			if !seen {
				index[k] = len(out)
				out = append(out, it)
				continue
			}
			n := &out[i]
			if movedOnly[i] {
				n.Description = it.Description
				delete(movedOnly, i)
			}
			n.Minutes += it.Minutes
			n.RawMinutes += it.RawMinutes
			n.Certainty = n.Certainty.Add(it.Certainty)
			n.OverCap = n.OverCap || it.OverCap
			n.Commits = append(append([]sessionizer.CommitRef(nil), n.Commits...), it.Commits...)
		case it.Ticket == "":
			moveTo(it, it.Minutes, it.RawMinutes)
		default:
			sure := it.Certainty.Sure()
			rest := it.Minutes - sure
			if rest <= 0 {
				out = append(out, it)
				continue
			}
			if sure <= 0 {
				moveTo(it, it.Minutes, it.RawMinutes)
				continue
			}
			kept := it
			kept.Minutes = sure
			kept.RawMinutes = scaleRound(it.RawMinutes, sure, it.Minutes)
			kept.Certainty = sessionizer.Certainty{Explicit: it.Certainty.Explicit, Rule: it.Certainty.Rule}
			out = append(out, kept)
			moveTo(it, rest, it.RawMinutes-kept.RawMinutes)
		}
	}
	if len(from) == 0 {
		return items, nil
	}
	return out, from
}

// scaleRound returns value*num/den rounded to the nearest whole number.
func scaleRound(value, num, den int) int {
	return (2*value*num + den) / (2 * den)
}
