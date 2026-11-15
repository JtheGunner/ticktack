package sessionizer

import (
	"testing"

	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/resolver"
)

func manual(m float64, ticket string) event.Event {
	return event.Event{At: at(m), Kind: event.KindContext, Source: ManualSource, Ticket: ticket}
}

func TestDeriveManualSwitchPinsAttribution(t *testing.T) {
	events := join(
		presence(0, 30, 1),
		[]event.Event{ctx(0, "/r/a", "ABC-1-work"), manual(10, "proj-12"), ctx(15, "/r/b", "ABC-2-other")},
	)
	blocks := Derive(events, nil, testConfig(), resolver.Rules{})
	if len(blocks) != 2 {
		t.Fatalf("want 2 blocks, got %+v", blocks)
	}
	if blocks[0].Ticket != "ABC-1" || !blocks[0].End.Equal(at(10)) {
		t.Errorf("first block = %+v", blocks[0])
	}
	b := blocks[1]
	if b.Ticket != "PROJ-12" || b.Tier != resolver.TierExplicit || b.Reason != "manual switch to PROJ-12" {
		t.Errorf("manual block = %+v", b)
	}
	if !b.Start.Equal(at(10)) || !b.End.Equal(at(31)) {
		t.Errorf("manual block range = %v..%v", b.Start, b.End)
	}
}

func TestDeriveManualClearRestoresAutomatic(t *testing.T) {
	events := join(
		presence(0, 40, 1),
		[]event.Event{ctx(0, "/r/a", "ABC-1-work"), manual(10, "PROJ-12"), ctx(15, "/r/b", "ABC-2-other"), manual(20, "")},
	)
	blocks := Derive(events, nil, testConfig(), resolver.Rules{})
	if len(blocks) != 3 {
		t.Fatalf("want 3 blocks, got %+v", blocks)
	}
	if blocks[1].Ticket != "PROJ-12" || !blocks[1].End.Equal(at(20)) {
		t.Errorf("manual block = %+v", blocks[1])
	}
	if blocks[2].Ticket != "ABC-2" || blocks[2].Tier != resolver.TierExplicit || blocks[2].Repo != "/r/b" {
		t.Errorf("restored block = %+v", blocks[2])
	}
}

func TestDeriveSecondManualSwitchReplacesFirst(t *testing.T) {
	events := join(presence(0, 30, 1), []event.Event{ctx(0, "/r/a", "ABC-1"), manual(5, "PROJ-1"), manual(15, "PROJ-2")})
	blocks := Derive(events, nil, testConfig(), resolver.Rules{})
	if len(blocks) != 3 || blocks[1].Ticket != "PROJ-1" || blocks[2].Ticket != "PROJ-2" {
		t.Fatalf("blocks = %+v", blocks)
	}
}

func TestLatestContext(t *testing.T) {
	if _, ok := LatestContext(nil, resolver.Rules{}); ok {
		t.Fatal("no events must report ok=false")
	}
	auto := []event.Event{ctx(0, "/r/a", "ABC-1-work")}
	cur, ok := LatestContext(auto, resolver.Rules{})
	if !ok || cur.Manual || cur.Attribution.Ticket != "ABC-1" || cur.Signal.Repo != "/r/a" {
		t.Fatalf("auto = %+v ok=%v", cur, ok)
	}
	pinned := append(auto, manual(5, "PROJ-12"), ctx(6, "/r/b", "ABC-2"))
	cur, _ = LatestContext(pinned, resolver.Rules{})
	if !cur.Manual || cur.Attribution.Ticket != "PROJ-12" || cur.Attribution.Tier != resolver.TierExplicit {
		t.Errorf("pinned attribution = %+v", cur)
	}
	if cur.Signal.Repo != "/r/b" {
		t.Errorf("signal must track the latest automatic context, got %+v", cur.Signal)
	}
	cleared, _ := LatestContext(append(pinned, manual(7, "")), resolver.Rules{})
	if cleared.Manual || cleared.Attribution.Ticket != "ABC-2" {
		t.Errorf("cleared = %+v", cleared)
	}
}
