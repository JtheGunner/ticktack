package sessionizer

import (
	"reflect"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/resolver"
)

func foldConfig() Config {
	cfg := testConfig()
	cfg.MinBlock = 5 * time.Minute
	return cfg
}

type wantFold struct {
	from, to float64
	ticket   string
}

func assertFolds(t *testing.T, b Block, want []wantFold) {
	t.Helper()
	var got []wantFold
	for _, f := range b.Folded {
		start := float64(f.Start.Sub(base) / time.Minute)
		end := float64(f.End.Sub(base) / time.Minute)
		got = append(got, wantFold{start, end, f.Ticket})
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("folded = %+v, want %+v", got, want)
	}
}

func TestDeriveFoldsShortBlockBetweenBlocksOfOneTicket(t *testing.T) {
	rules := resolver.Rules{Repos: []resolver.RepoRule{{Repo: "/r/rule", Ticket: "AAA-1"}}}
	tests := []struct {
		name      string
		events    []event.Event
		wantBlock []wantBlock
		wantFolds map[int][]wantFold // by block index
	}{
		{
			name: "A-B-A yields one block of A including B",
			events: join(presence(0, 25, 1), []event.Event{
				ctx(0, "/r/a", "AAA-1-x"), ctx(10, "/r/b", "BBB-2-y"), ctx(13, "/r/a", "AAA-1-x"),
			}),
			wantBlock: []wantBlock{{0, 25, "AAA-1", resolver.TierExplicit, ""}},
			wantFolds: map[int][]wantFold{0: {{10, 13, "BBB-2"}}},
		},
		{
			name: "short block without a ticket is folded too",
			events: join(presence(0, 25, 1), []event.Event{
				ctx(0, "/r/a", "AAA-1-x"), ctx(10, "/r/b", "main"), ctx(13, "/r/a", "AAA-1-x"),
			}),
			wantBlock: []wantBlock{{0, 25, "AAA-1", resolver.TierExplicit, ""}},
			wantFolds: map[int][]wantFold{0: {{10, 13, ""}}},
		},
		{
			name: "two short blocks in a row between A are not enclosed one by one",
			events: join(presence(0, 30, 1), []event.Event{
				ctx(0, "/r/a", "AAA-1-x"), ctx(10, "/r/b", "BBB-2-y"), ctx(12, "/r/c", "CCC-3-z"), ctx(14, "/r/a", "AAA-1-x"),
			}),
			// B and C are each shorter than the minimum, but neither is
			// enclosed by A on both sides; they merge into one unknown block.
			wantBlock: []wantBlock{
				{0, 10, "AAA-1", resolver.TierExplicit, ""},
				{10, 14, "", resolver.TierUnknown, minBlockReason},
				{14, 30, "AAA-1", resolver.TierExplicit, ""},
			},
		},
		{
			name: "A-B-C keeps the short block unknown",
			events: join(presence(0, 25, 1), []event.Event{
				ctx(0, "/r/a", "AAA-1-x"), ctx(10, "/r/b", "BBB-2-y"), ctx(13, "/r/c", "CCC-3-z"),
			}),
			wantBlock: []wantBlock{
				{0, 10, "AAA-1", resolver.TierExplicit, ""},
				{10, 13, "", resolver.TierUnknown, minBlockReason},
				{13, 25, "CCC-3", resolver.TierExplicit, ""},
			},
		},
		{
			name: "block at the minimum duration stays",
			events: join(presence(0, 25, 1), []event.Event{
				ctx(0, "/r/a", "AAA-1-x"), ctx(10, "/r/b", "BBB-2-y"), ctx(15, "/r/a", "AAA-1-x"),
			}),
			wantBlock: []wantBlock{
				{0, 10, "AAA-1", resolver.TierExplicit, ""},
				{10, 15, "BBB-2", resolver.TierExplicit, ""},
				{15, 25, "AAA-1", resolver.TierExplicit, ""},
			},
		},
		{
			name: "a presence gap before the next block stops the fold",
			events: join(presence(0, 12, 1), presence(20, 30, 1), []event.Event{
				ctx(0, "/r/a", "AAA-1-x"), ctx(10, "/r/b", "BBB-2-y"), ctx(15, "/r/a", "AAA-1-x"),
			}),
			wantBlock: []wantBlock{
				{0, 10, "AAA-1", resolver.TierExplicit, ""},
				{10, 12, "", resolver.TierUnknown, minBlockReason},
				{20, 30, "AAA-1", resolver.TierExplicit, ""},
			},
		},
		{
			name: "neighbours of different tiers: the lower tier takes the folded time",
			events: join(presence(0, 25, 1), []event.Event{
				ctx(0, "/r/a", "AAA-1-x"), ctx(10, "/r/b", "BBB-2-y"), ctx(13, "/r/rule", "main"),
			}),
			wantBlock: []wantBlock{
				{0, 10, "AAA-1", resolver.TierExplicit, ""},
				{10, 25, "AAA-1", resolver.TierRule, ""},
			},
			wantFolds: map[int][]wantFold{1: {{10, 13, "BBB-2"}}},
		},
		{
			name: "a manual switch block between blocks of one ticket is neither folded nor demoted",
			events: join(presence(0, 25, 1), []event.Event{
				ctx(0, "/r/a", "AAA-1-x"), manual(10, "PROJ-9"), manual(13, ""),
			}),
			wantBlock: []wantBlock{
				{0, 10, "AAA-1", resolver.TierExplicit, ""},
				{10, 13, "PROJ-9", resolver.TierExplicit, "manual switch to PROJ-9"},
				{13, 25, "AAA-1", resolver.TierExplicit, ""},
			},
		},
		{
			name: "a short manual switch block of the neighbours' ticket stays its own block",
			events: join(presence(0, 25, 1), []event.Event{
				ctx(0, "/r/a", "AAA-1-x"), manual(10, "AAA-1"), manual(13, ""),
			}),
			wantBlock: []wantBlock{
				{0, 10, "AAA-1", resolver.TierExplicit, ""},
				{10, 13, "AAA-1", resolver.TierExplicit, "manual switch to AAA-1"},
				{13, 25, "AAA-1", resolver.TierExplicit, ""},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocks := Derive(tt.events, nil, foldConfig(), rules)
			assertBlocks(t, blocks, tt.wantBlock)
			for i, b := range blocks {
				assertFolds(t, b, tt.wantFolds[i])
			}
		})
	}
}

func TestDeriveFoldedBlockCarriesTheNeighboursRule(t *testing.T) {
	rules := resolver.Rules{Repos: []resolver.RepoRule{{Repo: "/r/rule", Ticket: "AAA-1"}}}
	events := join(presence(0, 25, 1), []event.Event{
		ctx(0, "/r/a", "AAA-1-x"), ctx(10, "/r/b", "BBB-2-y"), ctx(13, "/r/rule", "main"),
	})
	blocks := Derive(events, nil, foldConfig(), rules)
	if len(blocks) != 2 {
		t.Fatalf("blocks = %+v", blocks)
	}
	if got := blocks[1]; got.Rule != resolver.RuleRepo || got.Repo != "/r/rule" {
		t.Errorf("folded block = %+v, want the rule and repo of the neighbour", got)
	}
}

// An override is an explicit statement: it is cut out after folding, and a
// short override block is kept however short it is.
func TestDeriveOverrideInsideFoldedBlockKeepsItsFolds(t *testing.T) {
	events := join(presence(0, 25, 1), []event.Event{
		ctx(0, "/r/a", "AAA-1-x"), ctx(10, "/r/b", "BBB-2-y"), ctx(13, "/r/a", "AAA-1-x"),
	})
	ov := []event.Override{{Start: at(20), End: at(22), Ticket: "OPS-1"}}
	blocks := Derive(events, ov, foldConfig(), resolver.Rules{})
	assertBlocks(t, blocks, []wantBlock{
		{0, 20, "AAA-1", resolver.TierExplicit, ""},
		{20, 22, "OPS-1", resolver.TierExplicit, "manual override"},
		{22, 25, "AAA-1", resolver.TierExplicit, ""},
	})
	assertFolds(t, blocks[0], []wantFold{{10, 13, "BBB-2"}})
	assertFolds(t, blocks[2], nil)
}

func TestDeriveOverrideCutsFoldedTime(t *testing.T) {
	events := join(presence(0, 25, 1), []event.Event{
		ctx(0, "/r/a", "AAA-1-x"), ctx(10, "/r/b", "BBB-2-y"), ctx(13, "/r/a", "AAA-1-x"),
	})
	ov := []event.Override{{Start: at(11), End: at(12), Ticket: "OPS-1"}}
	blocks := Derive(events, ov, foldConfig(), resolver.Rules{})
	assertBlocks(t, blocks, []wantBlock{
		{0, 11, "AAA-1", resolver.TierExplicit, ""},
		{11, 12, "OPS-1", resolver.TierExplicit, "manual override"},
		{12, 25, "AAA-1", resolver.TierExplicit, ""},
	})
	assertFolds(t, blocks[0], []wantFold{{10, 11, "BBB-2"}})
	assertFolds(t, blocks[2], []wantFold{{12, 13, "BBB-2"}})
}

// Folding works on hand-built blocks so that tiers that a context timeline
// can hardly produce are covered: a stale short block must never end up in a
// confirmed neighbour, folded time never gets a tier above the neighbour's.
func TestResolveShortBlocksFold(t *testing.T) {
	blk := func(from, to float64, ticket string, tier resolver.Tier, rule resolver.RuleKind) Block {
		return Block{Start: at(from), End: at(to), Ticket: ticket, Tier: tier, Rule: rule, Reason: string(tier) + "/" + ticket}
	}
	const suggested, explicit = resolver.TierSuggested, resolver.TierExplicit
	tests := []struct {
		name   string
		blocks []Block
		want   []wantBlock
	}{
		{
			name: "stale short block is not folded into confirmed neighbours",
			blocks: []Block{
				blk(0, 10, "AAA-1", explicit, resolver.RuleBranchTicket),
				blk(10, 13, "BBB-2", suggested, resolver.RuleBranchTicket),
				blk(13, 25, "AAA-1", explicit, resolver.RuleBranchTicket),
			},
			// Stale minutes never become confirmed time of A; as before the
			// block of another ticket is demoted.
			want: []wantBlock{
				{0, 10, "AAA-1", explicit, ""},
				{10, 13, "", resolver.TierUnknown, minBlockReason},
				{13, 25, "AAA-1", explicit, ""},
			},
		},
		{
			name: "stale short block is folded into stale neighbours (the longer one)",
			blocks: []Block{
				blk(0, 10, "AAA-1", suggested, resolver.RuleBranchTicket),
				blk(10, 13, "BBB-2", suggested, resolver.RuleBranchTicket),
				blk(13, 25, "AAA-1", suggested, resolver.RuleBranchTicket),
			},
			want: []wantBlock{{0, 10, "AAA-1", suggested, ""}, {10, 25, "AAA-1", suggested, ""}},
		},
		{
			name: "stale neighbour on one side takes the folded time",
			blocks: []Block{
				blk(0, 10, "AAA-1", explicit, resolver.RuleBranchTicket),
				blk(10, 13, "BBB-2", explicit, resolver.RuleBranchTicket),
				blk(13, 25, "AAA-1", suggested, resolver.RuleBranchTicket),
			},
			want: []wantBlock{{0, 10, "AAA-1", explicit, ""}, {10, 25, "AAA-1", suggested, ""}},
		},
		{
			name: "manual neighbours are never folded into",
			blocks: []Block{
				blk(0, 10, "AAA-1", explicit, ManualRule),
				blk(10, 13, "BBB-2", explicit, resolver.RuleBranchTicket),
				blk(13, 25, "AAA-1", explicit, ManualRule),
			},
			want: []wantBlock{
				{0, 10, "AAA-1", explicit, ""},
				{10, 13, "", resolver.TierUnknown, minBlockReason},
				{13, 25, "AAA-1", explicit, ""},
			},
		},
		{
			name: "no ticket on the neighbours means nothing to fold into",
			blocks: []Block{
				blk(0, 10, "", resolver.TierUnknown, resolver.RuleNone),
				blk(10, 13, "BBB-2", explicit, resolver.RuleBranchTicket),
				blk(13, 25, "", resolver.TierUnknown, resolver.RuleNone),
			},
			want: []wantBlock{
				{0, 10, "", resolver.TierUnknown, ""},
				{10, 13, "", resolver.TierUnknown, minBlockReason},
				{13, 25, "", resolver.TierUnknown, ""},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertBlocks(t, resolveShortBlocks(tt.blocks, 5*time.Minute), tt.want)
		})
	}
}
