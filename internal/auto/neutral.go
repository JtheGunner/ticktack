package auto

import (
	"cloud.google.com/go/civil"

	"github.com/JtheGunner/ticktack/internal/resolver"
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

// SelectNeutral picks what the autopilot books for one day: all presence time.
// Time on the explicit and rule tiers stays on its ticket; every other minute
// is moved to the neutral ticket (see MoveUncertain). The day total and the
// cap are judged on the input items, so a capped day books nothing and skips
// the input items as Select does.
//
// An empty neutralTicket moves nothing: items with fully sure time are booked,
// time without a ticket is skipped as unassigned and any other item is skipped
// with ReasonStandardTask, because there is no standard task to book it on.
func SelectNeutral(items []sessionizer.DayItem, neutralTicket string, capMinutes int) Selection {
	sel := Selection{CapMinutes: capMinutes}
	for _, it := range items {
		sel.TotalMinutes += it.Minutes
	}
	sel.CapExceeded = capMinutes > 0 && sel.TotalMinutes > capMinutes
	if sel.CapExceeded {
		for _, it := range items {
			sel.Skipped = append(sel.Skipped, Entry{Item: it, Tier: sureTier(it), Reason: ReasonOverCap})
		}
		return sel
	}
	if neutralTicket == "" {
		for _, it := range items {
			e := Entry{Item: it, Tier: sureTier(it)}
			switch {
			case it.Ticket == "":
				e.Reason = ReasonUnassigned
			case it.Certainty.Sure() < it.Minutes:
				e.Reason = ReasonStandardTask
			}
			if e.Reason != "" {
				sel.Skipped = append(sel.Skipped, e)
				continue
			}
			sel.Book = append(sel.Book, e)
		}
		return sel
	}
	moved, from := MoveUncertain(items, neutralTicket)
	for _, it := range moved {
		e := Entry{Item: it, Tier: sureTier(it)}
		if it.Ticket == neutralTicket && len(from) > 0 {
			e.Tier, e.Neutral, e.From = resolver.TierRule, true, from
		}
		sel.Book = append(sel.Book, e)
	}
	return sel
}

// sureTier is the tier of a moved item: explicit when it holds no rule time.
func sureTier(it sessionizer.DayItem) resolver.Tier {
	if it.Certainty.Rule == 0 {
		return resolver.TierExplicit
	}
	return resolver.TierRule
}
