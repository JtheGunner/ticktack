package auto

import (
	"reflect"
	"testing"

	"github.com/JtheGunner/ticktack/internal/ledger"
	"github.com/JtheGunner/ticktack/internal/resolver"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
)

func certainItem(ticket string, minutes int, c sessionizer.Certainty) sessionizer.DayItem {
	it := item(ticket, minutes)
	it.RawMinutes = minutes
	it.Certainty = c
	return it
}

func TestSelectNeutralSureOnlyDayMatchesSelect(t *testing.T) {
	items := []sessionizer.DayItem{
		certainItem("A-1", 60, sessionizer.Certainty{Explicit: 60}),
		certainItem("B-2", 30, sessionizer.Certainty{Rule: 30}),
	}
	tiers := LowestTiers([]sessionizer.Block{block("A-1", resolver.TierExplicit), block("B-2", resolver.TierRule)})

	got := SelectNeutral(items, "STD-1", 0)
	want := Select(items, tiers, 0)

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SelectNeutral = %+v, want %+v", got, want)
	}
}

func TestSelectNeutral(t *testing.T) {
	const neutral = "STD-1"
	mixed := []sessionizer.DayItem{
		certainItem("A-1", 60, sessionizer.Certainty{Explicit: 40, Learned: 20}),
		certainItem("B-2", 30, sessionizer.Certainty{Rule: 30}),
		certainItem("", 15, sessionizer.Certainty{Unknown: 15}),
	}
	tests := []struct {
		name         string
		items        []sessionizer.DayItem
		capMinutes   int
		wantBook     map[string]resolver.Tier
		wantSkipped  int
		wantFrom     []Origin
		wantCapped   bool
		wantNeutral  int
		wantSkipText string
	}{
		{
			name: "mixed day books sure time and neutral item", items: mixed,
			wantBook:    map[string]resolver.Tier{"A-1": resolver.TierExplicit, "B-2": resolver.TierRule, neutral: resolver.TierRule},
			wantFrom:    []Origin{{Ticket: "A-1", Minutes: 20}, {Ticket: "", Minutes: 15}},
			wantNeutral: 35,
		},
		{
			name: "cap exceeded books nothing and skips the input items", items: mixed, capMinutes: 100,
			wantBook: map[string]resolver.Tier{}, wantSkipped: 3, wantCapped: true, wantSkipText: ReasonOverCap,
		},
		{
			name: "day within cap books", items: mixed, capMinutes: 105,
			wantBook:    map[string]resolver.Tier{"A-1": resolver.TierExplicit, "B-2": resolver.TierRule, neutral: resolver.TierRule},
			wantFrom:    []Origin{{Ticket: "A-1", Minutes: 20}, {Ticket: "", Minutes: 15}},
			wantNeutral: 35,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SelectNeutral(tc.items, neutral, tc.capMinutes)

			if got.CapExceeded != tc.wantCapped || got.TotalMinutes != 105 {
				t.Fatalf("CapExceeded=%v Total=%d", got.CapExceeded, got.TotalMinutes)
			}
			if len(got.Skipped) != tc.wantSkipped {
				t.Fatalf("Skipped = %+v, want %d", got.Skipped, tc.wantSkipped)
			}
			for _, s := range got.Skipped {
				if s.Reason != tc.wantSkipText || s.Neutral {
					t.Errorf("skipped %+v, want reason %q and no neutral flag", s, tc.wantSkipText)
				}
			}
			if len(got.Book) != len(tc.wantBook) {
				t.Fatalf("Book = %+v, want %d entries", got.Book, len(tc.wantBook))
			}
			for _, e := range got.Book {
				tier, ok := tc.wantBook[e.Item.Ticket]
				if !ok || e.Tier != tier {
					t.Errorf("entry %s tier %q, want %q (listed %v)", e.Item.Ticket, e.Tier, tier, ok)
				}
				isNeutral := e.Item.Ticket == neutral
				if e.Neutral != isNeutral {
					t.Errorf("entry %s Neutral = %v", e.Item.Ticket, e.Neutral)
				}
				if isNeutral {
					if e.Item.Minutes != tc.wantNeutral || !reflect.DeepEqual(e.From, tc.wantFrom) {
						t.Errorf("neutral entry = %d min from %+v, want %d from %+v", e.Item.Minutes, e.From, tc.wantNeutral, tc.wantFrom)
					}
				} else if e.From != nil {
					t.Errorf("entry %s has From %+v", e.Item.Ticket, e.From)
				}
			}
		})
	}
}

func TestSelectNeutralWithoutNeutralTicketMovesNothing(t *testing.T) {
	items := []sessionizer.DayItem{
		certainItem("A-1", 60, sessionizer.Certainty{Rule: 60}),
		certainItem("B-2", 30, sessionizer.Certainty{Rule: 10, Learned: 20}),
		certainItem("", 15, sessionizer.Certainty{Unknown: 15}),
	}

	got := SelectNeutral(items, "", 0)

	if len(got.Book) != 1 || got.Book[0].Item.Ticket != "A-1" || got.Book[0].Neutral {
		t.Fatalf("Book = %+v, want only A-1", got.Book)
	}
	if len(got.Skipped) != 2 || got.Skipped[0].Reason != ReasonStandardTask || got.Skipped[1].Reason != ReasonUnassigned {
		t.Fatalf("Skipped = %+v", got.Skipped)
	}
}

func TestSelectNeutralEntriesSurviveResolveAndMerge(t *testing.T) {
	const neutral = "STD-1"
	from := []Origin{{Ticket: "A-1", Minutes: 20}}
	neutralEntry := Entry{
		Item: sessionizer.DayItem{Date: day, Ticket: neutral, Type: "Development", Minutes: 20}, Tier: resolver.TierRule,
		Neutral: true, From: from,
	}
	other := Entry{Item: sessionizer.DayItem{Date: day, Ticket: "A:default", Type: "Development", Minutes: 30}, Tier: resolver.TierRule}
	sel := Selection{Book: []Entry{neutralEntry, other}}

	t.Run("dropped neutral entry keeps its origins", func(t *testing.T) {
		got := sel.Resolve(nil, []string{neutral})
		if len(got.Skipped) != 1 || got.Skipped[0].Reason != ReasonStandardTask || !got.Skipped[0].Neutral || !reflect.DeepEqual(got.Skipped[0].From, from) {
			t.Fatalf("Skipped = %+v", got.Skipped)
		}
	})
	t.Run("rewritten neutral entry keeps its flags", func(t *testing.T) {
		got := sel.Resolve(map[string]string{neutral: "STD-9"}, nil)
		if got.Book[0].Item.Ticket != "STD-9" || !got.Book[0].Neutral || !reflect.DeepEqual(got.Book[0].From, from) {
			t.Fatalf("Book = %+v", got.Book)
		}
	})
	t.Run("merge ORs neutral and concatenates origins", func(t *testing.T) {
		second := Entry{
			Item: sessionizer.DayItem{Date: day, Ticket: "STD-9", Type: "Development", Minutes: 10}, Tier: resolver.TierRule,
			Neutral: true, From: []Origin{{Ticket: "", Minutes: 10}},
		}
		both := Selection{Book: []Entry{neutralEntry, second}}
		got := both.Resolve(map[string]string{neutral: "STD-9"}, nil)
		if len(got.Book) != 1 || !got.Book[0].Neutral || got.Book[0].Item.Minutes != 30 {
			t.Fatalf("Book = %+v", got.Book)
		}
		want := []Origin{{Ticket: "A-1", Minutes: 20}, {Ticket: "", Minutes: 10}}
		if !reflect.DeepEqual(got.Book[0].From, want) {
			t.Fatalf("From = %+v, want %+v", got.Book[0].From, want)
		}
	})
	t.Run("without keeps remaining entries intact", func(t *testing.T) {
		got := sel.Without([]string{"A:default"})
		if len(got.Book) != 1 || !got.Book[0].Neutral || !reflect.DeepEqual(got.Book[0].From, from) {
			t.Fatalf("Book = %+v", got.Book)
		}
	})
}

func TestBuildDigestCopiesNeutralOrigins(t *testing.T) {
	from := []Origin{{Ticket: "A-1", Minutes: 20}, {Ticket: "", Minutes: 15}}
	skipped := Entry{Item: sessionizer.DayItem{Date: day, Ticket: "STD-1", Type: "Development", Minutes: 35}, Tier: resolver.TierRule, Reason: ReasonStandardTask, Neutral: true, From: from}

	d := BuildDigest(day, Selection{Skipped: []Entry{skipped}}, ledger.Report{}, false)

	if len(d.Entries) != 1 || !d.Entries[0].Neutral || !reflect.DeepEqual(d.Entries[0].From, from) {
		t.Fatalf("Entries = %+v", d.Entries)
	}
}

func TestBuildDigestBookedNeutralEntryCarriesOrigins(t *testing.T) {
	from := []Origin{{Ticket: "A-1", Minutes: 20}}
	booked := Entry{Item: sessionizer.DayItem{Date: day, Ticket: "STD-1", Type: "Development", Minutes: 20}, Tier: resolver.TierRule, Neutral: true, From: from}
	report := ledger.Report{Created: 1, Items: []ledger.ItemResult{{Key: "STD-1:2026-01-05:Development", Action: ledger.ActionCreated, Minutes: 20}}}

	d := BuildDigest(day, Selection{Book: []Entry{booked}}, report, false)

	if len(d.Entries) != 1 || !d.Entries[0].Neutral || !reflect.DeepEqual(d.Entries[0].From, from) {
		t.Fatalf("Entries = %+v", d.Entries)
	}
}
