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
		GapThreshold   string `json:"gapThreshold"`
		IdleThreshold  string `json:"idleThreshold"`
		SampleInterval string `json:"sampleInterval"`
		MinBlock       string `json:"minBlock"`
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
				GapThreshold:   parseDuration(t, fx.Config.GapThreshold),
				IdleThreshold:  parseDuration(t, fx.Config.IdleThreshold),
				SampleInterval: parseDuration(t, fx.Config.SampleInterval),
				MinBlock:       parseDuration(t, fx.Config.MinBlock),
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
