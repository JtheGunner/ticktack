package review

import (
	"context"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/resolver"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
	"github.com/JtheGunner/ticktack/internal/store"
)

const (
	defaultProject = "GEN"
	defaultTask    = "GEN-1"
)

// defaultTaskConfig is testConfig with a global standard task, so every
// context without a ticket of its own resolves to defaultTask.
func defaultTaskConfig(t *testing.T, st *store.Store) config.Config {
	t.Helper()
	if err := st.SetDefaultTask(context.Background(), defaultProject, defaultTask); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig()
	cfg.DefaultIssue = &config.DefaultIssue{Project: defaultProject, Summary: "General"}
	return cfg
}

func TestRememberOfferFollowsDecidingRule(t *testing.T) {
	tests := []struct {
		name     string
		branch   string
		defaults bool
		repoRule bool
		learned  bool
		manual   bool
		wantRule resolver.RuleKind
		wantTick string
		want     bool
	}{
		{name: "no rule", branch: learnBranch, wantRule: resolver.RuleNone, want: true},
		{name: "learned rule", branch: learnBranch, learned: true, wantRule: resolver.RuleLearned, wantTick: "LRN-1", want: true},
		{name: "global default task", branch: learnBranch, defaults: true, wantRule: resolver.RuleGlobalDefault, wantTick: defaultTask, want: true},
		{name: "branch ticket", branch: "feature/ABC-5-x", defaults: true, wantRule: resolver.RuleBranchTicket, wantTick: "ABC-5"},
		{name: "repo rule", branch: learnBranch, defaults: true, repoRule: true, wantRule: resolver.RuleRepo, wantTick: "CFG-1"},
		{name: "manual switch", branch: learnBranch, defaults: true, manual: true, wantRule: sessionizer.ManualRule, wantTick: "MAN-1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := openStore(t)
			ctx := context.Background()
			cfg := testConfig()
			if tt.defaults {
				cfg = defaultTaskConfig(t, st)
			}
			if tt.repoRule {
				cfg.Rules = append(cfg.Rules, ruleFor(learnRepo, "CFG-1"))
			}
			if tt.learned {
				if _, err := st.AddLearnedRule(ctx, store.LearnedRule{Repo: learnRepo, Ticket: "LRN-1", CreatedAt: fixedNow}); err != nil {
					t.Fatal(err)
				}
			}
			seedContext(t, st, dayStart, 60, learnRepo, tt.branch)
			if tt.manual {
				pin := event.Event{At: dayStart, Source: sessionizer.ManualSource, Kind: event.KindContext, Ticket: "MAN-1"}
				if err := st.Append(ctx, pin); err != nil {
					t.Fatal(err)
				}
			}
			s := newSession(t, st, cfg)
			entries := s.Entries()
			if len(entries) != 1 {
				t.Fatalf("entries = %+v", entries)
			}
			if e := entries[0]; e.Rule != tt.wantRule || e.Ticket != tt.wantTick || e.Repo != learnRepo {
				t.Fatalf("entry = %+v, want rule %s ticket %q", e, tt.wantRule, tt.wantTick)
			}
			if err := s.Reassign(ctx, 1, "XYZ-9"); err != nil {
				t.Fatal(err)
			}
			offer, ok := s.Remembering()
			if ok != tt.want {
				t.Fatalf("offer = %+v ok=%v, want ok=%v", offer, ok, tt.want)
			}
			if ok && (offer.Repo != learnRepo || offer.Branch != tt.branch || offer.Ticket != "XYZ-9") {
				t.Errorf("offer = %+v", offer)
			}
		})
	}
}

func TestNoRememberOfferForOverrideEntry(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	cfg := defaultTaskConfig(t, st)
	seedContext(t, st, dayStart, 60, learnRepo, learnBranch)
	s := newSession(t, st, cfg)
	if err := s.Reassign(ctx, 1, "XYZ-9"); err != nil {
		t.Fatal(err)
	}
	if e := s.Entries()[0]; e.Rule != sessionizer.OverrideRule || e.Ticket != "XYZ-9" {
		t.Fatalf("entry = %+v, want an override entry", e)
	}
	if err := s.Reassign(ctx, 1, "XYZ-8"); err != nil {
		t.Fatal(err)
	}
	if offer, ok := s.Remembering(); ok {
		t.Fatalf("offer for an override entry: %+v", offer)
	}
}

func TestRememberedRuleAppliesBeforeDefaultTask(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	cfg := defaultTaskConfig(t, st)
	seedContext(t, st, dayStart, 60, learnRepo, learnBranch)
	s := newSession(t, st, cfg)
	if err := s.Reassign(ctx, 1, "XYZ-9"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Remember(ctx, false); err != nil {
		t.Fatal(err)
	}

	day2, start2 := nextDay()
	seedContext(t, st, start2, 60, learnRepo, learnBranch)
	seedContext(t, st, start2.Add(3*time.Hour), 60, learnRepo, "other")
	d, err := LoadDay(ctx, st, cfg, day2)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Blocks) != 2 {
		t.Fatalf("blocks = %+v", d.Blocks)
	}
	if b := d.Blocks[0]; b.Ticket != "XYZ-9" || b.Tier != resolver.TierLearned || b.Rule != resolver.RuleLearned {
		t.Errorf("remembered branch = %+v, want the learned rule before the standard task", b)
	}
	if b := d.Blocks[1]; b.Ticket != defaultTask || b.Rule != resolver.RuleGlobalDefault {
		t.Errorf("other branch = %+v, want the standard task", b)
	}
}
