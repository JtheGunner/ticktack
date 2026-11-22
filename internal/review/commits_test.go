package review

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/collector/git"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
)

func commitEvent(repo, ticket, h string, at time.Time) event.Event {
	return event.Event{At: at, Source: git.Source, Kind: event.KindContext, Repo: repo, Ticket: ticket,
		Key: fmt.Sprintf("git:%s:commit:%d:%s:main", repo, at.Unix(), h)}
}

func TestCommitRefsAssignsCommitsToTheTicketOfTheirBlock(t *testing.T) {
	t0 := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	hash := func(c string) string { return strings.Repeat(c, 40) }
	commit := func(h string, at time.Time) event.Event { return commitEvent("/r", "ABC-1", h, at) }
	blocks := []sessionizer.Block{
		{Start: t0, End: t0.Add(time.Hour), Ticket: "ABC-1"},
		{Start: t0.Add(time.Hour), End: t0.Add(2 * time.Hour), Ticket: ""},
		{Start: t0.Add(2 * time.Hour), End: t0.Add(3 * time.Hour), Ticket: "ABC-2"},
	}
	four := commitEvent("/r", "ABC-2", hash("4"), t0.Add(150*time.Minute))
	events := []event.Event{
		commit(hash("2"), t0.Add(20*time.Minute)),
		commit(hash("1"), t0.Add(10*time.Minute)),
		commit(hash("1"), t0.Add(10*time.Minute)), // duplicate
		commit(hash("3"), t0.Add(90*time.Minute)), // unassigned block
		four,
		commit(hash("5"), t0.Add(5*time.Hour)), // outside every block
		commit(hash("6"), t0.Add(-time.Hour)),  // before the day
		{At: t0.Add(15 * time.Minute), Source: "shell", Kind: event.KindContext, Repo: "/r", Key: "x"},
	}

	got := commitRefs(events, blocks, t0, t0.Add(24*time.Hour))

	one := got["ABC-1"]
	if len(one) != 2 || one[0].Hash != hash("1") || one[1].Hash != hash("2") || one[0].Repo != "/r" {
		t.Errorf("ABC-1 commits = %+v, want 1 then 2 in time order", one)
	}
	if two := got["ABC-2"]; len(two) != 1 || two[0].Hash != hash("4") {
		t.Errorf("ABC-2 commits = %+v", two)
	}
	if len(got) != 2 {
		t.Errorf("tickets = %v, want only ABC-1 and ABC-2", got)
	}
}

func TestCommitRefsRequiresTheCommitsOwnTicketToMatch(t *testing.T) {
	t0 := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	h := func(c string) string { return strings.Repeat(c, 40) }
	at := t0.Add(10 * time.Minute)
	// One block of ABC-1, which may be a pin, a fold or a reassign: the block
	// does not say where the commit was made.
	blocks := []sessionizer.Block{{Start: t0, End: t0.Add(time.Hour), Ticket: "ABC-1"}}

	tests := []struct {
		name   string
		events []event.Event
		want   []string
	}{
		{"own ticket equals block ticket", []event.Event{commitEvent("/r", "ABC-1", h("1"), at)}, []string{h("1")}},
		{"own ticket differs", []event.Event{commitEvent("/private", "XYZ-9", h("1"), at)}, nil},
		{"own ticket empty", []event.Event{commitEvent("/private", "", h("1"), at)}, nil},
		{"own ticket in another case", []event.Event{commitEvent("/r", "abc-1", h("1"), at)}, []string{h("1")}},
		{"own ticket with spaces", []event.Event{commitEvent("/r", " ABC-1 ", h("1"), at)}, []string{h("1")}},
		{"only the matching commit of two repos", []event.Event{
			commitEvent("/private", "XYZ-9", h("1"), at),
			commitEvent("/work", "ABC-1", h("2"), at.Add(time.Minute)),
		}, []string{h("2")}},
		{"duplicate hash counted once", []event.Event{
			commitEvent("/work", "ABC-1", h("1"), at),
			commitEvent("/work", "ABC-1", h("1"), at),
		}, []string{h("1")}},
		{"same hash in two repos stays apart", []event.Event{
			commitEvent("/a", "ABC-1", h("1"), at),
			commitEvent("/b", "ABC-1", h("1"), at.Add(time.Minute)),
		}, []string{h("1"), h("1")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, r := range commitRefs(tt.events, blocks, t0, t0.Add(24*time.Hour))["ABC-1"] {
				got = append(got, r.Hash)
			}
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Errorf("hashes = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCommitRefsIgnoresCommitsInPinnedTimeOfAnotherTicket(t *testing.T) {
	t0 := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	blocks := []sessionizer.Block{{Start: t0, End: t0.Add(time.Hour), Ticket: "CUST-1"}}
	events := []event.Event{commitEvent("/private/diary", "HOME-3", strings.Repeat("a", 40), t0.Add(time.Minute))}

	if got := commitRefs(events, blocks, t0, t0.Add(24*time.Hour)); len(got) != 0 {
		t.Errorf("commit of another ticket leaked into %v", got)
	}
}
