package explain

import (
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/resolver"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
)

func appPresence(from time.Time, fromMin, toMin int, app string) []event.Event {
	var evs []event.Event
	for m := fromMin; m <= toMin; m++ {
		evs = append(evs, event.Event{At: from.Add(time.Duration(m) * time.Minute), Kind: event.KindPresence, Source: "presence", App: app})
	}
	return evs
}

func TestBlockNamesNeutralAppClass(t *testing.T) {
	events := []event.Event{{At: t0, Kind: event.KindContext, Source: "git", Repo: "/x", Branch: "ABC-2-fix"}}
	events = append(events, appPresence(t0, 0, 9, "ide")...)
	events = append(events, appPresence(t0, 10, 29, "chat")...)
	cfg := sessionizer.DefaultConfig()
	cfg.AppClasses = map[string]config.AppClass{"chat": config.ClassNeutral}

	var neutral *sessionizer.Block
	blocks := sessionizer.Derive(events, nil, cfg, resolver.Rules{})
	for i := range blocks {
		if blocks[i].Reason == sessionizer.NeutralAppReason {
			neutral = &blocks[i]
		}
	}
	if neutral == nil {
		t.Fatalf("no neutral block in %+v", blocks)
	}
	ex := Block(Input{Block: *neutral, Events: events, Config: cfg})
	if ex.Source != SourceContext || ex.AppClass != config.ClassNeutral || ex.App != "chat" || ex.Stale {
		t.Fatalf("explanation = %+v", ex)
	}
	if ex.Decision.Rule != resolver.RuleBranchTicket {
		t.Errorf("rule = %q, want the branch rule that named the ticket", ex.Decision.Rule)
	}
}

func TestBlockDoesNotCountPersonalSamplesAsPresence(t *testing.T) {
	events := []event.Event{{At: t0, Kind: event.KindContext, Source: "git", Repo: "/x", Branch: "ABC-2-fix"}}
	events = append(events, appPresence(t0, 0, 9, "ide")...)
	events = append(events, appPresence(t0, 10, 19, "music")...)
	cfg := sessionizer.DefaultConfig()
	cfg.AppClasses = map[string]config.AppClass{"music": config.ClassPersonal}
	blocks := sessionizer.Derive(events, nil, cfg, resolver.Rules{})
	if len(blocks) != 1 {
		t.Fatalf("blocks = %+v", blocks)
	}
	ex := Block(Input{Block: blocks[0], Events: events, Config: cfg})
	if ex.Presence.Samples != 10 || !ex.Presence.Next.IsZero() {
		t.Errorf("presence = %+v, want 10 samples and no later presence", ex.Presence)
	}
}
