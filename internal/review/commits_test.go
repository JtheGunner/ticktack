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

func TestCommitRefsAssignsCommitsToTheTicketOfTheirBlock(t *testing.T) {
	t0 := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	hash := func(c string) string { return strings.Repeat(c, 40) }
	commit := func(h string, at time.Time) event.Event {
		return event.Event{At: at, Source: git.Source, Kind: event.KindContext, Repo: "/r",
			Key: fmt.Sprintf("git:/r:commit:%d:%s:main", at.Unix(), h)}
	}
	blocks := []sessionizer.Block{
		{Start: t0, End: t0.Add(time.Hour), Ticket: "ABC-1"},
		{Start: t0.Add(time.Hour), End: t0.Add(2 * time.Hour), Ticket: ""},
		{Start: t0.Add(2 * time.Hour), End: t0.Add(3 * time.Hour), Ticket: "ABC-2"},
	}
	events := []event.Event{
		commit(hash("2"), t0.Add(20*time.Minute)),
		commit(hash("1"), t0.Add(10*time.Minute)),
		commit(hash("1"), t0.Add(10*time.Minute)), // duplicate
		commit(hash("3"), t0.Add(90*time.Minute)), // unassigned block
		commit(hash("4"), t0.Add(150*time.Minute)),
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
