package sessionizer

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/resolver"
)

var update = flag.Bool("update", false, "rewrite the expected fixture files")

type fixture struct {
	Config struct {
		GapThreshold  string `json:"gapThreshold"`
		IdleThreshold string `json:"idleThreshold"`
		MinBlock      string `json:"minBlock"`
		ContextMaxAge string `json:"contextMaxAge"`
	} `json:"config"`
	Rules struct {
		Repos []resolver.RepoRule `json:"repos"`
	} `json:"rules"`
	Events    []event.Event    `json:"events"`
	Overrides []event.Override `json:"overrides"`
}

func parseDuration(t *testing.T, s string) time.Duration {
	t.Helper()
	if s == "" {
		return 0
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		t.Fatalf("bad duration %q: %v", s, err)
	}
	return d
}

// TestReplayFixtures derives blocks from recorded event files and compares them
// with the expected files next to them.
//
// Fixture notes:
//   - A block ends at its last activity: the last present sample minus its idle
//     time (1 s in these recordings). It no longer extends by one sample
//     interval, so every expected end is one interval earlier than before and
//     the fixtures carry no sampleInterval setting.
//   - In sleep_gap.json the gap marker at 09:40 precedes the first sample after
//     the gap, the order in which the agent writes them (the marker first, then
//     the sample). Earlier the order did not matter; now a sample sorted ahead
//     of its own gap marker would start a span that the marker closes at once.
//   - Every fixture sets contextMaxAge to the default of two hours, and each
//     has a single context event per branch, so nothing confirms the context
//     later. Time from two hours after that event on is tier suggested with
//     the reason "context unconfirmed for <age>" and keeps its ticket.
//     normal_day.json: the morning block ends one second short of the limit
//     and keeps tier rule; the block after the break starts 2h20m after the
//     only context event and is suggested as a whole.
//     sleep_gap.json: the block after the sleep gap is cut at 10:00, two hours
//     after the context event; the part before stays explicit.
//     ticket_switches.json: no context is older than two hours, so nothing
//     changes.
func TestReplayFixtures(t *testing.T) {
	inputs, err := filepath.Glob(filepath.Join("testdata", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, input := range inputs {
		if strings.HasSuffix(input, ".expected.json") {
			continue
		}
		count++
		name := strings.TrimSuffix(filepath.Base(input), ".json")
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(input)
			if err != nil {
				t.Fatal(err)
			}
			var fx fixture
			if err := json.Unmarshal(raw, &fx); err != nil {
				t.Fatalf("decode fixture: %v", err)
			}
			cfg := Config{
				GapThreshold:  parseDuration(t, fx.Config.GapThreshold),
				IdleThreshold: parseDuration(t, fx.Config.IdleThreshold),
				MinBlock:      parseDuration(t, fx.Config.MinBlock),
				ContextMaxAge: parseDuration(t, fx.Config.ContextMaxAge),
			}
			blocks := Derive(fx.Events, fx.Overrides, cfg, resolver.Rules{Repos: fx.Rules.Repos})
			for i := range blocks {
				blocks[i].Start, blocks[i].End = blocks[i].Start.UTC(), blocks[i].End.UTC()
			}
			got, err := json.MarshalIndent(blocks, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, '\n')
			expectedPath := strings.TrimSuffix(input, ".json") + ".expected.json"
			if *update {
				if err := os.WriteFile(expectedPath, got, 0o600); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(expectedPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(want) {
				t.Fatalf("blocks differ from %s\ngot:\n%s\nwant:\n%s", expectedPath, got, want)
			}
		})
	}
	if count < 3 {
		t.Fatalf("expected at least 3 fixtures, found %d", count)
	}
}
