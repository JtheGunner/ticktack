package explain

import (
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/resolver"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
)

var t0 = time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)

func presenceRun(from time.Time, minutes int) []event.Event {
	var evs []event.Event
	for m := 0; m <= minutes; m++ {
		evs = append(evs, event.Event{At: from.Add(time.Duration(m) * time.Minute), Kind: event.KindPresence, Source: "presence"})
	}
	return evs
}

func TestBlockNamesDecidingRule(t *testing.T) {
	rules := resolver.Rules{
		Repos:   []resolver.RepoRule{{Repo: "/cfg", Ticket: "CFG-1"}},
		Learned: []resolver.LearnedRule{{ID: 7, Repo: "/learn", Branch: "dev", Ticket: "LRN-7"}},
	}
	tests := []struct {
		name        string
		ctx         event.Event
		wantRule    resolver.RuleKind
		wantSource  Source
		wantSkipped int
		wantID      int64
	}{
		{"event ticket", event.Event{Repo: "/x", Ticket: "evt-1"}, resolver.RuleEventTicket, SourceContext, 0, 0},
		{"branch ticket", event.Event{Repo: "/x", Branch: "ABC-2-fix"}, resolver.RuleBranchTicket, SourceContext, 1, 0},
		{"repo rule", event.Event{Repo: "/cfg/sub", Branch: "main"}, resolver.RuleRepo, SourceContext, 2, 0},
		{"learned rule", event.Event{Repo: "/learn", Branch: "dev"}, resolver.RuleLearned, SourceContext, 3, 7},
		{"unknown", event.Event{Repo: "/none", Branch: "main"}, resolver.RuleNone, SourceContext, 4, 0},
		{"manual", event.Event{Source: sessionizer.ManualSource, Ticket: "MAN-1"}, sessionizer.ManualRule, SourceContext, 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := tc.ctx
			ctx.At, ctx.Kind = t0, event.KindContext
			if ctx.Source == "" {
				ctx.Source = "shell"
			}
			events := append([]event.Event{ctx}, presenceRun(t0, 30)...)
			cfg := sessionizer.DefaultConfig()
			blocks := sessionizer.Derive(events, nil, cfg, rules)
			if len(blocks) != 1 {
				t.Fatalf("blocks = %+v", blocks)
			}
			ex := Block(Input{Block: blocks[0], Events: events, Rules: rules, Config: cfg})
			if ex.Source != tc.wantSource || ex.Decision.Rule != tc.wantRule || len(ex.Skipped) != tc.wantSkipped || ex.Decision.LearnedID != tc.wantID {
				t.Fatalf("explanation = %+v", ex)
			}
			if ex.Event.Source != ctx.Source || !ex.Event.At.Equal(t0) {
				t.Errorf("deciding event = %+v", ex.Event)
			}
			if ex.Presence.Samples == 0 {
				t.Error("no presence evidence")
			}
		})
	}
}

func TestBlockSourcesWithoutContext(t *testing.T) {
	cfg := sessionizer.DefaultConfig()
	events := presenceRun(t0, 30)
	blocks := sessionizer.Derive(events, nil, cfg, resolver.Rules{})
	ex := Block(Input{Block: blocks[0], Events: events, Config: cfg})
	if ex.Source != SourceNoContext {
		t.Fatalf("source = %v", ex.Source)
	}

	ov := []event.Override{{Start: t0, End: t0.Add(10 * time.Minute), Ticket: "OVR-1"}}
	blocks = sessionizer.Derive(events, ov, cfg, resolver.Rules{})
	ex = Block(Input{Block: blocks[0], Events: events, Overrides: ov, Config: cfg})
	if ex.Source != SourceOverride || ex.Override.Ticket != "OVR-1" {
		t.Fatalf("override explanation = %+v", ex)
	}
}

func TestPresenceEvidenceReportsGapsAndNeighbours(t *testing.T) {
	cfg := sessionizer.DefaultConfig()
	events := []event.Event{{At: t0, Kind: event.KindContext, Source: "shell", Repo: "/r", Branch: "ABC-1"}}
	events = append(events, presenceRun(t0, 20)...)
	resume := t0.Add(60 * time.Minute)
	events = append(events, event.Event{At: resume, Kind: event.KindGap, IdleSeconds: 39 * 60, Source: "presence"})
	events = append(events, presenceRun(resume, 5)...)
	blocks := sessionizer.Derive(events, nil, cfg, resolver.Rules{})
	if len(blocks) != 2 {
		t.Fatalf("blocks = %+v", blocks)
	}
	ex := Block(Input{Block: blocks[0], Events: events, Config: cfg})
	if len(ex.Presence.Gaps) != 1 || ex.Presence.Next.IsZero() == false && !ex.Presence.Next.Equal(resume) {
		t.Fatalf("presence = %+v", ex.Presence)
	}
}

func TestPresenceEvidenceJudgesSamplesByTheirLastInput(t *testing.T) {
	cfg := sessionizer.DefaultConfig()
	events := []event.Event{{At: t0, Kind: event.KindContext, Source: "shell", Repo: "/r", Branch: "ABC-1"}}
	events = append(events, presenceRun(t0, 10)...)
	// The user stops at minute 10; the next two samples only report growing
	// idle time and must not count as new activity.
	events = append(events,
		event.Event{At: t0.Add(11 * time.Minute), Kind: event.KindPresence, IdleSeconds: 60, Source: "presence"},
		event.Event{At: t0.Add(12 * time.Minute), Kind: event.KindPresence, IdleSeconds: 120, Source: "presence"},
	)
	back := t0.Add(60 * time.Minute)
	events = append(events, presenceRun(back, 5)...)

	blocks := sessionizer.Derive(events, nil, cfg, resolver.Rules{})
	if len(blocks) != 2 {
		t.Fatalf("blocks = %+v", blocks)
	}
	ex := Block(Input{Block: blocks[0], Events: events, Config: cfg})
	wantEnd := t0.Add(10 * time.Minute)
	if !blocks[0].End.Equal(wantEnd) || !ex.Presence.Last.Equal(wantEnd) {
		t.Fatalf("block end = %v, last activity = %v, want %v", blocks[0].End, ex.Presence.Last, wantEnd)
	}
	if !ex.Presence.Next.Equal(back) {
		t.Fatalf("next activity = %v, want %v (idle samples are no activity)", ex.Presence.Next, back)
	}
}

func shortCfg() sessionizer.Config {
	cfg := sessionizer.DefaultConfig()
	cfg.MinBlock = 10 * time.Minute
	return cfg
}

func ctxAt(at time.Time, repo, branch, ticket string) event.Event {
	return event.Event{At: at, Kind: event.KindContext, Source: "shell", Repo: repo, Branch: branch, Ticket: ticket}
}

func TestBlockMergedShortBlockUsesMatchingStep(t *testing.T) {
	rules := resolver.Rules{Repos: []resolver.RepoRule{{Repo: "/cfg", Ticket: "CFG-1"}}}
	cfg := shortCfg()
	// A 4 min block with an explicit CFG-1 ticket (no branch) precedes a 20 min repo-rule
	// block for CFG-1: they merge under the longer block's tier and reason.
	events := []event.Event{ctxAt(t0, "/cfg", "", "CFG-1"), ctxAt(t0.Add(4*time.Minute), "/cfg", "main", "")}
	events = append(events, presenceRun(t0, 24)...)
	blocks := sessionizer.Derive(events, nil, cfg, rules)
	if len(blocks) != 1 || blocks[0].Tier != resolver.TierRule {
		t.Fatalf("blocks = %+v", blocks)
	}
	ex := Block(Input{Block: blocks[0], Events: events, Rules: rules, Config: cfg})
	if ex.Source != SourceContext || ex.Decision.Rule != resolver.RuleRepo || ex.Decision.Tier != blocks[0].Tier {
		t.Fatalf("explanation names %v/%v for a %v block", ex.Source, ex.Decision.Rule, blocks[0].Tier)
	}
	if !ex.Event.At.Equal(t0.Add(4 * time.Minute)) {
		t.Errorf("deciding event at %v", ex.Event.At)
	}
}

func TestBlockSpanningTwoStepsWithSameAttribution(t *testing.T) {
	rules := resolver.Rules{Repos: []resolver.RepoRule{{Repo: "/cfg", Ticket: "CFG-1"}}}
	cfg := sessionizer.DefaultConfig()
	events := []event.Event{ctxAt(t0, "/cfg", "main", ""), ctxAt(t0.Add(10*time.Minute), "/cfg", "main", "")}
	events = append(events, presenceRun(t0, 30)...)
	blocks := sessionizer.Derive(events, nil, cfg, rules)
	if len(blocks) != 1 {
		t.Fatalf("blocks = %+v", blocks)
	}
	ex := Block(Input{Block: blocks[0], Events: events, Rules: rules, Config: cfg})
	if ex.Source != SourceContext || !ex.Event.At.Equal(t0) {
		t.Fatalf("explanation = %+v", ex)
	}
}

func TestBlockMergedFromDifferentRulesListsSignals(t *testing.T) {
	rules := resolver.Rules{Repos: []resolver.RepoRule{{Repo: "/cfg", Ticket: "CFG-1"}}}
	events := []event.Event{ctxAt(t0, "/cfg", "main", ""), ctxAt(t0.Add(10*time.Minute), "/x", "", "CFG-1")}
	// A block whose attribution matches none of its steps (as after a merge
	// that no single signal explains).
	b := sessionizer.Block{Start: t0, End: t0.Add(20 * time.Minute), Ticket: "CFG-1", Tier: resolver.TierLearned, Reason: "learned rule for /y"}
	ex := Block(Input{Block: b, Events: events, Rules: rules, Config: sessionizer.DefaultConfig()})
	if ex.Source != SourceMerged || len(ex.Merged) != 2 {
		t.Fatalf("explanation = %+v", ex)
	}
	if ex.Merged[0].Decision.Rule != resolver.RuleRepo || ex.Merged[1].Decision.Rule != resolver.RuleEventTicket {
		t.Errorf("merged rules = %v, %v", ex.Merged[0].Decision.Rule, ex.Merged[1].Decision.Rule)
	}
}

func TestBlockNamesTheAgeOfStaleContext(t *testing.T) {
	cfg := sessionizer.DefaultConfig()
	cfg.ContextMaxAge = 2 * time.Hour
	shell := func(at time.Time) event.Event {
		return event.Event{At: at, Kind: event.KindContext, Source: "shell", Repo: "/x", Branch: "ABC-2-fix"}
	}
	pin := event.Event{At: t0.Add(-10 * time.Hour), Kind: event.KindContext, Source: sessionizer.ManualSource, Ticket: "MAN-1"}
	tests := []struct {
		name       string
		events     []event.Event
		block      int
		wantStale  bool
		wantAge    time.Duration
		wantPinned bool
		wantRule   resolver.RuleKind
		wantEvent  time.Time
	}{
		{"below the limit", append([]event.Event{shell(t0)}, presenceRun(t0, 150)...), 0, false, 0, false, resolver.RuleBranchTicket, t0},
		{"above the limit", append([]event.Event{shell(t0)}, presenceRun(t0, 150)...), 1, true, 2 * time.Hour, false, resolver.RuleBranchTicket, t0},
		{
			"carried across midnight", append([]event.Event{shell(t0.Add(-15 * time.Hour))}, presenceRun(t0, 30)...),
			0, true, 15 * time.Hour, false, resolver.RuleBranchTicket, t0.Add(-15 * time.Hour),
		},
		{"pin of an earlier day", append([]event.Event{pin}, presenceRun(t0, 30)...), 0, true, 10 * time.Hour, true, sessionizer.ManualRule, pin.At},
		{
			"pin of the same day", append([]event.Event{{At: t0, Kind: event.KindContext, Source: sessionizer.ManualSource, Ticket: "MAN-1"}}, presenceRun(t0, 300)...),
			0, false, 0, false, sessionizer.ManualRule, t0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			blocks := sessionizer.Derive(tc.events, nil, cfg, resolver.Rules{})
			if tc.block >= len(blocks) {
				t.Fatalf("blocks = %+v", blocks)
			}
			ex := Block(Input{Block: blocks[tc.block], Events: tc.events, Config: cfg})
			if ex.Source != SourceContext || ex.Decision.Rule != tc.wantRule || !ex.Event.At.Equal(tc.wantEvent) {
				t.Fatalf("explanation = %+v", ex)
			}
			if ex.Stale != tc.wantStale || ex.Age != tc.wantAge || ex.Pinned != tc.wantPinned {
				t.Errorf("stale = %v, age = %v, pinned = %v", ex.Stale, ex.Age, ex.Pinned)
			}
			if !tc.wantStale {
				return
			}
			if !ex.ConfirmedAt.Equal(tc.wantEvent) || ex.MaxAge != 2*time.Hour {
				t.Errorf("confirmed at %v, limit %v", ex.ConfirmedAt, ex.MaxAge)
			}
			if blocks[tc.block].Reason != sessionizer.StaleReason(ex.Age) {
				t.Errorf("block reason %q does not name the age %v", blocks[tc.block].Reason, ex.Age)
			}
		})
	}
}

// A short confirmed block that merged into a stale neighbour of its ticket
// starts before the context went stale, or under a later confirmation; the
// tier still comes from the stale context, so the age is named.
func TestBlockNamesTheAgeOfStaleContextMergedWithConfirmedTime(t *testing.T) {
	cfg := shortCfg()
	cfg.ContextMaxAge = 2 * time.Hour
	shell := func(at time.Time) event.Event { return ctxAt(at, "/x", "ABC-2-fix", "") }
	tests := []struct {
		name            string
		events          []event.Event
		wantAge         time.Duration
		wantConfirmedAt time.Time
	}{
		{
			"confirmed head shorter than the stale tail",
			append([]event.Event{shell(t0)}, presenceRun(t0.Add(115*time.Minute), 12)...),
			2 * time.Hour, t0,
		},
		{
			"confirmed head longer than the stale tail",
			append([]event.Event{shell(t0)}, presenceRun(t0.Add(113*time.Minute), 12)...),
			2 * time.Hour, t0,
		},
		{
			"stale head before a short confirmed tail",
			append([]event.Event{shell(t0.Add(-15 * time.Hour)), shell(t0.Add(7 * time.Minute))}, presenceRun(t0, 12)...),
			15 * time.Hour, t0.Add(-15 * time.Hour),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			blocks := sessionizer.Derive(tc.events, nil, cfg, resolver.Rules{})
			if len(blocks) != 1 || blocks[0].Tier != resolver.TierSuggested || blocks[0].Duration() != 12*time.Minute {
				t.Fatalf("blocks = %+v", blocks)
			}
			ex := Block(Input{Block: blocks[0], Events: tc.events, Config: cfg})
			if ex.Source != SourceContext || ex.Decision.Rule != resolver.RuleBranchTicket {
				t.Fatalf("explanation = %+v", ex)
			}
			if !ex.Stale || ex.Age != tc.wantAge || !ex.ConfirmedAt.Equal(tc.wantConfirmedAt) || ex.Pinned {
				t.Errorf("stale = %v, age = %v, confirmed at %v, pinned = %v", ex.Stale, ex.Age, ex.ConfirmedAt, ex.Pinned)
			}
			if blocks[0].Reason != sessionizer.StaleReason(ex.Age) {
				t.Errorf("block reason %q does not name the age %v", blocks[0].Reason, ex.Age)
			}
		})
	}
}

// An override that removes part of a stale block leaves pieces that keep the
// reason of the whole block, so the age is the one at the start of the stale
// time before the cut, not at the start of the piece.
func TestBlockNamesTheAgeOfStaleContextCutByAnOverride(t *testing.T) {
	cfg := sessionizer.DefaultConfig()
	cfg.ContextMaxAge = 2 * time.Hour
	events := append([]event.Event{ctxAt(t0, "/x", "ABC-2-fix", "")}, presenceRun(t0, 300)...)
	minute := func(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }
	tests := []struct {
		name      string
		override  event.Override
		wantStart []time.Time
	}{
		{
			"front reassigned",
			event.Override{Start: minute(120), End: minute(180), Ticket: "OTH-1"},
			[]time.Time{minute(180)},
		},
		{
			"split in the middle",
			event.Override{Start: minute(150), End: minute(200), Ticket: "OTH-1"},
			[]time.Time{minute(120), minute(200)},
		},
		{
			"front discarded",
			event.Override{Start: minute(100), End: minute(240), Discard: true},
			[]time.Time{minute(240)},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			overrides := []event.Override{tc.override}
			var stale []sessionizer.Block
			for _, b := range sessionizer.Derive(events, overrides, cfg, resolver.Rules{}) {
				if b.Tier == resolver.TierSuggested {
					stale = append(stale, b)
				}
			}
			if len(stale) != len(tc.wantStart) {
				t.Fatalf("stale blocks = %+v", stale)
			}
			for i, b := range stale {
				if !b.Start.Equal(tc.wantStart[i]) {
					t.Fatalf("stale block %d starts %v, want %v", i, b.Start, tc.wantStart[i])
				}
				ex := Block(Input{Block: b, Events: events, Overrides: overrides, Config: cfg})
				if ex.Source != SourceContext || ex.Decision.Rule != resolver.RuleBranchTicket || !ex.Event.At.Equal(t0) {
					t.Fatalf("explanation of block %d = %+v", i, ex)
				}
				if !ex.Stale || ex.Age != 2*time.Hour || !ex.ConfirmedAt.Equal(t0) || ex.Pinned {
					t.Errorf("block %d: stale = %v, age = %v, confirmed at %v, pinned = %v", i, ex.Stale, ex.Age, ex.ConfirmedAt, ex.Pinned)
				}
				if b.Reason != sessionizer.StaleReason(ex.Age) {
					t.Errorf("block %d reason %q does not name the age %v", i, b.Reason, ex.Age)
				}
			}
		})
	}
}

// A pin of an earlier day is stale whatever the age limit is, also when the
// limit is switched off.
func TestBlockNamesStalePinWithTheAgeLimitOff(t *testing.T) {
	cfg := sessionizer.DefaultConfig()
	cfg.ContextMaxAge = 0
	pin := event.Event{At: t0.Add(-10 * time.Hour), Kind: event.KindContext, Source: sessionizer.ManualSource, Ticket: "MAN-1"}
	events := append([]event.Event{pin}, presenceRun(t0, 30)...)
	blocks := sessionizer.Derive(events, nil, cfg, resolver.Rules{})
	if len(blocks) != 1 {
		t.Fatalf("blocks = %+v", blocks)
	}
	ex := Block(Input{Block: blocks[0], Events: events, Config: cfg})
	if ex.Source != SourceContext || !ex.Stale || !ex.Pinned || ex.Age != 10*time.Hour || ex.MaxAge != 0 {
		t.Fatalf("explanation = %+v", ex)
	}
	if blocks[0].Reason != sessionizer.StaleReason(ex.Age) {
		t.Errorf("block reason %q does not name the age %v", blocks[0].Reason, ex.Age)
	}
}

// The age is counted from the last confirmation, not from the event that
// first set the context.
func TestBlockCountsAgeFromTheLastConfirmation(t *testing.T) {
	cfg := sessionizer.DefaultConfig()
	ctx := event.Event{At: t0, Kind: event.KindContext, Source: "shell", Repo: "/x", Branch: "ABC-2-fix"}
	again := ctx
	again.At = t0.Add(30 * time.Minute)
	events := append([]event.Event{ctx, again}, presenceRun(t0, 200)...)
	blocks := sessionizer.Derive(events, nil, cfg, resolver.Rules{})
	if len(blocks) != 2 {
		t.Fatalf("blocks = %+v", blocks)
	}
	ex := Block(Input{Block: blocks[1], Events: events, Config: cfg})
	if !ex.Stale || !ex.ConfirmedAt.Equal(again.At) || ex.Age != 2*time.Hour {
		t.Fatalf("explanation = %+v", ex)
	}
}
