package review

import (
	"context"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/store"
)

// Two neighbouring entries assigned to the same ticket merge into one. A
// correction on the merged entry is created last and must cover all of it,
// although the override of the second half starts later.
func TestReviewCorrectionOnMergedEntryCoversAllOfIt(t *testing.T) {
	tests := []struct {
		name        string
		act         func(*Session, context.Context) error
		wantTicket  string
		wantMinutes int
	}{
		{
			name:        "reassign",
			act:         func(s *Session, ctx context.Context) error { return s.Reassign(ctx, 1, "PRJ-7") },
			wantTicket:  "PRJ-7",
			wantMinutes: 60,
		},
		{
			name: "discard",
			act:  func(s *Session, ctx context.Context) error { return s.Discard(ctx, 1) },
		},
		{
			name: "reassign then discard",
			act: func(s *Session, ctx context.Context) error {
				if err := s.Reassign(ctx, 1, "PRJ-7"); err != nil {
					return err
				}
				return s.Discard(ctx, 1)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := openStore(t)
			seed(t, st, dayStart, 30, "ABC-1")
			seed(t, st, dayStart.Add(30*time.Minute), 30, "ABC-2")
			ctx := context.Background()
			s := newSession(t, st, testConfig())
			if e := s.Entries(); len(e) != 2 {
				t.Fatalf("seeded entries = %+v", e)
			}
			// Second half first, so creation order differs from start order.
			if err := s.Reassign(ctx, 2, "PRJ-5"); err != nil {
				t.Fatal(err)
			}
			if err := s.Reassign(ctx, 1, "PRJ-5"); err != nil {
				t.Fatal(err)
			}
			if e := s.Entries(); len(e) != 1 || e[0].Ticket != "PRJ-5" {
				t.Fatalf("entries did not merge: %+v", e)
			}
			if err := tt.act(s, ctx); err != nil {
				t.Fatal(err)
			}
			assertDayTickets(t, st, tt.wantTicket, tt.wantMinutes)
		})
	}
}

// assertDayTickets checks that the re-derived day holds exactly wantMinutes on
// wantTicket and nothing else; an empty wantTicket expects an empty day.
func assertDayTickets(t *testing.T, st *store.Store, wantTicket string, wantMinutes int) {
	t.Helper()
	day, err := LoadDay(context.Background(), st, testConfig(), testDay)
	if err != nil {
		t.Fatal(err)
	}
	if wantTicket == "" {
		if len(day.Blocks) != 0 || len(day.Items) != 0 {
			t.Fatalf("day still holds time: blocks=%+v items=%+v", day.Blocks, day.Items)
		}
		return
	}
	var minutes int
	for _, b := range day.Blocks {
		if b.Ticket != wantTicket {
			t.Fatalf("block on %q, want only %q: %+v", b.Ticket, wantTicket, day.Blocks)
		}
		minutes += int(b.Duration() / time.Minute)
	}
	if minutes != wantMinutes {
		t.Fatalf("%s = %d min, want %d: %+v", wantTicket, minutes, wantMinutes, day.Blocks)
	}
}
