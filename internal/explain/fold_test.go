package explain

import (
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/resolver"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
)

func TestBlockNamesTheFoldedShortBlock(t *testing.T) {
	cfg := shortCfg()
	events := []event.Event{
		ctxAt(t0, "/a", "", "AAA-1"),
		ctxAt(t0.Add(20*time.Minute), "/b", "", "BBB-2"),
		ctxAt(t0.Add(28*time.Minute), "/a", "", "AAA-1"),
	}
	events = append(events, presenceRun(t0, 40)...)
	blocks := sessionizer.Derive(events, nil, cfg, resolver.Rules{})
	if len(blocks) != 1 {
		t.Fatalf("want A-B-A folded into one block, got %+v", blocks)
	}
	ex := Block(Input{Block: blocks[0], Events: events, Config: cfg})
	if ex.Source != SourceContext || ex.Decision.Rule != resolver.RuleEventTicket {
		t.Fatalf("explanation = %v / %v, want the rule of the enclosing blocks", ex.Source, ex.Decision.Rule)
	}
	if len(ex.Folded) != 1 || ex.Folded[0].Ticket != "BBB-2" ||
		!ex.Folded[0].Start.Equal(t0.Add(20*time.Minute)) || !ex.Folded[0].End.Equal(t0.Add(28*time.Minute)) {
		t.Errorf("folded = %+v", ex.Folded)
	}
}

func TestBlockWithoutFoldsNamesNone(t *testing.T) {
	cfg := shortCfg()
	events := append([]event.Event{ctxAt(t0, "/a", "", "AAA-1")}, presenceRun(t0, 20)...)
	blocks := sessionizer.Derive(events, nil, cfg, resolver.Rules{})
	if ex := Block(Input{Block: blocks[0], Events: events, Config: cfg}); len(ex.Folded) != 0 {
		t.Errorf("folded = %+v", ex.Folded)
	}
}
