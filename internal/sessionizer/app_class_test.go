package sessionizer

import (
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/resolver"
)

func appClassConfig() Config {
	cfg := testConfig()
	cfg.AppClasses = map[string]config.AppClass{
		"chat":  config.ClassNeutral,
		"music": config.ClassPersonal,
		"ide":   config.ClassWork,
	}
	return cfg
}

func TestDeriveAppClasses(t *testing.T) {
	gitA := ctx(0, "/r/a", "AAA-1-x")
	tests := []struct {
		name   string
		cfg    Config
		events []event.Event
		want   []wantBlock
	}{
		{
			name:   "no classes configured: every app counts as work",
			cfg:    testConfig(),
			events: join(presenceApp(0, 9, "ide"), presenceApp(10, 19, "chat"), presenceApp(20, 30, "music"), []event.Event{gitA}),
			want:   []wantBlock{{0, 30, "AAA-1", resolver.TierExplicit, ""}},
		},
		{
			name:   "unlisted app and listed work app count in full",
			cfg:    appClassConfig(),
			events: join(presenceApp(0, 9, "ide"), presenceApp(10, 20, "other"), []event.Event{gitA}),
			want:   []wantBlock{{0, 20, "AAA-1", resolver.TierExplicit, ""}},
		},
		{
			name:   "neutral app between two work stretches keeps the ticket as suggested",
			cfg:    appClassConfig(),
			events: join(presenceApp(0, 9, "ide"), presenceApp(10, 19, "chat"), presenceApp(20, 30, "ide"), []event.Event{gitA}),
			want: []wantBlock{
				{0, 9, "AAA-1", resolver.TierExplicit, ""},
				{9, 19, "AAA-1", resolver.TierSuggested, NeutralAppReason},
				{19, 30, "AAA-1", resolver.TierExplicit, ""},
			},
		},
		{
			name:   "personal app ends the span and the time is not counted",
			cfg:    appClassConfig(),
			events: join(presenceApp(0, 9, "ide"), presenceApp(10, 19, "music"), presenceApp(20, 30, "ide"), []event.Event{gitA}),
			want: []wantBlock{
				{0, 9, "AAA-1", resolver.TierExplicit, ""},
				{20, 30, "AAA-1", resolver.TierExplicit, ""},
			},
		},
		{
			name:   "a short personal stretch is not bridged by the gap threshold",
			cfg:    appClassConfig(),
			events: join(presenceApp(0, 9, "ide"), presenceApp(10, 11, "music"), presenceApp(12, 20, "ide"), []event.Event{gitA}),
			want: []wantBlock{
				{0, 9, "AAA-1", resolver.TierExplicit, ""},
				{12, 20, "AAA-1", resolver.TierExplicit, ""},
			},
		},
		{
			name:   "only personal presence yields no block at all",
			cfg:    appClassConfig(),
			events: join(presenceApp(0, 30, "music"), []event.Event{gitA}),
			want:   nil,
		},
		{
			name:   "neutral time without a ticket stays unknown",
			cfg:    appClassConfig(),
			events: join(presenceApp(0, 9, "chat")),
			want:   []wantBlock{{0, 9, "", resolver.TierUnknown, noContextReason}},
		},
		{
			name:   "neutral app does not lower a manual pin",
			cfg:    appClassConfig(),
			events: join(presenceApp(0, 9, "chat"), []event.Event{gitA, manual(0, "PIN-1")}),
			want:   []wantBlock{{0, 9, "PIN-1", resolver.TierExplicit, ""}},
		},
		{
			name: "stale neutral time keeps the stale reason",
			cfg: func() Config {
				c := appClassConfig()
				c.ContextMaxAge = 5 * time.Minute
				return c
			}(),
			events: join(presenceApp(0, 4, "ide"), presenceApp(5, 14, "chat"), []event.Event{gitA}),
			want: []wantBlock{
				{0, 4, "AAA-1", resolver.TierExplicit, ""},
				{4, 14, "AAA-1", resolver.TierSuggested, "context unconfirmed for 5m"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocks := Derive(tt.events, nil, tt.cfg, resolver.Rules{})
			assertBlocks(t, blocks, tt.want)
		})
	}
}

func TestDeriveNeutralTimeIsSuggestedNotCarriedAndNotSure(t *testing.T) {
	events := join(
		presenceApp(0, 9, "ide"), presenceApp(10, 19, "chat"), presenceApp(20, 30, "ide"),
		[]event.Event{ctx(0, "/r/a", "AAA-1-x")},
	)
	blocks := Derive(events, nil, appClassConfig(), resolver.Rules{})
	items := Aggregate(blocks, RoundingConfig{Rounding: time.Minute}, time.UTC)
	if len(items) != 1 {
		t.Fatalf("items = %+v", items)
	}
	c := items[0].Certainty
	if c.Suggested != 10 || c.Carried != 0 || c.Sure() != 20 || c.Total() != items[0].Minutes {
		t.Errorf("certainty = %+v, want 10 suggested, none carried, 20 sure", c)
	}
}

func TestDeriveShortNeutralBlockNextToWorkKeepsItsTicket(t *testing.T) {
	events := join(presenceApp(0, 9, "ide"), presenceApp(10, 10, "chat"), presenceApp(11, 20, "ide"), []event.Event{ctx(0, "/r/a", "AAA-1-x")})
	cfg := appClassConfig()
	cfg.MinBlock = 5 * time.Minute
	blocks := Derive(events, nil, cfg, resolver.Rules{})
	for _, b := range blocks {
		if b.Ticket != "AAA-1" {
			t.Fatalf("a block lost the ticket: %+v", blocks)
		}
	}
	var suggested time.Duration
	for _, b := range blocks {
		if b.Tier == resolver.TierSuggested {
			suggested += b.Duration()
		}
	}
	if suggested == 0 {
		t.Errorf("neutral time must stay suggested, got %+v", blocks)
	}
}

func TestDeriveFoldDoesNotCrossPersonalGap(t *testing.T) {
	// Without the personal stretch the 3 min look at BBB-2 would be folded into
	// AAA-1; with it the blocks do not touch, so nothing is folded and the
	// short block is demoted as usual.
	events := join(
		presenceApp(0, 9, "ide"), presenceApp(10, 12, "music"), presenceApp(13, 15, "ide"), presenceApp(16, 30, "ide"),
		[]event.Event{ctx(0, "/r/a", "AAA-1-x"), ctx(13, "/r/b", "BBB-2-y"), ctx(16, "/r/a", "AAA-1-x")},
	)
	cfg := appClassConfig()
	cfg.MinBlock = 5 * time.Minute
	blocks := Derive(events, nil, cfg, resolver.Rules{})
	for _, b := range blocks {
		if len(b.Folded) != 0 {
			t.Fatalf("nothing may be folded across a personal gap: %+v", blocks)
		}
	}
	assertBlocks(t, blocks, []wantBlock{
		{0, 9, "AAA-1", resolver.TierExplicit, ""},
		{13, 16, "", resolver.TierUnknown, minBlockReason},
		{16, 30, "AAA-1", resolver.TierExplicit, ""},
	})
}

func TestConfigFromCopiesAppClasses(t *testing.T) {
	c := config.Config{AppClasses: map[string]config.AppClass{"chat": config.ClassNeutral}}
	if got := ConfigFrom(c).appClass("chat"); got != config.ClassNeutral {
		t.Errorf("class = %q, want neutral", got)
	}
	if got := ConfigFrom(c).appClass("other"); got != config.ClassWork {
		t.Errorf("class = %q, want work", got)
	}
}
