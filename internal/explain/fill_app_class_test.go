package explain

import (
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/resolver"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
)

// A filled gap next to a neutral block, with app classes configured: the fill
// is explained as a manual fill, the neutral block still as neutral, and a
// personal stretch inside the range of the fill is not part of it.
func TestFillNextToNeutralBlockAndPersonalTime(t *testing.T) {
	minute := func(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }
	events := []event.Event{{At: t0, Kind: event.KindContext, Source: "git", Repo: "/x", Branch: "ABC-2-fix"}}
	events = append(events, appPresence(t0, 0, 9, "ide")...)
	events = append(events, appPresence(t0, 10, 19, "chat")...)
	events = append(events, appPresence(t0, 30, 34, "music")...)
	events = append(events, appPresence(t0, 60, 70, "ide")...)
	cfg := sessionizer.DefaultConfig()
	cfg.AppClasses = map[string]config.AppClass{"chat": config.ClassNeutral, "music": config.ClassPersonal}
	overrides := []event.Override{{Start: minute(19), End: minute(60), Ticket: "MEET-1", Kind: event.OverrideFill}}

	blocks := sessionizer.Derive(events, overrides, cfg, resolver.Rules{})
	var neutral, fill []sessionizer.Block
	for _, b := range blocks {
		switch b.Reason {
		case sessionizer.NeutralAppReason:
			neutral = append(neutral, b)
		case sessionizer.FillReason:
			fill = append(fill, b)
		}
	}
	if len(neutral) != 1 || len(fill) != 2 {
		t.Fatalf("blocks = %+v, want one neutral block and a fill split around the personal stretch", blocks)
	}
	// The personal stretch 30-34 is not covered by any fill.
	for _, b := range fill {
		if b.Start.Before(minute(34)) && b.End.After(minute(30)) {
			t.Errorf("fill %v-%v covers personal time", b.Start, b.End)
		}
	}
	in := Input{Events: events, Overrides: overrides, Config: cfg}
	in.Block = neutral[0]
	if ex := Block(in); ex.Source != SourceContext || ex.AppClass != config.ClassNeutral || ex.App != "chat" {
		t.Errorf("neutral block explained as %+v", ex)
	}
	in.Block = fill[0]
	ex := Block(in)
	if ex.Source != SourceFill || ex.Override.Ticket != "MEET-1" || ex.Override.Kind != event.OverrideFill {
		t.Errorf("fill explained as %+v", ex)
	}
}
