package auto

import (
	"reflect"
	"testing"

	"cloud.google.com/go/civil"

	"github.com/JtheGunner/ticktack/internal/sessionizer"
)

func TestMoveUncertain(t *testing.T) {
	const neutral = "STD-1"
	nextDay := civil.Date{Year: 2026, Month: 1, Day: 6}
	item := func(d civil.Date, ticket, typ string, minutes int, c sessionizer.Certainty) sessionizer.DayItem {
		return sessionizer.DayItem{
			Date: d, Ticket: ticket, Type: typ, Minutes: minutes, RawMinutes: minutes,
			Description: ticket + " — blocks", Certainty: c,
		}
	}
	neutralItem := func(d civil.Date, typ string, minutes int, desc string) sessionizer.DayItem {
		return sessionizer.DayItem{
			Date: d, Ticket: neutral, Type: typ, Minutes: minutes, RawMinutes: minutes,
			Description: desc, Certainty: sessionizer.Certainty{Rule: minutes},
		}
	}
	const dev = "Development"
	withRaw := func(it sessionizer.DayItem, raw int) sessionizer.DayItem {
		it.RawMinutes = raw
		return it
	}

	tests := []struct {
		name     string
		in       []sessionizer.DayItem
		want     []sessionizer.DayItem
		wantFrom []Origin
	}{
		{
			name: "mixed item is split with rounding",
			in: []sessionizer.DayItem{{
				Date: day, Ticket: "A-1", Type: dev, Minutes: 45, RawMinutes: 43,
				Description: "A-1 — 3 blocks", Commits: []sessionizer.CommitRef{{Repo: "/r", Hash: "abc"}},
				Certainty: sessionizer.Certainty{Explicit: 10, Rule: 5, Learned: 10, Suggested: 15, Unknown: 5, Carried: 5},
			}},
			want: []sessionizer.DayItem{
				{
					Date: day, Ticket: "A-1", Type: dev, Minutes: 15, RawMinutes: 14,
					Description: "A-1 — 3 blocks", Commits: []sessionizer.CommitRef{{Repo: "/r", Hash: "abc"}},
					Certainty: sessionizer.Certainty{Explicit: 10, Rule: 5},
				},
				withRaw(neutralItem(day, dev, 30, neutral+" — uncertain time"), 29),
			},
			wantFrom: []Origin{{Ticket: "A-1", Minutes: 30}},
		},
		{
			name:     "only uncertain item is dropped",
			in:       []sessionizer.DayItem{item(day, "A-1", dev, 20, sessionizer.Certainty{Suggested: 20})},
			want:     []sessionizer.DayItem{neutralItem(day, dev, 20, neutral+" — uncertain time")},
			wantFrom: []Origin{{Ticket: "A-1", Minutes: 20}},
		},
		{
			name:     "unassigned item moves",
			in:       []sessionizer.DayItem{item(day, "", dev, 15, sessionizer.Certainty{Unknown: 15})},
			want:     []sessionizer.DayItem{neutralItem(day, dev, 15, neutral+" — uncertain time")},
			wantFrom: []Origin{{Ticket: "", Minutes: 15}},
		},
		{
			name: "learned time moves",
			in:   []sessionizer.DayItem{item(day, "A-1", dev, 30, sessionizer.Certainty{Rule: 15, Learned: 15})},
			want: []sessionizer.DayItem{
				{
					Date: day, Ticket: "A-1", Type: dev, Minutes: 15, RawMinutes: 15,
					Description: "A-1 — blocks", Certainty: sessionizer.Certainty{Rule: 15},
				},
				neutralItem(day, dev, 15, neutral+" — uncertain time"),
			},
			wantFrom: []Origin{{Ticket: "A-1", Minutes: 15}},
		},
		{
			name: "already neutral item merges and keeps its description",
			in: []sessionizer.DayItem{
				neutralItem(day, dev, 30, "STD-1 — 2 blocks"),
				item(day, "A-1", dev, 15, sessionizer.Certainty{Suggested: 15}),
			},
			want:     []sessionizer.DayItem{neutralItem(day, dev, 45, "STD-1 — 2 blocks")},
			wantFrom: []Origin{{Ticket: "A-1", Minutes: 15}},
		},
		{
			name: "neutral item after a contributor takes the position of the first contributor",
			in: []sessionizer.DayItem{
				item(day, "A-1", dev, 15, sessionizer.Certainty{Suggested: 15}),
				item(day, "B-1", dev, 30, sessionizer.Certainty{Rule: 30}),
				neutralItem(day, dev, 30, "STD-1 — 2 blocks"),
			},
			want: []sessionizer.DayItem{
				neutralItem(day, dev, 45, "STD-1 — 2 blocks"),
				item(day, "B-1", dev, 30, sessionizer.Certainty{Rule: 30}),
			},
			wantFrom: []Origin{{Ticket: "A-1", Minutes: 15}},
		},
		{
			name: "types stay separate",
			in: []sessionizer.DayItem{
				item(day, "A-1", "Development", 15, sessionizer.Certainty{Learned: 15}),
				item(day, "B-1", "Review", 20, sessionizer.Certainty{Unknown: 20}),
			},
			want: []sessionizer.DayItem{
				neutralItem(day, "Development", 15, neutral+" — uncertain time"),
				neutralItem(day, "Review", 20, neutral+" — uncertain time"),
			},
			wantFrom: []Origin{{Ticket: "A-1", Minutes: 15}, {Ticket: "B-1", Minutes: 20}},
		},
		{
			name: "origins merge per ticket",
			in: []sessionizer.DayItem{
				item(day, "A-1", "Development", 15, sessionizer.Certainty{Learned: 15}),
				item(day, "A-1", "Review", 10, sessionizer.Certainty{Learned: 10}),
				item(day, "", dev, 5, sessionizer.Certainty{Unknown: 5}),
				item(day, "", "Review", 5, sessionizer.Certainty{Unknown: 5}),
			},
			want: []sessionizer.DayItem{
				neutralItem(day, "Development", 20, neutral+" — uncertain time"),
				neutralItem(day, "Review", 15, neutral+" — uncertain time"),
			},
			wantFrom: []Origin{{Ticket: "A-1", Minutes: 25}, {Ticket: "", Minutes: 10}},
		},
		{
			name: "nothing uncertain returns the items unchanged",
			in: []sessionizer.DayItem{
				item(day, "A-1", dev, 30, sessionizer.Certainty{Explicit: 10, Rule: 20}),
				neutralItem(day, dev, 15, "STD-1 — 1 block"),
			},
			want: []sessionizer.DayItem{
				item(day, "A-1", dev, 30, sessionizer.Certainty{Explicit: 10, Rule: 20}),
				neutralItem(day, dev, 15, "STD-1 — 1 block"),
			},
		},
		{
			name: "several days keep separate neutral items",
			in: []sessionizer.DayItem{
				item(day, "A-1", dev, 15, sessionizer.Certainty{Suggested: 15}),
				item(nextDay, "A-1", dev, 25, sessionizer.Certainty{Unknown: 25}),
			},
			want: []sessionizer.DayItem{
				neutralItem(day, dev, 15, neutral+" — uncertain time"),
				neutralItem(nextDay, dev, 25, neutral+" — uncertain time"),
			},
			wantFrom: []Origin{{Ticket: "A-1", Minutes: 40}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := append([]sessionizer.DayItem(nil), tc.in...)
			got, from := MoveUncertain(tc.in, neutral)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("items:\n got  %+v\n want %+v", got, tc.want)
			}
			if len(from) != len(tc.wantFrom) || (len(from) > 0 && !reflect.DeepEqual(from, tc.wantFrom)) {
				t.Errorf("from: got %+v, want %+v", from, tc.wantFrom)
			}
			if !reflect.DeepEqual(tc.in, in) {
				t.Errorf("input was modified")
			}
			sum := func(items []sessionizer.DayItem) (n int) {
				for _, it := range items {
					n += it.Minutes
				}
				return n
			}
			if sum(got) != sum(tc.in) {
				t.Errorf("minutes not preserved: %d != %d", sum(got), sum(tc.in))
			}
			for _, it := range got {
				if it.Certainty.Total() != it.Minutes {
					t.Errorf("%s: certainty %d != minutes %d", it.Ticket, it.Certainty.Total(), it.Minutes)
				}
			}
		})
	}
}
