package sessionizer

import (
	"testing"

	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/resolver"
)

// presenceApp returns present samples every minute from..to inclusive, all in app.
func presenceApp(from, to float64, app string) []event.Event {
	var out []event.Event
	for m := from; m <= to; m++ {
		out = append(out, event.Event{At: at(m), Kind: event.KindPresence, IdleSeconds: 1, App: app})
	}
	return out
}

func appHint(m float64, app, repo, ticket string) event.Event {
	return event.Event{At: at(m), Kind: event.KindContext, Source: "title", App: app, Repo: repo, Ticket: ticket}
}

type span struct {
	ticket     string
	start, end float64
}

func TestPrecedencePinBeatsHintBeatsGit(t *testing.T) {
	gitCtx := ctx(0, "/r/a", "ABC-1-work")
	tests := []struct {
		name   string
		events []event.Event
		want   []span
	}{
		{
			name: "hint beats git while its app is frontmost",
			events: join(
				presenceApp(0, 4, "term"), presenceApp(5, 9, "ide"), presenceApp(10, 20, "term"),
				[]event.Event{gitCtx, appHint(5, "ide", "", "WEB-7")},
			),
			want: []span{{"ABC-1", 0, 5}, {"WEB-7", 5, 10}, {"ABC-1", 10, 21}},
		},
		{
			name: "hint is ignored when its app is not frontmost",
			events: join(
				presenceApp(0, 20, "term"),
				[]event.Event{gitCtx, appHint(5, "ide", "", "WEB-7")},
			),
			want: []span{{"ABC-1", 0, 21}},
		},
		{
			name: "hint does not outlive its app and returns with it",
			events: join(
				presenceApp(0, 9, "ide"), presenceApp(10, 14, "term"), presenceApp(15, 20, "ide"),
				[]event.Event{gitCtx, appHint(0, "ide", "", "WEB-7")},
			),
			want: []span{{"WEB-7", 0, 10}, {"ABC-1", 10, 15}, {"WEB-7", 15, 21}},
		},
		{
			name: "manual pin beats hint and git",
			events: join(
				presenceApp(0, 20, "ide"),
				[]event.Event{gitCtx, appHint(0, "ide", "", "WEB-7"), manual(0, "PIN-1")},
			),
			want: []span{{"PIN-1", 0, 21}},
		},
		{
			name: "cleared pin restores the hint, not the git context",
			events: join(
				presenceApp(0, 20, "ide"),
				[]event.Event{gitCtx, appHint(0, "ide", "", "WEB-7"), manual(0, "PIN-1"), manual(10, "")},
			),
			want: []span{{"PIN-1", 0, 10}, {"WEB-7", 10, 21}},
		},
		{
			name: "hint without app keeps latest-wins behaviour",
			events: join(
				presenceApp(0, 20, "term"),
				[]event.Event{gitCtx, appHint(5, "", "", "WEB-7"), ctx(12, "/r/a", "ABC-2-next")},
			),
			want: []span{{"ABC-1", 0, 5}, {"WEB-7", 5, 12}, {"ABC-2", 12, 21}},
		},
		{
			name: "repo-only hint for the git repo keeps the branch ticket",
			events: join(
				presenceApp(0, 20, "ide"),
				[]event.Event{gitCtx, appHint(5, "ide", "/r/a", "")},
			),
			want: []span{{"ABC-1", 0, 21}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocks := Derive(tt.events, nil, testConfig(), resolver.Rules{})
			if len(blocks) != len(tt.want) {
				t.Fatalf("blocks = %+v, want %d", blocks, len(tt.want))
			}
			for i, w := range tt.want {
				b := blocks[i]
				if b.Ticket != w.ticket || !b.Start.Equal(at(w.start)) || !b.End.Equal(at(w.end)) {
					t.Errorf("block %d = %s %v-%v, want %s %v-%v", i, b.Ticket, b.Start, b.End, w.ticket, at(w.start), at(w.end))
				}
			}
		})
	}
}
