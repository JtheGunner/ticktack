package review

import (
	"context"
	"testing"
	"time"

	"cloud.google.com/go/civil"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
	"github.com/JtheGunner/ticktack/internal/store"
)

// gate builds a track record by driving real review sessions against a store.
type gate struct {
	t   *testing.T
	st  *store.Store
	cfg config.Config
}

func newGate(t *testing.T) *gate {
	t.Helper()
	return &gate{t: t, st: openStore(t), cfg: testConfig()}
}

// dayBefore returns the day n days before the reference day.
func dayBefore(n int) civil.Date { return testDay.AddDays(-n) }

// at returns the instant of the local hour on day.
func (g *gate) at(day civil.Date, hour int) time.Time {
	return time.Date(day.Year, day.Month, day.Day, hour, 0, 0, 0, g.cfg.Location)
}

// work seeds minutes of presence on day from the local hour, attributed to
// ticket (empty: unassigned).
func (g *gate) work(day civil.Date, hour, minutes int, ticket string) {
	g.t.Helper()
	seed(g.t, g.st, g.at(day, hour), minutes, ticket)
}

// review runs one review session of day at the instant at and finishes it.
func (g *gate) review(day civil.Date, at time.Time, acts ...func(s *Session) error) {
	g.t.Helper()
	ctx := context.Background()
	s, err := NewSession(ctx, g.st, g.cfg, day, func() time.Time { return at })
	if err != nil {
		g.t.Fatal(err)
	}
	for _, act := range acts {
		if err := act(s); err != nil {
			g.t.Fatal(err)
		}
	}
	if err := s.Finish(ctx); err != nil {
		g.t.Fatal(err)
	}
}

// reviewClean seeds an hour for ABC-1 on day and confirms it that evening.
func (g *gate) reviewClean(day civil.Date) {
	g.t.Helper()
	g.work(day, 9, 60, "ABC-1")
	g.review(day, g.at(day, 18), confirmAll)
}

func (g *gate) ruleChange(at time.Time, action store.RuleChangeAction) {
	g.t.Helper()
	if err := g.st.RecordRuleChange(context.Background(), store.RuleChange{ChangedAt: at, Action: action}); err != nil {
		g.t.Fatal(err)
	}
}

func (g *gate) streak(now time.Time) int {
	g.t.Helper()
	n, err := TrackRecord(context.Background(), g.st, g.cfg, now, 0)
	if err != nil {
		g.t.Fatal(err)
	}
	return n
}

func confirmAll(s *Session) error {
	_, err := s.ConfirmAll(context.Background())
	return err
}

func confirmEntry(n int) func(*Session) error {
	return func(s *Session) error { return s.Confirm(context.Background(), n) }
}

func reassign(n int, ticket string) func(*Session) error {
	return func(s *Session) error { return s.Reassign(context.Background(), n, ticket) }
}

func discard(n int) func(*Session) error {
	return func(s *Session) error { return s.Discard(context.Background(), n) }
}

func markBreak(n int) func(*Session) error {
	return func(s *Session) error { return s.MarkBreak(context.Background(), n) }
}

func split(n int, clock, ticket string) func(*Session) error {
	return func(s *Session) error { return s.Split(context.Background(), n, clock, ticket) }
}

// dayOutcome is what a day does to the streak.
type dayOutcome int

const (
	dayCounts dayOutcome = iota
	// daySkipped neither counts nor ends the streak.
	daySkipped
	// dayBreaks ends the streak.
	dayBreaks
)

// TestTrackRecordDayRules pins the gate rules for one day. Every case builds
// yesterday; the day before it is always clean, so the streak tells whether
// yesterday counted (2), was skipped (1) or ended the streak (0).
func TestTrackRecordDayRules(t *testing.T) {
	day := dayBefore(1)
	tests := []struct {
		name  string
		build func(g *gate)
		want  dayOutcome
	}{
		{"every assigned item confirmed", func(g *gate) {
			g.work(day, 9, 60, "ABC-1")
			g.work(day, 11, 60, "ABC-2")
			g.review(day, g.at(day, 18), confirmAll)
		}, dayCounts},
		{"one of two assigned items confirmed", func(g *gate) {
			g.work(day, 9, 60, "ABC-1")
			g.work(day, 11, 60, "ABC-2")
			g.review(day, g.at(day, 18), confirmEntry(1))
		}, daySkipped},
		{"reviewed but nothing confirmed", func(g *gate) {
			g.work(day, 9, 60, "ABC-1")
			g.review(day, g.at(day, 18))
		}, daySkipped},
		{"minutes changed after the confirmation", func(g *gate) {
			g.work(day, 9, 60, "ABC-1")
			g.review(day, g.at(day, 18), confirmAll)
			g.work(day, 13, 30, "ABC-1")
		}, daySkipped},
		{"changed minutes confirmed again", func(g *gate) {
			g.work(day, 9, 60, "ABC-1")
			g.review(day, g.at(day, 18), confirmAll)
			g.work(day, 13, 30, "ABC-1")
			g.review(day, g.at(day, 19), confirmAll)
		}, dayCounts},
		{"new item appeared after the confirmation", func(g *gate) {
			g.work(day, 9, 60, "ABC-1")
			g.review(day, g.at(day, 18), confirmAll)
			g.work(day, 13, 30, "ABC-2")
		}, daySkipped},
		{"unassigned time left open", func(g *gate) {
			g.work(day, 7, 60, "")
			g.work(day, 9, 60, "ABC-1")
			g.review(day, g.at(day, 18), confirmAll)
		}, dayCounts},
		{"only unassigned time", func(g *gate) {
			g.work(day, 7, 60, "")
			g.review(day, g.at(day, 18))
		}, daySkipped},
		{"unassigned time assigned", func(g *gate) {
			g.work(day, 7, 60, "")
			g.work(day, 9, 60, "ABC-1")
			g.review(day, g.at(day, 18), reassign(1, "XYZ-9"), confirmAll)
		}, dayCounts},
		{"unassigned time split and assigned", func(g *gate) {
			g.work(day, 7, 60, "")
			g.work(day, 9, 60, "ABC-1")
			g.review(day, g.at(day, 18), split(1, "07:30", "XYZ-9"), confirmAll)
		}, dayCounts},
		{"unassigned time discarded", func(g *gate) {
			g.work(day, 7, 60, "")
			g.work(day, 9, 60, "ABC-1")
			g.review(day, g.at(day, 18), discard(1), confirmAll)
		}, dayCounts},
		{"assigned block reassigned", func(g *gate) {
			g.work(day, 9, 60, "ABC-1")
			g.review(day, g.at(day, 18), reassign(1, "XYZ-9"), confirmAll)
		}, dayBreaks},
		{"assigned block discarded", func(g *gate) {
			g.work(day, 9, 60, "ABC-1")
			g.work(day, 11, 60, "ABC-2")
			g.review(day, g.at(day, 18), discard(1), confirmAll)
		}, dayBreaks},
		{"assigned block marked as a break", func(g *gate) {
			g.work(day, 9, 60, "ABC-1")
			g.work(day, 11, 60, "ABC-2")
			g.review(day, g.at(day, 18), markBreak(1), confirmAll)
		}, dayBreaks},
		{"assigned block split", func(g *gate) {
			g.work(day, 9, 60, "ABC-1")
			g.review(day, g.at(day, 18), split(1, "09:30", "XYZ-9"), confirmAll)
		}, dayBreaks},
		{"correction in one session, clean session afterwards", func(g *gate) {
			g.work(day, 9, 60, "ABC-1")
			g.review(day, g.at(day, 17), reassign(1, "XYZ-9"))
			g.review(day, g.at(day, 18), confirmAll)
		}, dayBreaks},
		{"time pinned with switch", func(g *gate) {
			pin := event.Event{At: g.at(day, 9), Source: sessionizer.ManualSource, Kind: event.KindContext, Ticket: "ABC-7"}
			if err := g.st.Append(context.Background(), pin); err != nil {
				g.t.Fatal(err)
			}
			g.work(day, 9, 60, "")
			g.review(day, g.at(day, 18), confirmAll)
		}, dayCounts},
		{"rule added on the day", func(g *gate) {
			g.work(day, 9, 60, "ABC-1")
			g.review(day, g.at(day, 18), confirmAll)
			g.ruleChange(g.at(day, 19), store.RuleAdded)
		}, dayBreaks},
		{"rule edited on the day", func(g *gate) {
			g.work(day, 9, 60, "ABC-1")
			g.review(day, g.at(day, 18), confirmAll)
			g.ruleChange(g.at(day, 19), store.RuleEdited)
		}, dayBreaks},
		{"rule removed on the day", func(g *gate) {
			g.work(day, 9, 60, "ABC-1")
			g.review(day, g.at(day, 18), confirmAll)
			g.ruleChange(g.at(day, 19), store.RuleRemoved)
		}, dayBreaks},
		{"rule changed on a day without a review", func(g *gate) {
			g.ruleChange(g.at(day, 19), store.RuleAdded)
		}, dayBreaks},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newGate(t)
			g.reviewClean(dayBefore(2))
			tt.build(g)
			want := map[dayOutcome]int{dayCounts: 2, daySkipped: 1, dayBreaks: 0}[tt.want]
			if got := g.streak(fixedNow); got != want {
				t.Fatalf("streak = %d, want %d", got, want)
			}
		})
	}
}

func TestTrackRecordWalksBackFromTheMostRecentDay(t *testing.T) {
	tests := []struct {
		name  string
		build func(g *gate)
		want  int
	}{
		{"no reviews", func(*gate) {}, 0},
		{"unreviewed days are skipped", func(g *gate) {
			// Days 0, 1 and 4 reviewed (a weekend in between).
			g.reviewClean(dayBefore(0))
			g.reviewClean(dayBefore(1))
			g.reviewClean(dayBefore(4))
		}, 3},
		{"a correction ends the streak", func(g *gate) {
			g.reviewClean(dayBefore(5))
			g.reviewClean(dayBefore(4))
			g.work(dayBefore(3), 9, 60, "ABC-1")
			g.review(dayBefore(3), g.at(dayBefore(3), 18), reassign(1, "XYZ-9"), confirmAll)
			g.reviewClean(dayBefore(2))
			g.reviewClean(dayBefore(1))
		}, 2},
		{"a rule change between reviewed days ends the streak", func(g *gate) {
			g.reviewClean(dayBefore(3))
			g.ruleChange(g.at(dayBefore(2), 10), store.RuleEdited)
			g.reviewClean(dayBefore(1))
		}, 1},
		{"future days are ignored", func(g *gate) {
			g.reviewClean(dayBefore(-3))
			g.ruleChange(g.at(dayBefore(-2), 10), store.RuleAdded)
			g.reviewClean(dayBefore(0))
		}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newGate(t)
			tt.build(g)
			if got := g.streak(fixedNow); got != tt.want {
				t.Fatalf("streak = %d, want %d", got, tt.want)
			}
		})
	}
}

// A rule change belongs to its calendar day in the configured time zone.
func TestTrackRecordRuleChangeDayUsesConfiguredTimeZone(t *testing.T) {
	g := newGate(t)
	g.cfg.Location = mustLoc(t, "Pacific/Auckland")
	for n := 0; n <= 2; n++ {
		g.reviewClean(dayBefore(n))
	}
	// 2026-01-03 11:30 UTC is 2026-01-04 00:30 in Auckland: the change counts
	// against 01-04, so only 01-05 is left in the streak.
	g.ruleChange(time.Date(2026, 1, 3, 11, 30, 0, 0, time.UTC), store.RuleAdded)
	if got := g.streak(g.at(testDay, 20)); got != 1 {
		t.Fatalf("streak = %d, want 1", got)
	}
}

func TestTrackRecordStopsAtTheRequestedLength(t *testing.T) {
	g := newGate(t)
	for n := 0; n < 4; n++ {
		g.reviewClean(dayBefore(n))
	}
	n, err := TrackRecord(context.Background(), g.st, g.cfg, fixedNow, 3)
	if err != nil || n != 3 {
		t.Fatalf("streak = %d err %v, want 3", n, err)
	}
}

func recordsOf(t *testing.T, st *store.Store) []store.ReviewRecord {
	t.Helper()
	got, err := st.ReviewRecords(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestSessionCountsCorrectionsButNotConfirms(t *testing.T) {
	tests := []struct {
		name string
		// ticket of the seeded block; empty seeds unassigned time.
		ticket      string
		act         func(s *Session) error
		corrections int
	}{
		{"confirm", "ABC-1", confirmEntry(1), 0},
		{"confirm all", "ABC-1", confirmAll, 0},
		{"reassign", "ABC-1", reassign(1, "XYZ-9"), 1},
		{"discard", "ABC-1", discard(1), 1},
		{"break", "ABC-1", markBreak(1), 1},
		{"split", "ABC-1", split(1, "09:30", "XYZ-9"), 1},
		// Filling or dropping unassigned time corrects nothing: the tool
		// claimed nothing there.
		{"assign unassigned time", "", reassign(1, "XYZ-9"), 0},
		{"discard unassigned time", "", discard(1), 0},
		{"break in unassigned time", "", markBreak(1), 0},
		{"split unassigned time", "", split(1, "09:30", "XYZ-9"), 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := openStore(t)
			seed(t, st, dayStart, 60, tt.ticket)
			s := newSession(t, st, testConfig())
			if err := tt.act(s); err != nil {
				t.Fatal(err)
			}
			got := recordsOf(t, st)
			if len(got) != 1 || got[0].Corrections != tt.corrections || got[0].Day != testDay.String() {
				t.Fatalf("records = %+v, want %d corrections", got, tt.corrections)
			}
		})
	}
}

// Remembering a rule for filled unassigned time is no correction either: the
// reassignment that led to it carries the verdict.
func TestSessionRememberingARuleIsNoCorrection(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	seedContext(t, st, dayStart, 60, learnRepo, learnBranch)
	s := newSession(t, st, testConfig())
	if err := s.Reassign(ctx, 1, "ABC-2"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Remember(ctx, true); err != nil {
		t.Fatal(err)
	}
	got := recordsOf(t, st)
	if len(got) != 1 || got[0].Corrections != 0 {
		t.Fatalf("records = %+v, want no corrections", got)
	}
}

func TestSessionRecordSurvivesWithoutFinish(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	seed(t, st, dayStart, 60, "ABC-1")
	s := newSession(t, st, testConfig())
	if err := s.Confirm(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.Discard(ctx, 1); err != nil {
		t.Fatal(err)
	}
	// No Finish: the session was aborted. What it did is still logged.
	got := recordsOf(t, st)
	if len(got) != 1 || got[0].Corrections != 1 {
		t.Fatalf("records = %+v", got)
	}
}

func TestSessionFinishRecordsConfirmedCount(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	seed(t, st, dayStart, 60, "ABC-1")
	s := newSession(t, st, testConfig())
	if err := s.Confirm(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(ctx); err != nil {
		t.Fatal(err)
	}
	got := recordsOf(t, st)
	if len(got) != 1 || got[0].Confirmed != 1 || got[0].Corrections != 0 {
		t.Fatalf("records = %+v", got)
	}
	if n, err := TrackRecord(ctx, st, testConfig(), fixedNow, 0); err != nil || n != 1 {
		t.Fatalf("streak = %d err %v", n, err)
	}
}

func TestSessionWithoutActionsAndFinishLeavesNoRecord(t *testing.T) {
	st := openStore(t)
	seed(t, st, dayStart, 60, "ABC-1")
	newSession(t, st, testConfig())
	if got := recordsOf(t, st); len(got) != 0 {
		t.Fatalf("records = %+v", got)
	}
}

func TestSessionConfirmedCountsOnlyItsOwnConfirmations(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	seed(t, st, dayStart, 60, "ABC-1")
	first := newSession(t, st, testConfig())
	if err := first.Confirm(ctx, 1); err != nil {
		t.Fatal(err)
	}
	// A second session opens the already confirmed day and quits at once.
	second := newSession(t, st, testConfig())
	if err := second.Finish(ctx); err != nil {
		t.Fatal(err)
	}
	got := recordsOf(t, st)
	if len(got) != 2 || got[0].Confirmed != 1 || got[1].Confirmed != 0 || got[1].Corrections != 0 {
		t.Fatalf("records = %+v", got)
	}
	if got[0].ID == got[1].ID {
		t.Fatal("sessions share a row")
	}
}

func TestCorrectedSessionFollowedByCleanSessionStaysCorrected(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	seed(t, st, dayStart, 60, "ABC-1")
	first := newSession(t, st, testConfig())
	if err := first.Reassign(ctx, 1, "XYZ-9"); err != nil {
		t.Fatal(err)
	}
	if err := first.Finish(ctx); err != nil {
		t.Fatal(err)
	}
	second := newSession(t, st, testConfig())
	if _, err := second.ConfirmAll(ctx); err != nil {
		t.Fatal(err)
	}
	if err := second.Finish(ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := TrackRecord(ctx, st, testConfig(), fixedNow, 0); err != nil || n != 0 {
		t.Fatalf("streak = %d err %v, want 0", n, err)
	}
}
