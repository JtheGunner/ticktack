package review

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/resolver"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
	"github.com/JtheGunner/ticktack/internal/store"
)

// seedWithGap records ABC-1 from 09:00 to 10:00 and DEF-2 from 11:00 to 12:00,
// leaving an idle gap of one hour in between.
func seedWithGap(t *testing.T, st *store.Store) {
	t.Helper()
	seed(t, st, dayStart, 60, "ABC-1")
	seed(t, st, dayStart.Add(2*time.Hour), 60, "DEF-2")
}

func storedOverrides(t *testing.T, st *store.Store) []event.Override {
	t.Helper()
	got, err := st.Overrides(context.Background(), dayStart.Add(-24*time.Hour), dayStart.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestIdleGapIsOfferedBetweenPresenceOnly(t *testing.T) {
	st := openStore(t)
	seedWithGap(t, st)
	s := newSession(t, st, testConfig())
	got := s.Entries()
	if len(got) != 3 {
		t.Fatalf("entries = %+v, want block, idle gap, block", got)
	}
	idle := got[1]
	if idle.Kind != KindIdle || idle.Minutes != 60 || !idle.Start.Equal(dayStart.Add(time.Hour)) || !idle.End.Equal(dayStart.Add(2*time.Hour)) {
		t.Errorf("idle entry = %+v, want 10:00-11:00 as KindIdle", idle)
	}
	if got[0].Kind != KindBlock || got[2].Kind != KindBlock {
		t.Errorf("neighbours = %v and %v, want blocks", got[0].Kind, got[2].Kind)
	}
	// Offering a gap books nothing: without an explicit fill the day (and so
	// anything auto mode could book) holds the presence time only.
	if total := s.Day().TotalMinutes; total != 120 {
		t.Errorf("total = %d min, want 120: an unfilled gap must not count", total)
	}
	// Time before the first and after the last presence is not offered.
	if got[0].Start.After(dayStart) || got[2].End.Before(dayStart.Add(3*time.Hour)) {
		t.Errorf("unexpected bounds: %+v", got)
	}
}

func TestFillingAnIdleGap(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	seedWithGap(t, st)
	s := newSession(t, st, testConfig())

	if err := s.Reassign(ctx, 2, "meet-1"); err != nil {
		t.Fatal(err)
	}
	got := s.Entries()
	if len(got) != 3 {
		t.Fatalf("entries = %+v, want the gap replaced by a filled block (no second offer)", got)
	}
	f := got[1]
	if f.Kind != KindBlock || f.Ticket != "MEET-1" || f.Tier != resolver.TierExplicit || f.Reason != sessionizer.FillReason || f.Minutes != 60 {
		t.Errorf("filled entry = %+v, want explicit MEET-1 block with reason %q", f, sessionizer.FillReason)
	}
	if ovs := storedOverrides(t, st); len(ovs) != 1 || ovs[0].Kind != event.OverrideFill {
		t.Errorf("overrides = %+v, want one fill", ovs)
	}
	// Only the day's own presence plus the fill is counted, nothing twice.
	if total := s.Day().TotalMinutes; total != 180 {
		t.Errorf("total = %d min, want 180 (60 + 60 filled + 60)", total)
	}
	// Filling unassigned idle time is not a correction.
	if recs := recordsOf(t, st); len(recs) != 1 || recs[0].Corrections != 0 {
		t.Errorf("records = %+v, want no correction", recs)
	}
	// The filled block is reviewed like any other.
	if err := s.Confirm(ctx, 2); err != nil {
		t.Fatal(err)
	}
	// Reassigning the filled block keeps it a fill, so the time stays.
	if err := s.Reassign(ctx, 2, "OTH-5"); err != nil {
		t.Fatal(err)
	}
	if got := s.Entries(); len(got) != 3 || got[1].Ticket != "OTH-5" || got[1].Minutes != 60 {
		t.Errorf("after reassigning the fill: %+v", got)
	}
}

func TestSplitFillsOnlyTheSecondPartOfAnIdleGap(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	seedWithGap(t, st)
	s := newSession(t, st, testConfig())

	if err := s.Split(ctx, 2, "10:20", ""); !errors.Is(err, ErrSplitNeedsTicket) {
		t.Fatalf("split idle without ticket err = %v, want ErrSplitNeedsTicket", err)
	}
	if err := s.Split(ctx, 2, "10:20", "MEET-1"); err != nil {
		t.Fatal(err)
	}
	got := s.Entries()
	if len(got) != 4 || got[1].Kind != KindIdle || got[1].Minutes != 20 || got[2].Ticket != "MEET-1" || got[2].Minutes != 40 {
		t.Errorf("entries = %+v, want 20 min idle then 40 min MEET-1", got)
	}
}

func TestBreakIsStoredAndShownApartFromDiscard(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	seedWithGap(t, st)
	s := newSession(t, st, testConfig())

	if err := s.MarkBreak(ctx, 2); err != nil {
		t.Fatal(err)
	}
	got := s.Entries()
	if len(got) != 3 || got[1].Kind != KindBreak || !got[1].Idle || got[1].Minutes != 60 {
		t.Fatalf("entries = %+v, want a break entry of 60 min", got)
	}
	if sum := s.Summary(); sum.BreakMinutes != 60 || sum.Discarded != 0 {
		t.Errorf("summary = %+v, want 60 break minutes and no discard", sum)
	}
	if ovs := storedOverrides(t, st); len(ovs) != 1 || ovs[0].Kind != event.OverrideBreak {
		t.Errorf("overrides = %+v, want a break", ovs)
	}
	if err := s.Confirm(ctx, 2); !errors.Is(err, ErrNotConfirmable) {
		t.Errorf("confirm break err = %v, want ErrNotConfirmable", err)
	}
	if err := s.MarkBreak(ctx, 2); !errors.Is(err, ErrAlreadyBreak) {
		t.Errorf("break twice err = %v, want ErrAlreadyBreak", err)
	}
	if err := s.Split(ctx, 2, "10:30", "ABC-1"); !errors.Is(err, ErrBreakNotSplittable) {
		t.Errorf("split break err = %v, want ErrBreakNotSplittable", err)
	}
	// Breaks are never booked and never counted as unassigned.
	if n, err := s.ConfirmAll(ctx); err != nil || n != 2 {
		t.Fatalf("ConfirmAll = %d, %v, want the two blocks only", n, err)
	}
	items, err := ConfirmedItems(ctx, st, testConfig(), testDay)
	if err != nil || len(items) != 2 {
		t.Fatalf("items = %+v err=%v, want ABC-1 and DEF-2", items, err)
	}
	if sum := s.Summary(); sum.Unassigned != 0 {
		t.Errorf("unassigned = %d, want 0", sum.Unassigned)
	}
}

func TestBreakOfPresenceTimeIsNotBooked(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	seed(t, st, dayStart, 60, "ABC-1")
	s := newSession(t, st, testConfig())
	if err := s.MarkBreak(ctx, 1); err != nil {
		t.Fatal(err)
	}
	got := s.Entries()
	if len(got) != 1 || got[0].Kind != KindBreak || got[0].Idle {
		t.Fatalf("entries = %+v, want one break in presence time", got)
	}
	if s.Day().TotalMinutes != 0 || s.Summary().BreakMinutes != 60 {
		t.Errorf("total = %d, summary = %+v", s.Day().TotalMinutes, s.Summary())
	}
	// A break that had a ticket is a correction.
	if recs := recordsOf(t, st); len(recs) != 1 || recs[0].Corrections != 1 {
		t.Errorf("records = %+v, want one correction", recs)
	}
	// Giving the break a ticket again brings the presence time back.
	if err := s.Reassign(ctx, 1, "XYZ-9"); err != nil {
		t.Fatal(err)
	}
	if got := s.Entries(); len(got) != 1 || got[0].Ticket != "XYZ-9" || s.Summary().BreakMinutes != 0 {
		t.Errorf("entries = %+v, summary = %+v", got, s.Summary())
	}
}

func TestDiscardingAnIdleGapDismissesIt(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	seedWithGap(t, st)
	s := newSession(t, st, testConfig())
	if err := s.Discard(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if got := s.Entries(); len(got) != 2 {
		t.Errorf("entries = %+v, want the gap gone", got)
	}
	if sum := s.Summary(); sum.Discarded != 1 || sum.BreakMinutes != 0 {
		t.Errorf("summary = %+v", sum)
	}
	// A new session does not offer it again either.
	if got := newSession(t, st, testConfig()).Entries(); len(got) != 2 {
		t.Errorf("entries of a new session = %+v, want the gap not offered again", got)
	}
}

func TestUndoLastAction(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	seedWithGap(t, st)
	s := newSession(t, st, testConfig())

	if _, err := s.UndoLast(ctx); !errors.Is(err, ErrNothingToUndo) {
		t.Fatalf("undo with nothing err = %v, want ErrNothingToUndo", err)
	}
	if recs := recordsOf(t, st); len(recs) != 0 {
		t.Fatalf("records = %+v, a refused undo must write nothing", recs)
	}

	// discard block 1 (correction), break the idle gap, fill nothing yet.
	if err := s.Discard(ctx, 1); err != nil { // block ABC-1: entries now idle, DEF-2
		t.Fatal(err)
	}
	if err := s.MarkBreak(ctx, 1); err != nil { // idle gap
		t.Fatal(err)
	}
	if err := s.Reassign(ctx, 2, "XYZ-9"); err != nil { // block DEF-2 -> reassign (correction)
		t.Fatal(err)
	}
	if recs := recordsOf(t, st); recs[0].Corrections != 2 {
		t.Fatalf("corrections = %d, want 2 (discard of a block, reassign of a block)", recs[0].Corrections)
	}

	wantKinds := []event.OverrideKind{event.OverrideReassign, event.OverrideBreak, event.OverrideDiscard}
	wantCorrections := []int{1, 1, 0}
	for i, want := range wantKinds {
		undone, err := s.UndoLast(ctx)
		if err != nil {
			t.Fatalf("undo %d: %v", i+1, err)
		}
		if undone.EffectiveKind() != want {
			t.Errorf("undo %d removed a %s override, want %s", i+1, undone.EffectiveKind(), want)
		}
		if got := len(storedOverrides(t, st)); got != len(wantKinds)-i-1 {
			t.Errorf("after undo %d: %d overrides stored, want %d", i+1, got, len(wantKinds)-i-1)
		}
		if recs := recordsOf(t, st); recs[0].Corrections != wantCorrections[i] {
			t.Errorf("after undo %d: corrections = %d, want %d", i+1, recs[0].Corrections, wantCorrections[i])
		}
	}
	if got := s.Entries(); len(got) != 3 || got[1].Kind != KindIdle {
		t.Errorf("entries after undoing everything = %+v, want the day as it was", got)
	}
	if sum := s.Summary(); sum.Discarded != 0 || sum.BreakMinutes != 0 {
		t.Errorf("summary = %+v, want counters back at zero", sum)
	}
	if _, err := s.UndoLast(ctx); !errors.Is(err, ErrNothingToUndo) {
		t.Errorf("undo past the start err = %v, want ErrNothingToUndo", err)
	}
}

func TestUndoRevertsAFillAndOnlyThisSessionsOverrides(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	seedWithGap(t, st)

	earlier := newSession(t, st, testConfig())
	if err := earlier.Discard(ctx, 1); err != nil {
		t.Fatal(err)
	}

	s := newSession(t, st, testConfig())
	if _, err := s.UndoLast(ctx); !errors.Is(err, ErrNothingToUndo) {
		t.Fatalf("undo of an earlier session's override err = %v, want ErrNothingToUndo", err)
	}
	if err := s.Reassign(ctx, 1, "MEET-1"); err != nil { // idle gap -> fill
		t.Fatal(err)
	}
	if got := s.Entries(); got[0].Ticket != "MEET-1" {
		t.Fatalf("entries = %+v", got)
	}
	if _, err := s.UndoLast(ctx); err != nil {
		t.Fatal(err)
	}
	if got := s.Entries(); len(got) != 2 || got[0].Kind != KindIdle {
		t.Errorf("entries after undoing the fill = %+v, want the gap offered again", got)
	}
	if got := len(storedOverrides(t, st)); got != 1 {
		t.Errorf("%d overrides stored, want the earlier session's discard only", got)
	}
}

func TestSplitOfAnOverrideBlockKeepingTheTicketIsRefused(t *testing.T) {
	tests := []struct {
		name   string
		ticket string
	}{
		{"no ticket", ""},
		{"same ticket", "ZZZ-1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			st := openStore(t)
			seed(t, st, dayStart, 60, "ABC-1")
			s := newSession(t, st, testConfig())
			if err := s.Reassign(ctx, 1, "ZZZ-1"); err != nil {
				t.Fatal(err)
			}
			before := len(storedOverrides(t, st))
			if err := s.Split(ctx, 1, "09:30", tt.ticket); !errors.Is(err, ErrSplitSameTicket) {
				t.Fatalf("err = %v, want ErrSplitSameTicket", err)
			}
			if got := len(storedOverrides(t, st)); got != before || len(s.Entries()) != 1 {
				t.Errorf("state changed: %d overrides (was %d), entries %+v", got, before, s.Entries())
			}
		})
	}
	t.Run("a different ticket works", func(t *testing.T) {
		ctx := context.Background()
		st := openStore(t)
		seed(t, st, dayStart, 60, "ABC-1")
		s := newSession(t, st, testConfig())
		if err := s.Reassign(ctx, 1, "ZZZ-1"); err != nil {
			t.Fatal(err)
		}
		if err := s.Split(ctx, 1, "09:30", "YYY-2"); err != nil {
			t.Fatal(err)
		}
		if got := s.Entries(); len(got) != 2 || got[0].Ticket != "ZZZ-1" || got[1].Ticket != "YYY-2" {
			t.Errorf("entries = %+v", got)
		}
	})
}

func TestSplitOfAFilledBlockKeepingTheTicketIsRefused(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	seedWithGap(t, st)
	s := newSession(t, st, testConfig())
	if err := s.Reassign(ctx, 2, "MEET-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Split(ctx, 2, "10:30", ""); !errors.Is(err, ErrSplitSameTicket) {
		t.Fatalf("err = %v, want ErrSplitSameTicket", err)
	}
	if err := s.Split(ctx, 2, "10:30", "OTH-5"); err != nil {
		t.Fatal(err)
	}
	if got := s.Entries(); len(got) != 4 || got[1].Ticket != "MEET-1" || got[2].Ticket != "OTH-5" || got[2].Minutes != 30 {
		t.Errorf("entries = %+v", got)
	}
}

// Personal time (TICKT-70) is not counted, so it is never offered as an idle
// gap: nothing in the entry list can be filled or marked as a break there.
func TestPersonalStretchIsNotOfferedInReview(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	app := func(from time.Time, minutes int, name string) {
		for i := 0; i <= minutes; i++ {
			e := event.Event{At: from.Add(time.Duration(i) * time.Minute), Source: "test", Kind: event.KindPresence, App: name}
			if err := st.Append(ctx, e); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := st.Append(ctx, event.Event{At: dayStart, Source: "test", Kind: event.KindContext, Ticket: "ABC-1"}); err != nil {
		t.Fatal(err)
	}
	app(dayStart, 30, "ide")
	app(dayStart.Add(31*time.Minute), 30, "music")
	app(dayStart.Add(62*time.Minute), 28, "ide")
	cfg := testConfig()
	cfg.AppClasses = map[string]config.AppClass{"music": config.ClassPersonal}
	s := newSession(t, st, cfg)

	for _, e := range s.Entries() {
		if e.Kind == KindIdle || e.Kind == KindBreak {
			t.Errorf("entry %+v: personal time must not be an idle gap or break", e)
		}
	}
	if len(s.Entries()) != 2 || len(s.Day().IdleGaps) != 0 {
		t.Fatalf("entries = %+v, gaps = %+v", s.Entries(), s.Day().IdleGaps)
	}
	// There is no entry for the personal stretch to act on.
	if err := s.Reassign(ctx, 3, "MEET-1"); !errors.Is(err, ErrNoSuchEntry) {
		t.Errorf("assign err = %v, want ErrNoSuchEntry", err)
	}
	if err := s.MarkBreak(ctx, 3); !errors.Is(err, ErrNoSuchEntry) {
		t.Errorf("break err = %v, want ErrNoSuchEntry", err)
	}
	// A fill stored directly over the personal stretch gives it no time.
	if _, err := st.AddOverrideID(ctx, event.Override{Start: dayStart.Add(30 * time.Minute), End: dayStart.Add(62 * time.Minute), Ticket: "MEET-1", Kind: event.OverrideFill}); err != nil {
		t.Fatal(err)
	}
	s = newSession(t, st, cfg)
	for _, e := range s.Entries() {
		if e.Ticket == "MEET-1" {
			t.Errorf("fill reached personal time: %+v", e)
		}
	}
}
