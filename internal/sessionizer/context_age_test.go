package sessionizer

import (
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/resolver"
)

// agedConfig is testConfig with a context age limit of two hours and day
// boundaries in loc (UTC when nil).
func agedConfig(loc *time.Location) Config {
	cfg := testConfig()
	cfg.ContextMaxAge = 2 * time.Hour
	cfg.Location = loc
	return cfg
}

type wantBlock struct {
	from, to float64
	ticket   string
	tier     resolver.Tier
	reason   string // compared only when set
}

func assertBlocks(t *testing.T, got []Block, want []wantBlock) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("want %d blocks, got %d: %+v", len(want), len(got), got)
	}
	for i, w := range want {
		b := got[i]
		if !b.Start.Equal(at(w.from)) || !b.End.Equal(at(w.to)) {
			t.Errorf("block %d range = %v..%v, want %v..%v", i, b.Start, b.End, at(w.from), at(w.to))
		}
		if b.Ticket != w.ticket || b.Tier != w.tier {
			t.Errorf("block %d = %s/%s, want %s/%s", i, b.Ticket, b.Tier, w.ticket, w.tier)
		}
		if w.reason != "" && b.Reason != w.reason {
			t.Errorf("block %d reason = %q, want %q", i, b.Reason, w.reason)
		}
	}
}

// The base time is 09:00 UTC, so minute 900 is midnight UTC.
const midnight = 900

func TestDeriveContextAgeLimit(t *testing.T) {
	branch := ctx(0, "/r/a", "ABC-1-work")
	shell := func(m float64, repo, branchName string) event.Event {
		e := ctx(m, repo, branchName)
		e.Source = "shell"
		return e
	}
	tests := []struct {
		name   string
		events []event.Event
		want   []wantBlock
	}{
		{
			name:   "just below the limit stays explicit",
			events: join([]event.Event{branch}, presence(0, 119, 1)),
			want:   []wantBlock{{0, 119, "ABC-1", resolver.TierExplicit, ""}},
		},
		{
			name:   "ending exactly at the limit stays explicit",
			events: join([]event.Event{branch}, presence(0, 120, 1)),
			want:   []wantBlock{{0, 120, "ABC-1", resolver.TierExplicit, ""}},
		},
		{
			name:   "above the limit the rest is only suggested",
			events: join([]event.Event{branch}, presence(0, 150, 1)),
			want: []wantBlock{
				{0, 120, "ABC-1", resolver.TierExplicit, ""},
				{120, 150, "ABC-1", resolver.TierSuggested, "context unconfirmed for 2h"},
			},
		},
		{
			name:   "presence that starts after the limit names the real age",
			events: join([]event.Event{branch}, presence(0, 30, 1), presence(200, 230, 1)),
			want: []wantBlock{
				{0, 30, "ABC-1", resolver.TierExplicit, ""},
				{200, 230, "ABC-1", resolver.TierSuggested, "context unconfirmed for 3h20m"},
			},
		},
		{
			name:   "shell prompt on the same repo and branch resets the age",
			events: join([]event.Event{branch, shell(100, "/r/a", "ABC-1-work")}, presence(0, 200, 1)),
			want:   []wantBlock{{0, 200, "ABC-1", resolver.TierExplicit, ""}},
		},
		{
			name:   "commit on the same repo and branch resets the age",
			events: join([]event.Event{branch, ctx(119, "/r/a", "ABC-1-work")}, presence(0, 239, 1)),
			want:   []wantBlock{{0, 239, "ABC-1", resolver.TierExplicit, ""}},
		},
		{
			name:   "confirmation after expiry makes the time explicit again",
			events: join([]event.Event{branch, ctx(150, "/r/a", "ABC-1-work")}, presence(0, 200, 1)),
			want: []wantBlock{
				{0, 120, "ABC-1", resolver.TierExplicit, ""},
				{120, 150, "ABC-1", resolver.TierSuggested, "context unconfirmed for 2h"},
				{150, 200, "ABC-1", resolver.TierExplicit, ""},
			},
		},
		{
			name:   "new context starts with its own age",
			events: join([]event.Event{branch, ctx(100, "/r/b", "ABC-2-other")}, presence(0, 250, 1)),
			want: []wantBlock{
				{0, 100, "ABC-1", resolver.TierExplicit, ""},
				{100, 220, "ABC-2", resolver.TierExplicit, ""},
				{220, 250, "ABC-2", resolver.TierSuggested, "context unconfirmed for 2h"},
			},
		},
		{
			name:   "context carried from the day before is only suggested",
			events: join([]event.Event{ctx(-900, "/r/a", "ABC-1-work")}, presence(0, 30, 1)),
			want:   []wantBlock{{0, 30, "ABC-1", resolver.TierSuggested, "context unconfirmed for 15h"}},
		},
		{
			name:   "the age runs across midnight",
			events: join([]event.Event{ctx(midnight-60, "/r/a", "ABC-1-work")}, presence(midnight-60, midnight+90, 1)),
			want: []wantBlock{
				{midnight - 60, midnight + 60, "ABC-1", resolver.TierExplicit, ""},
				{midnight + 60, midnight + 90, "ABC-1", resolver.TierSuggested, "context unconfirmed for 2h"},
			},
		},
		{
			name:   "a pin is exempt from the limit on its own day",
			events: join([]event.Event{manual(0, "PIN-1")}, presence(0, 300, 1)),
			want:   []wantBlock{{0, 300, "PIN-1", resolver.TierExplicit, "manual switch to PIN-1"}},
		},
		{
			name:   "a pin resets the age of expired context",
			events: join([]event.Event{branch, manual(150, "PIN-1")}, presence(0, 400, 1)),
			want: []wantBlock{
				{0, 120, "ABC-1", resolver.TierExplicit, ""},
				{120, 150, "ABC-1", resolver.TierSuggested, "context unconfirmed for 2h"},
				{150, 400, "PIN-1", resolver.TierExplicit, "manual switch to PIN-1"},
			},
		},
		{
			name:   "a pin carried from the day before is only suggested",
			events: join([]event.Event{manual(-600, "PIN-1")}, presence(0, 30, 1)),
			want:   []wantBlock{{0, 30, "PIN-1", resolver.TierSuggested, "context unconfirmed for 10h"}},
		},
		{
			name:   "a pin stops being explicit at midnight",
			events: join([]event.Event{manual(midnight-30, "PIN-1")}, presence(midnight-30, midnight+30, 1)),
			want: []wantBlock{
				{midnight - 30, midnight, "PIN-1", resolver.TierExplicit, "manual switch to PIN-1"},
				{midnight, midnight + 30, "PIN-1", resolver.TierSuggested, "context unconfirmed for 30m"},
			},
		},
		{
			name:   "a new pin on the following day is explicit again",
			events: join([]event.Event{manual(-600, "PIN-1"), manual(10, "PIN-1")}, presence(0, 30, 1)),
			want: []wantBlock{
				{0, 10, "PIN-1", resolver.TierSuggested, "context unconfirmed for 10h"},
				{10, 30, "PIN-1", resolver.TierExplicit, "manual switch to PIN-1"},
			},
		},
		{
			name:   "clearing a pin does not confirm the automatic context",
			events: join([]event.Event{branch, manual(10, "PIN-1"), manual(150, "")}, presence(0, 180, 1)),
			want: []wantBlock{
				{0, 10, "ABC-1", resolver.TierExplicit, ""},
				{10, 150, "PIN-1", resolver.TierExplicit, ""},
				{150, 180, "ABC-1", resolver.TierSuggested, "context unconfirmed for 2h30m"},
			},
		},
		{
			name:   "automatic context seen under a pin keeps its own age",
			events: join([]event.Event{branch, manual(10, "PIN-1"), ctx(140, "/r/a", "ABC-1-work"), manual(150, "")}, presence(0, 180, 1)),
			want: []wantBlock{
				{0, 10, "ABC-1", resolver.TierExplicit, ""},
				{10, 150, "PIN-1", resolver.TierExplicit, ""},
				{150, 180, "ABC-1", resolver.TierExplicit, ""},
			},
		},
		{
			name:   "context without a ticket stays unknown",
			events: join([]event.Event{ctx(0, "/r/none", "main")}, presence(0, 150, 1)),
			want:   []wantBlock{{0, 150, "", resolver.TierUnknown, "no ticket in branch and no rule for repo"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertBlocks(t, Derive(tt.events, nil, agedConfig(nil), resolver.Rules{}), tt.want)
		})
	}
}

func TestDeriveContextAgeLimitDemotesRuleAndLearnedTiers(t *testing.T) {
	rules := resolver.Rules{
		Repos:   []resolver.RepoRule{{Repo: "/cfg", Ticket: "CFG-1"}},
		Learned: []resolver.LearnedRule{{ID: 1, Repo: "/learn", Ticket: "LRN-1"}},
	}
	tests := []struct {
		name, repo, ticket string
		tier               resolver.Tier
	}{
		{"repo rule", "/cfg", "CFG-1", resolver.TierRule},
		{"learned rule", "/learn", "LRN-1", resolver.TierLearned},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := join([]event.Event{ctx(0, tt.repo, "main")}, presence(0, 150, 1))
			assertBlocks(t, Derive(events, nil, agedConfig(nil), rules), []wantBlock{
				{0, 120, tt.ticket, tt.tier, ""},
				{120, 150, tt.ticket, resolver.TierSuggested, "context unconfirmed for 2h"},
			})
		})
	}
}

// A stale block shorter than MinBlock must not take over the tier of a
// confirmed neighbour, or auto mode would book the stale minutes.
func TestDeriveContextAgeLimitKeepsShortStaleBlocksSuggested(t *testing.T) {
	branch := ctx(0, "/r/a", "ABC-1-work")
	tests := []struct {
		name   string
		events []event.Event
		want   []wantBlock
	}{
		{
			name:   "a short stale tail is not absorbed by the confirmed block before it",
			events: join([]event.Event{branch}, presence(0, 128, 1)),
			want: []wantBlock{
				{0, 120, "ABC-1", resolver.TierExplicit, ""},
				{120, 128, "ABC-1", resolver.TierSuggested, "context unconfirmed for 2h"},
			},
		},
		{
			name:   "a short stale head is not absorbed by the confirmed block after it",
			events: join([]event.Event{ctx(-900, "/r/a", "ABC-1-work"), ctx(8, "/r/a", "ABC-1-work")}, presence(0, 30, 1)),
			want: []wantBlock{
				{0, 8, "ABC-1", resolver.TierSuggested, "context unconfirmed for 15h"},
				{8, 30, "ABC-1", resolver.TierExplicit, ""},
			},
		},
		{
			name:   "a short stale stretch between two confirmed blocks stays apart",
			events: join([]event.Event{branch, ctx(125, "/r/a", "ABC-1-work")}, presence(0, 200, 1)),
			want: []wantBlock{
				{0, 120, "ABC-1", resolver.TierExplicit, ""},
				{120, 125, "ABC-1", resolver.TierSuggested, "context unconfirmed for 2h"},
				{125, 200, "ABC-1", resolver.TierExplicit, ""},
			},
		},
		{
			name:   "a short confirmed block joins a longer stale neighbour at the lower tier",
			events: join([]event.Event{branch, ctx(150, "/r/a", "ABC-1-work")}, presence(0, 155, 1)),
			want: []wantBlock{
				{0, 120, "ABC-1", resolver.TierExplicit, ""},
				{120, 155, "ABC-1", resolver.TierSuggested, "context unconfirmed for 2h"},
			},
		},
		{
			name:   "a short stale block without a neighbour of its ticket is unknown like any short block",
			events: join([]event.Event{ctx(-900, "/r/a", "ABC-1-work")}, presence(0, 8, 1)),
			want:   []wantBlock{{0, 8, "", resolver.TierUnknown, minBlockReason}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := agedConfig(nil)
			cfg.MinBlock = 10 * time.Minute
			assertBlocks(t, Derive(tt.events, nil, cfg, resolver.Rules{}), tt.want)
		})
	}
}

func TestDeriveContextAgeLimitKeepsRepoAndBranchOnTheSuggestion(t *testing.T) {
	events := join([]event.Event{ctx(-900, "/r/a", "ABC-1-work")}, presence(0, 30, 1))
	blocks := Derive(events, nil, agedConfig(nil), resolver.Rules{})
	if len(blocks) != 1 || blocks[0].Repo != "/r/a" || blocks[0].Branch != "ABC-1-work" {
		t.Fatalf("blocks = %+v", blocks)
	}
}

func TestDeriveContextAgeLimitOff(t *testing.T) {
	events := join([]event.Event{ctx(-900, "/r/a", "ABC-1-work"), manual(-600, "PIN-1")}, presence(0, 30, 1))
	for _, maxAge := range []time.Duration{0, -time.Hour} {
		cfg := agedConfig(nil)
		cfg.ContextMaxAge = maxAge
		assertBlocks(t, Derive(events, nil, cfg, resolver.Rules{}), []wantBlock{
			{0, 30, "PIN-1", resolver.TierExplicit, "manual switch to PIN-1"},
		})
	}
}

// The day a pin belongs to is a calendar day in the configured zone.
func TestDerivePinDayFollowsTheConfiguredZone(t *testing.T) {
	zurich := mustZurich(t)
	// 22:30 UTC is 23:30 in Zurich (CET), so the local day ends 30 minutes
	// later, while the UTC day would run for another 90 minutes.
	pinAt := midnight - 90.0
	events := join([]event.Event{manual(pinAt, "PIN-1")}, presence(pinAt, pinAt+60, 1))
	assertBlocks(t, Derive(events, nil, agedConfig(zurich), resolver.Rules{}), []wantBlock{
		{pinAt, pinAt + 30, "PIN-1", resolver.TierExplicit, ""},
		{pinAt + 30, pinAt + 60, "PIN-1", resolver.TierSuggested, "context unconfirmed for 30m"},
	})
	assertBlocks(t, Derive(events, nil, agedConfig(time.UTC), resolver.Rules{}), []wantBlock{
		{pinAt, pinAt + 60, "PIN-1", resolver.TierExplicit, ""},
	})
}

func TestDeriveTitleHintConfirmsContext(t *testing.T) {
	const ide, browser = "com.example.ide", "com.example.browser"
	tests := []struct {
		name   string
		events []event.Event
		want   []wantBlock
	}{
		{
			name: "a hint for the repository git is in confirms the branch",
			events: join(
				[]event.Event{ctx(0, "/r/a", "ABC-1-work"), appHint(100, ide, "/r/a", "")},
				presenceApp(0, 200, ide),
			),
			want: []wantBlock{{0, 200, "ABC-1", resolver.TierExplicit, ""}},
		},
		{
			name: "a hint for another repository does not confirm git context",
			events: join(
				[]event.Event{ctx(0, "/r/a", "ABC-1-work"), appHint(100, ide, "/r/b", "")},
				presenceApp(0, 150, browser),
			),
			want: []wantBlock{
				{0, 120, "ABC-1", resolver.TierExplicit, ""},
				{120, 150, "ABC-1", resolver.TierSuggested, "context unconfirmed for 2h"},
			},
		},
		{
			name: "a hint that applies again keeps the age of its own event",
			events: join(
				[]event.Event{appHint(0, ide, "/r/b", "HNT-1"), ctx(1, "/r/a", "ABC-1-work")},
				presenceApp(0, 10, ide), presenceApp(11, 130, browser), presenceApp(131, 160, ide),
			),
			want: []wantBlock{
				{0, 11, "HNT-1", resolver.TierExplicit, ""},
				{11, 121, "ABC-1", resolver.TierExplicit, ""},
				{121, 131, "ABC-1", resolver.TierSuggested, "context unconfirmed for 2h"},
				{131, 160, "HNT-1", resolver.TierSuggested, "context unconfirmed for 2h11m"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertBlocks(t, Derive(tt.events, nil, agedConfig(nil), resolver.Rules{}), tt.want)
		})
	}
}

func TestStaleReasonFormatsTheAge(t *testing.T) {
	tests := []struct {
		age  time.Duration
		want string
	}{
		{2 * time.Hour, "context unconfirmed for 2h"},
		{2*time.Hour + 20*time.Minute + 59*time.Second, "context unconfirmed for 2h20m"},
		{45 * time.Minute, "context unconfirmed for 45m"},
		{30 * time.Second, "context unconfirmed for under 1m"},
		{26 * time.Hour, "context unconfirmed for 26h"},
	}
	for _, tt := range tests {
		if got := StaleReason(tt.age); got != tt.want {
			t.Errorf("StaleReason(%v) = %q, want %q", tt.age, got, tt.want)
		}
	}
}

func TestConfigFromTakesContextMaxAgeAndLocation(t *testing.T) {
	zurich := mustZurich(t)
	cfg := ConfigFrom(config.Config{ContextMaxAge: 90 * time.Minute, Location: zurich})
	if cfg.ContextMaxAge != 90*time.Minute || cfg.Location != zurich {
		t.Errorf("config = %+v", cfg)
	}
	if off := ConfigFrom(config.Config{}); off.ContextMaxAge != 0 {
		t.Errorf("a zero context_max_age must stay off, got %v", off.ContextMaxAge)
	}
	if DefaultConfig().ContextMaxAge != config.DefaultContextMaxAge {
		t.Errorf("default = %v", DefaultConfig().ContextMaxAge)
	}
}
