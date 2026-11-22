package sessionizer

import (
	"strings"

	"cloud.google.com/go/civil"
)

// RetargetItem returns the item booked on ticket instead of its current one;
// the old ticket in the description is replaced too.
func RetargetItem(it DayItem, ticket string) DayItem {
	if it.Ticket != "" {
		it.Description = strings.Replace(it.Description, it.Ticket, ticket, 1)
	}
	it.Ticket = ticket
	return it
}

// MergeItems combines items that share a ticket, date and type, summing their
// minutes, in the order of their first occurrence. Booking is keyed by those
// three values, so two items with the same key would overwrite each other.
func MergeItems(items []DayItem) []DayItem {
	type key struct {
		ticket, typ string
		date        civil.Date
	}
	index := map[key]int{}
	out := make([]DayItem, 0, len(items))
	for _, it := range items {
		k := key{it.Ticket, it.Type, it.Date}
		i, seen := index[k]
		if !seen || it.Ticket == "" {
			index[k] = len(out)
			out = append(out, it)
			continue
		}
		out[i].Minutes += it.Minutes
		out[i].RawMinutes += it.RawMinutes
		out[i].Certainty = out[i].Certainty.Add(it.Certainty)
		out[i].OverCap = out[i].OverCap || it.OverCap
	}
	return out
}
