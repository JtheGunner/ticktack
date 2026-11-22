package explain

import (
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/resolver"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
)

func TestBlockNamesNeutralAppFromSampleWithIdleTime(t *testing.T) {
	// The neutral sample is stamped 30 s after the block end: the block ends at
	// the moment of the last input, the sample time minus its idle time.
	sampleAt := t0.Add(15 * time.Minute)
	events := []event.Event{{At: t0, Kind: event.KindContext, Source: "git", Repo: "/x", Branch: "ABC-2-fix"}}
	events = append(events, appPresence(t0, 0, 14, "ide")...)
	events = append(events, event.Event{At: sampleAt, Kind: event.KindPresence, Source: "presence", App: "chat", IdleSeconds: 30})
	cfg := sessionizer.DefaultConfig()
	cfg.AppClasses = map[string]config.AppClass{"chat": config.ClassNeutral}

	for _, b := range sessionizer.Derive(events, nil, cfg, resolver.Rules{}) {
		if b.Reason != sessionizer.NeutralAppReason {
			continue
		}
		if !b.End.Before(sampleAt) {
			t.Fatalf("block end %v is not before the sample time", b.End)
		}
		if ex := Block(Input{Block: b, Events: events, Config: cfg}); ex.App != "chat" {
			t.Errorf("App = %q, want chat", ex.App)
		}
		return
	}
	t.Fatalf("no neutral block derived: %+v", sessionizer.Derive(events, nil, cfg, resolver.Rules{}))
}
