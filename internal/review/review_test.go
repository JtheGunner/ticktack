package review

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"cloud.google.com/go/civil"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/resolver"
	"github.com/JtheGunner/ticktack/internal/store"
)

var (
	testDay   = civil.Date{Year: 2026, Month: 1, Day: 5}
	dayStart  = time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	fixedNow  = time.Date(2026, 1, 5, 20, 0, 0, 0, time.UTC)
	testClock = func() time.Time { return fixedNow }
)

func testConfig() config.Config {
	return config.Config{
		Location:         time.UTC,
		GapThreshold:     5 * time.Minute,
		MinBlockDuration: 10 * time.Minute,
		Rounding:         15 * time.Minute,
		DailyCap:         10 * time.Hour,
		TicketPattern:    mustPattern(config.DefaultTicketPattern),
	}
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// seed records presence every minute from start for minutes+1 samples, with a
// context event at the start. An empty ticket seeds no context (unassigned).
func seed(t *testing.T, st *store.Store, start time.Time, minutes int, ticket string) {
	t.Helper()
	ctx := context.Background()
	if ticket != "" {
		e := event.Event{At: start, Source: "test", Kind: event.KindContext, Ticket: ticket}
		if err := st.Append(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i <= minutes; i++ {
		e := event.Event{At: start.Add(time.Duration(i) * time.Minute), Source: "test", Kind: event.KindPresence}
		if err := st.Append(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
}

func newSession(t *testing.T, st *store.Store, cfg config.Config) *Session {
	t.Helper()
	s, err := NewSession(context.Background(), st, cfg, testDay, testClock)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestReviewConfirmMarksItemForSync(t *testing.T) {
	st := openStore(t)
	seed(t, st, dayStart, 60, "ABC-1")
	ctx := context.Background()
	s := newSession(t, st, testConfig())
	if err := s.Confirm(ctx, 1); err != nil {
		t.Fatal(err)
	}
	items, err := ConfirmedItems(ctx, st, testConfig(), testDay)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Ticket != "ABC-1" || items[0].Minutes != 60 {
		t.Fatalf("items = %+v", items)
	}
	if !s.Entries()[0].Confirmed {
		t.Fatal("entry not shown as confirmed")
	}
}

func TestReviewReassignWritesOverride(t *testing.T) {
	st := openStore(t)
	seed(t, st, dayStart, 60, "ABC-1")
	ctx := context.Background()
	s := newSession(t, st, testConfig())
	if err := s.Reassign(ctx, 1, "xyz-9"); err != nil {
		t.Fatal(err)
	}
	ovs, err := st.Overrides(ctx, dayStart.Add(-time.Hour), dayStart.Add(24*time.Hour))
	if err != nil || len(ovs) != 1 || ovs[0].Ticket != "XYZ-9" || ovs[0].Discard {
		t.Fatalf("overrides = %+v err=%v", ovs, err)
	}
	if !ovs[0].Start.Equal(dayStart) {
		t.Fatalf("override start = %v", ovs[0].Start)
	}
	if e := s.Entries(); len(e) != 1 || e[0].Ticket != "XYZ-9" {
		t.Fatalf("entries not re-derived: %+v", e)
	}
	if err := s.Reassign(ctx, 1, "not a ticket"); !errors.Is(err, ErrInvalidTicket) {
		t.Fatalf("invalid ticket err = %v", err)
	}
	if err := s.Reassign(ctx, 7, "ABC-1"); !errors.Is(err, ErrNoSuchEntry) {
		t.Fatalf("bad index err = %v", err)
	}
}

func TestReviewDiscardExcludesFromSync(t *testing.T) {
	for name, act := range map[string]func(*Session, context.Context) error{
		"discard": func(s *Session, ctx context.Context) error { return s.Discard(ctx, 1) },
	} {
		t.Run(name, func(t *testing.T) {
			st := openStore(t)
			seed(t, st, dayStart, 60, "ABC-1")
			ctx := context.Background()
			s := newSession(t, st, testConfig())
			if err := s.Confirm(ctx, 1); err != nil {
				t.Fatal(err)
			}
			if err := act(s, ctx); err != nil {
				t.Fatal(err)
			}
			items, err := ConfirmedItems(ctx, st, testConfig(), testDay)
			if err != nil || len(items) != 0 {
				t.Fatalf("items = %+v err=%v", items, err)
			}
			if len(s.Entries()) != 0 || s.Summary().Discarded != 1 {
				t.Fatalf("entries=%v summary=%+v", s.Entries(), s.Summary())
			}
		})
	}
}

func TestReviewSplitAttributesSecondPart(t *testing.T) {
	st := openStore(t)
	seed(t, st, dayStart, 60, "ABC-1")
	ctx := context.Background()
	s := newSession(t, st, testConfig())
	if err := s.Split(ctx, 1, "09:30", "DEF-2"); err != nil {
		t.Fatal(err)
	}
	e := s.Entries()
	if len(e) != 2 || e[0].Ticket != "ABC-1" || e[1].Ticket != "DEF-2" || e[1].Minutes != 30 {
		t.Fatalf("entries = %+v", e)
	}
	if err := s.Split(ctx, 1, "09:00", "DEF-2"); !errors.Is(err, ErrInvalidSplit) {
		t.Fatalf("edge split err = %v", err)
	}
	if err := s.Split(ctx, 1, "9h", ""); err == nil {
		t.Fatal("expected parse error")
	}
	if err := s.Split(ctx, 1, "09:10", ""); err != nil {
		t.Fatal(err)
	}
	if len(s.Entries()) != 3 {
		t.Fatalf("entries = %+v", s.Entries())
	}
}

func TestReviewShowsUnassignedGaps(t *testing.T) {
	st := openStore(t)
	seed(t, st, dayStart, 60, "")
	ctx := context.Background()
	s := newSession(t, st, testConfig())
	e := s.Entries()
	if len(e) != 1 || e[0].Kind != KindGap || e[0].Tier != resolver.TierUnknown || e[0].Minutes != 60 {
		t.Fatalf("entries = %+v", e)
	}
	if err := s.Confirm(ctx, 1); !errors.Is(err, ErrGapNotAttached) {
		t.Fatalf("confirm gap err = %v", err)
	}
	if n, err := s.ConfirmAll(ctx); err != nil || n != 0 {
		t.Fatalf("ConfirmAll n=%d err=%v", n, err)
	}
	if sum := s.Summary(); sum.Unassigned != 1 || sum.Confirmed != 0 {
		t.Fatalf("summary = %+v", sum)
	}
	if err := s.Reassign(ctx, 1, "ABC-3"); err != nil {
		t.Fatal(err)
	}
	if e := s.Entries(); len(e) != 1 || e[0].Kind != KindBlock || e[0].Ticket != "ABC-3" {
		t.Fatalf("gap not resolved: %+v", e)
	}
}

func TestReviewBlocksSyncWhenCapExceededUntilAcknowledged(t *testing.T) {
	st := openStore(t)
	cfg := testConfig()
	cfg.DailyCap = 30 * time.Minute
	seed(t, st, dayStart, 60, "ABC-1")
	ctx := context.Background()
	s := newSession(t, st, cfg)
	if w := s.CapWarning(); w == nil || w.Acknowledged || w.TotalMinutes != 60 || w.CapMinutes != 30 {
		t.Fatalf("warning = %+v", w)
	}
	if err := s.Confirm(ctx, 1); err != nil {
		t.Fatal(err)
	}
	_, err := ConfirmedItems(ctx, st, cfg, testDay)
	var blocked *CapBlockedError
	if !errors.As(err, &blocked) || blocked.TotalMinutes != 60 {
		t.Fatalf("err = %v", err)
	}
	if !s.Summary().CapBlocked {
		t.Fatal("summary should report cap block")
	}
	if err := s.AcknowledgeCap(ctx); err != nil {
		t.Fatal(err)
	}
	items, err := ConfirmedItems(ctx, st, cfg, testDay)
	if err != nil || len(items) != 1 {
		t.Fatalf("after ack: %+v err=%v", items, err)
	}
	// More time changes the total: the acknowledgement no longer applies.
	seed(t, st, dayStart.Add(2*time.Hour), 30, "ABC-1")
	if _, err := ConfirmedItems(ctx, st, cfg, testDay); !errors.As(err, &blocked) {
		t.Fatalf("stale acknowledgement accepted: %v", err)
	}
	if err := newSession(t, st, testConfig()).AcknowledgeCap(ctx); !errors.Is(err, ErrNoCapWarning) {
		t.Fatalf("ack without warning err = %v", err)
	}
}

func TestReviewNeverBooksUnconfirmedItems(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	cfg := testConfig()
	seed(t, st, dayStart, 60, "ABC-1")
	seed(t, st, dayStart.Add(2*time.Hour), 60, "DEF-2")
	seed(t, st, dayStart.Add(4*time.Hour), 30, "GHI-3")

	items, err := ConfirmedItems(ctx, st, cfg, testDay)
	if err != nil || len(items) != 0 {
		t.Fatalf("unreviewed day released items: %+v err=%v", items, err)
	}

	s := newSession(t, st, cfg)
	if n, err := s.ConfirmAll(ctx); err != nil || n != 3 {
		t.Fatalf("ConfirmAll n=%d err=%v", n, err)
	}
	// Changed after confirmation: new presence extends ABC-1's neighbour DEF-2.
	for i := 61; i <= 75; i++ {
		e := event.Event{At: dayStart.Add(2*time.Hour + time.Duration(i)*time.Minute), Source: "test", Kind: event.KindPresence}
		if err := st.Append(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	// Discarded after confirmation.
	// Entries are ABC-1, idle gap, DEF-2, idle gap, GHI-3.
	if err := newSession(t, st, cfg).Discard(ctx, 5); err != nil {
		t.Fatal(err)
	}
	items, err = ConfirmedItems(ctx, st, cfg, testDay)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Ticket != "ABC-1" {
		t.Fatalf("only unchanged confirmed item may remain: %+v", items)
	}
}
