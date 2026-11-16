package sessionizer

import (
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/resolver"
)

var base = time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)

func at(minutes float64) time.Time {
	return base.Add(time.Duration(minutes * float64(time.Minute)))
}

// presence returns present samples every step minutes from..to inclusive.
func presence(from, to, step float64) []event.Event {
	var out []event.Event
	for m := from; m <= to; m += step {
		out = append(out, event.Event{At: at(m), Kind: event.KindPresence, IdleSeconds: 1})
	}
	return out
}

func ctx(m float64, repo, branch string) event.Event {
	return event.Event{At: at(m), Kind: event.KindContext, Source: "git", Repo: repo, Branch: branch}
}

func join(parts ...[]event.Event) []event.Event {
	var out []event.Event
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func testConfig() Config {
	return Config{GapThreshold: 5 * time.Minute, IdleThreshold: 5 * time.Minute, SampleInterval: time.Minute, MinBlock: 2 * time.Minute}
}

func total(blocks []Block) time.Duration {
	var sum time.Duration
	for _, b := range blocks {
		sum += b.Duration()
	}
	return sum
}

func TestDeriveBridgesShortGaps(t *testing.T) {
	events := join(presence(0, 10, 1), presence(14, 20, 1), []event.Event{ctx(0, "/r/a", "ABC-1-work")})
	blocks := Derive(events, nil, testConfig(), resolver.Rules{})
	if len(blocks) != 1 || !blocks[0].Start.Equal(at(0)) || !blocks[0].End.Equal(at(21)) {
		t.Fatalf("want one block 0..21, got %+v", blocks)
	}
	if blocks[0].Ticket != "ABC-1" {
		t.Fatalf("ticket = %q", blocks[0].Ticket)
	}
}

func TestDeriveDoesNotBridgeLongGaps(t *testing.T) {
	events := join(presence(0, 10, 1), presence(20, 30, 1), []event.Event{ctx(0, "/r/a", "ABC-1-work")})
	blocks := Derive(events, nil, testConfig(), resolver.Rules{})
	if len(blocks) != 2 {
		t.Fatalf("want two blocks, got %+v", blocks)
	}
	if !blocks[0].End.Equal(at(11)) || !blocks[1].Start.Equal(at(20)) {
		t.Fatalf("unexpected edges: %+v", blocks)
	}
}

func TestDeriveCarriesContextForward(t *testing.T) {
	events := join(presence(0, 30, 1), []event.Event{ctx(0, "/r/a", "ABC-1-work")})
	blocks := Derive(events, nil, testConfig(), resolver.Rules{})
	if len(blocks) != 1 || blocks[0].Ticket != "ABC-1" || blocks[0].Duration() != 31*time.Minute {
		t.Fatalf("context not carried forward: %+v", blocks)
	}
}

func TestDeriveIgnoresAppOnlyContextHints(t *testing.T) {
	hint := event.Event{At: at(10), Kind: event.KindContext, Source: "activitywatch", App: "Safari"}
	events := join(presence(0, 30, 1), []event.Event{ctx(0, "/r/a", "ABC-1-work"), hint})
	blocks := Derive(events, nil, testConfig(), resolver.Rules{})
	if len(blocks) != 1 || blocks[0].Ticket != "ABC-1" || blocks[0].Duration() != 31*time.Minute {
		t.Fatalf("app-only hint disturbed the context: %+v", blocks)
	}
}

func TestDeriveSplitsOnTicketSwitch(t *testing.T) {
	events := join(presence(0, 29, 1), []event.Event{ctx(0, "/r/a", "ABC-1-work"), ctx(15, "/r/a", "ABC-2-other")})
	blocks := Derive(events, nil, testConfig(), resolver.Rules{})
	if len(blocks) != 2 || blocks[0].Ticket != "ABC-1" || blocks[1].Ticket != "ABC-2" {
		t.Fatalf("want ABC-1 then ABC-2, got %+v", blocks)
	}
	if !blocks[0].End.Equal(at(15)) || !blocks[1].Start.Equal(at(15)) {
		t.Fatalf("split not at the switch: %+v", blocks)
	}
}

func TestDeriveUnknownBeforeFirstContext(t *testing.T) {
	events := join(presence(0, 19, 1), []event.Event{ctx(10, "/r/a", "ABC-1-work")})
	blocks := Derive(events, nil, testConfig(), resolver.Rules{})
	if len(blocks) != 2 || blocks[0].Tier != resolver.TierUnknown || blocks[0].Ticket != "" || blocks[1].Ticket != "ABC-1" {
		t.Fatalf("got %+v", blocks)
	}
}

func TestDeriveNeverCreatesTimeWithoutPresence(t *testing.T) {
	events := []event.Event{ctx(0, "/r/a", "ABC-1-work"), ctx(30, "/r/a", "ABC-2-x"), ctx(60, "/r/b", "main")}
	if blocks := Derive(events, nil, testConfig(), resolver.Rules{}); len(blocks) != 0 {
		t.Fatalf("context alone created time: %+v", blocks)
	}
	idle := []event.Event{{At: at(5), Kind: event.KindPresence, IdleSeconds: 3600}}
	if blocks := Derive(append(idle, events...), nil, testConfig(), resolver.Rules{}); len(blocks) != 0 {
		t.Fatalf("idle sample created time: %+v", blocks)
	}
}

func TestDeriveIgnoresSleepGapMarker(t *testing.T) {
	// Samples stop at minute 10, the machine sleeps until 13 (3 minutes, less
	// than the gap threshold); the marker must still end the span.
	events := join(presence(0, 10, 1), presence(13, 20, 1), []event.Event{
		{At: at(13), Kind: event.KindGap, IdleSeconds: 180},
		ctx(0, "/r/a", "ABC-1-work"),
	})
	blocks := Derive(events, nil, testConfig(), resolver.Rules{})
	if len(blocks) != 2 {
		t.Fatalf("gap marker was bridged: %+v", blocks)
	}
	if !blocks[0].End.Equal(at(11)) || !blocks[1].Start.Equal(at(13)) {
		t.Fatalf("time inside the gap counted: %+v", blocks)
	}
	if total(blocks) != 19*time.Minute {
		t.Fatalf("total = %v", total(blocks))
	}
}

func TestDeriveBackwardsClockJumpYieldsNoNegativeBlock(t *testing.T) {
	events := []event.Event{
		ctx(0, "/r/a", "ABC-1-work"),
		{ID: 1, At: at(10), Kind: event.KindPresence},
		{ID: 2, At: at(11), Kind: event.KindPresence},
		{ID: 3, At: at(5), Kind: event.KindPresence}, // clock jumped back
		{ID: 4, At: at(6), Kind: event.KindPresence},
		{ID: 5, At: at(12), Kind: event.KindPresence},
	}
	blocks := Derive(events, nil, testConfig(), resolver.Rules{})
	if len(blocks) == 0 {
		t.Fatal("expected blocks")
	}
	for _, b := range blocks {
		if !b.End.After(b.Start) {
			t.Fatalf("non-positive block: %+v", b)
		}
	}
	if len(events) != 6 || events[3].ID != 3 {
		t.Fatal("input was modified")
	}
}

func TestDeriveAppliesOverridesAfterRederive(t *testing.T) {
	events := join(presence(0, 59, 1), []event.Event{ctx(0, "/r/a", "main")})
	ov := []event.Override{
		{Start: at(20), End: at(30), Ticket: "ZZZ-9"},
		{Start: at(40), End: at(50), Discard: true},
		{Start: at(70), End: at(80), Ticket: "ZZZ-9"}, // no presence: no time
	}
	check := func(t *testing.T, rules resolver.Rules, wantRule string) {
		t.Helper()
		blocks := Derive(events, ov, testConfig(), rules)
		var zzz, rule, sum time.Duration
		for _, b := range blocks {
			sum += b.Duration()
			switch b.Ticket {
			case "ZZZ-9":
				zzz += b.Duration()
				if b.Tier != resolver.TierExplicit {
					t.Fatalf("override tier = %s", b.Tier)
				}
			case wantRule:
				rule += b.Duration()
			}
		}
		if zzz != 10*time.Minute || sum != 50*time.Minute || rule != 40*time.Minute {
			t.Fatalf("zzz=%v rule=%v sum=%v blocks=%+v", zzz, rule, sum, blocks)
		}
	}
	check(t, resolver.Rules{Repos: []resolver.RepoRule{{Repo: "/r/a", Ticket: "AAA-1"}}}, "AAA-1")
	check(t, resolver.Rules{Repos: []resolver.RepoRule{{Repo: "/r/a", Ticket: "BBB-2"}}}, "BBB-2")
}

func TestDeriveMergesShortBlockIntoSameTicketNeighbour(t *testing.T) {
	cfg := testConfig()
	cfg.MinBlock = 5 * time.Minute
	rules := resolver.Rules{Repos: []resolver.RepoRule{{Repo: "/r/a", Ticket: "AAA-1"}}}
	// Branch names with the same ticket but a different reason split the
	// block; the short middle piece is absorbed by its longer neighbour.
	events := join(presence(0, 29, 1), []event.Event{
		ctx(0, "/r/a", "AAA-1-x"), ctx(20, "/r/a", "AAA-1-y"), ctx(22, "/r/a", "AAA-1-z"),
	})
	blocks := Derive(events, nil, cfg, rules)
	var sum time.Duration
	for _, b := range blocks {
		if b.Ticket != "AAA-1" {
			t.Fatalf("unexpected ticket: %+v", blocks)
		}
		sum += b.Duration()
	}
	if len(blocks) != 2 || sum != 30*time.Minute {
		t.Fatalf("got %+v", blocks)
	}
}

func TestDeriveDemotesShortBlockWithoutMatchingNeighbour(t *testing.T) {
	cfg := testConfig()
	cfg.MinBlock = 5 * time.Minute
	events := join(presence(0, 29, 1), []event.Event{
		ctx(0, "/r/a", "AAA-1-x"), ctx(20, "/r/a", "BBB-2-y"), ctx(22, "/r/a", "CCC-3-z"),
	})
	blocks := Derive(events, nil, cfg, resolver.Rules{})
	if len(blocks) != 3 || blocks[1].Ticket != "" || blocks[1].Tier != resolver.TierUnknown {
		t.Fatalf("short block not demoted: %+v", blocks)
	}
}

func TestDeriveMergesTouchingDemotedBlocksOfDifferentContexts(t *testing.T) {
	cfg := testConfig()
	cfg.MinBlock = 5 * time.Minute
	events := join(presence(0, 8, 1), []event.Event{
		ctx(0, "/r/a", "AAA-1-x"), ctx(1, "/r/b", "BBB-2-y"), ctx(2, "/r/c", "CCC-3-z"),
		ctx(3, "/r/a", "AAA-1-x"), ctx(4, "/r/b", "BBB-2-y"), ctx(5, "/r/c", "CCC-3-z"),
	})
	blocks := Derive(events, nil, cfg, resolver.Rules{})
	if len(blocks) != 1 {
		t.Fatalf("want one merged block, got %+v", blocks)
	}
	b := blocks[0]
	if b.Ticket != "" || b.Tier != resolver.TierUnknown || b.Repo != "" || b.Branch != "" || b.Duration() != 9*time.Minute {
		t.Fatalf("merged block wrong: %+v", b)
	}
}
