package store

import (
	"context"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/event"
)

func TestCarriedContextReturnsLastManualAndLastAutomatic(t *testing.T) {
	sources := CarrySources{Manual: "manual", AppHint: "title"}
	contextEvent := func(offset time.Duration, source, repo, branch, ticket, app string) event.Event {
		return event.Event{At: base.Add(offset), Source: source, Kind: event.KindContext, Repo: repo, Branch: branch, Ticket: ticket, App: app}
	}
	tests := []struct {
		name   string
		events []event.Event
		// want lists "source/branch/ticket" of the carried events in order.
		want []string
	}{
		{name: "nothing before", events: []event.Event{contextEvent(time.Minute, "shell", "/r", "late", "", "")}},
		{
			name: "latest of each family in time order",
			events: []event.Event{
				contextEvent(-5*time.Hour, "manual", "", "", "OLD-1", ""),
				contextEvent(-4*time.Hour, "shell", "/r", "old", "", ""),
				contextEvent(-3*time.Hour, "manual", "", "", "PIN-1", ""),
				contextEvent(-2*time.Hour, "git", "/r", "new", "", ""),
			},
			want: []string{"manual//PIN-1", "git/new/"},
		},
		{
			name: "the boundary itself belongs to the day",
			events: []event.Event{
				contextEvent(-time.Hour, "shell", "/r", "before", "", ""),
				contextEvent(0, "shell", "/r", "at-start", "", ""),
				contextEvent(0, "manual", "", "", "PIN-2", ""),
			},
			want: []string{"shell/before/"},
		},
		{
			name: "a manual clear is returned so the caller sees the pin ended",
			events: []event.Event{
				contextEvent(-2*time.Hour, "manual", "", "", "PIN-1", ""),
				contextEvent(-time.Hour, "manual", "", "", "", ""),
			},
			want: []string{"manual//"},
		},
		{
			name: "signal-less and app-bound hints are not automatic context",
			events: []event.Event{
				contextEvent(-3*time.Hour, "shell", "/r", "branch", "", ""),
				contextEvent(-2*time.Hour, "title", "/other", "", "", "com.example.ide"),
				contextEvent(-time.Hour, "activitywatch", "", "", "", "com.example.app"),
			},
			want: []string{"shell/branch/"},
		},
		{
			name: "a hint without an app is ordinary context",
			events: []event.Event{
				contextEvent(-3*time.Hour, "shell", "/r", "branch", "", ""),
				contextEvent(-2*time.Hour, "title", "/other", "hinted", "", ""),
			},
			want: []string{"title/hinted/"},
		},
		{
			name: "presence and gaps are never carried",
			events: []event.Event{
				{At: base.Add(-time.Hour), Source: "presence", Kind: event.KindPresence, Repo: "/r"},
				{At: base.Add(-30 * time.Minute), Source: "presence", Kind: event.KindGap, IdleSeconds: 60},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := openTemp(t)
			ctx := context.Background()
			for _, e := range tt.events {
				if err := s.Append(ctx, e); err != nil {
					t.Fatal(err)
				}
			}
			got, err := s.CarriedContext(ctx, base, sources)
			if err != nil {
				t.Fatal(err)
			}
			var labels []string
			for _, e := range got {
				if e.Kind != event.KindContext || e.ID == 0 || !e.At.Before(base) {
					t.Errorf("unexpected carried event %+v", e)
				}
				labels = append(labels, e.Source+"/"+e.Branch+"/"+e.Ticket)
			}
			if len(labels) != len(tt.want) {
				t.Fatalf("carried = %v, want %v", labels, tt.want)
			}
			for i := range labels {
				if labels[i] != tt.want[i] {
					t.Fatalf("carried = %v, want %v", labels, tt.want)
				}
			}
		})
	}
}

func TestCarriedContextPrefersLaterInsertAtEqualTime(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	for _, branch := range []string{"first", "second"} {
		e := event.Event{At: base.Add(-time.Hour), Source: "shell", Kind: event.KindContext, Repo: "/r", Branch: branch}
		if err := s.Append(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.CarriedContext(ctx, base, CarrySources{Manual: "manual", AppHint: "title"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Branch != "second" {
		t.Fatalf("carried = %+v", got)
	}
}
