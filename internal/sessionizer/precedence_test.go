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
		out = append(out, event.Event{At: at(m), Kind: event.KindPresence, App: app})
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
			want: []span{{"ABC-1", 0, 5}, {"WEB-7", 5, 10}, {"ABC-1", 10, 20}},
		},
		{
			name: "hint is ignored when its app is not frontmost",
			events: join(
				presenceApp(0, 20, "term"),
				[]event.Event{gitCtx, appHint(5, "ide", "", "WEB-7")},
			),
			want: []span{{"ABC-1", 0, 20}},
		},
		{
			name: "hint does not outlive its app and returns with it",
			events: join(
				presenceApp(0, 9, "ide"), presenceApp(10, 14, "term"), presenceApp(15, 20, "ide"),
				[]event.Event{gitCtx, appHint(0, "ide", "", "WEB-7")},
			),
			want: []span{{"WEB-7", 0, 10}, {"ABC-1", 10, 15}, {"WEB-7", 15, 20}},
		},
		{
			name: "manual pin beats hint and git",
			events: join(
				presenceApp(0, 20, "ide"),
				[]event.Event{gitCtx, appHint(0, "ide", "", "WEB-7"), manual(0, "PIN-1")},
			),
			want: []span{{"PIN-1", 0, 20}},
		},
		{
			name: "cleared pin restores the hint, not the git context",
			events: join(
				presenceApp(0, 20, "ide"),
				[]event.Event{gitCtx, appHint(0, "ide", "", "WEB-7"), manual(0, "PIN-1"), manual(10, "")},
			),
			want: []span{{"PIN-1", 0, 10}, {"WEB-7", 10, 20}},
		},
		{
			name: "hint without app keeps latest-wins behaviour",
			events: join(
				presenceApp(0, 20, "term"),
				[]event.Event{gitCtx, appHint(5, "", "", "WEB-7"), ctx(12, "/r/a", "ABC-2-next")},
			),
			want: []span{{"ABC-1", 0, 5}, {"WEB-7", 5, 12}, {"ABC-2", 12, 20}},
		},
		{
			name: "repo-only hint for the git repo keeps the branch ticket",
			events: join(
				presenceApp(0, 20, "ide"),
				[]event.Event{gitCtx, appHint(5, "ide", "/r/a", "")},
			),
			want: []span{{"ABC-1", 0, 20}},
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

func TestPrecedenceEdgeCases(t *testing.T) {
	gitCtx := ctx(0, "/r/a", "ABC-1-work")
	withID := func(e event.Event, id int64) event.Event { e.ID = id; return e }
	tests := []struct {
		name   string
		events []event.Event
		want   []span
	}{
		{
			name: "a sleep gap does not change the frontmost app; the app after it decides",
			events: join(
				presenceApp(0, 9, "ide"),
				[]event.Event{{At: at(15), Kind: event.KindGap, IdleSeconds: 300}},
				presenceApp(15, 20, "term"),
				[]event.Event{gitCtx, appHint(0, "ide", "", "WEB-7")},
			),
			want: []span{{"WEB-7", 0, 9}, {"ABC-1", 15, 20}},
		},
		{
			name: "hint and git at the same time: the hint wins whatever the ID order",
			events: join(
				presenceApp(0, 20, "ide"),
				[]event.Event{withID(appHint(5, "ide", "", "WEB-7"), 1), withID(ctx(5, "/r/a", "ABC-2-next"), 2), withID(gitCtx, 0)},
			),
			want: []span{{"ABC-1", 0, 5}, {"WEB-7", 5, 20}},
		},
		{
			name: "hint and git at the same time, reversed IDs",
			events: join(
				presenceApp(0, 20, "ide"),
				[]event.Event{withID(appHint(5, "ide", "", "WEB-7"), 2), withID(ctx(5, "/r/a", "ABC-2-next"), 1), withID(gitCtx, 0)},
			),
			want: []span{{"ABC-1", 0, 5}, {"WEB-7", 5, 20}},
		},
		{
			name: "hint and the presence sample that brings its app to the front share a timestamp",
			events: join(
				presenceApp(0, 4, "term"), presenceApp(6, 20, "ide"),
				[]event.Event{withID(gitCtx, 0), withID(appHint(5, "ide", "", "WEB-7"), 1), withID(event.Event{At: at(5), Kind: event.KindPresence, App: "ide"}, 2)},
			),
			want: []span{{"ABC-1", 0, 5}, {"WEB-7", 5, 20}},
		},
		{
			name: "a presence sample without an app neither clears nor changes the frontmost app",
			events: join(
				presenceApp(0, 4, "ide"), presenceApp(5, 9, ""), presenceApp(10, 20, "ide"),
				[]event.Event{gitCtx, appHint(0, "ide", "", "WEB-7")},
			),
			want: []span{{"WEB-7", 0, 20}},
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

func TestLatestContextUnderAnActiveHint(t *testing.T) {
	gitCtx := ctx(0, "/r/a", "ABC-1-work")
	hint := appHint(2, "ide", "", "WEB-7")
	tests := []struct {
		name       string
		front      string
		wantTicket string
		wantSignal resolver.ContextSignal
	}{
		{"hint app frontmost", "ide", "WEB-7", resolver.ContextSignal{Ticket: "WEB-7"}},
		{"other app frontmost", "term", "ABC-1", resolver.ContextSignal{Repo: "/r/a", Branch: "ABC-1-work"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := join(presenceApp(0, 5, tt.front), []event.Event{gitCtx, hint})
			cur, ok := LatestContext(events, resolver.Rules{})
			if !ok {
				t.Fatal("no context")
			}
			if cur.Attribution.Ticket != tt.wantTicket || cur.Manual || cur.Signal != tt.wantSignal {
				t.Errorf("latest = %+v, want ticket %s signal %+v", cur, tt.wantTicket, tt.wantSignal)
			}
		})
	}

	pinned := join(presenceApp(0, 5, "ide"), []event.Event{gitCtx, hint, manual(3, "PIN-1")})
	cur, _ := LatestContext(pinned, resolver.Rules{})
	if cur.Attribution.Ticket != "PIN-1" || !cur.Manual || cur.Signal.Ticket != "WEB-7" {
		t.Errorf("pinned latest = %+v, want pin on top and the hint as automatic signal", cur)
	}
}
