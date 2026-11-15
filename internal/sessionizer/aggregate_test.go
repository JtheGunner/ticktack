package sessionizer

import (
	"testing"
	"time"

	"cloud.google.com/go/civil"
)

func blockAt(start time.Time, d time.Duration, ticket string) Block {
	return Block{Start: start, End: start.Add(d), Ticket: ticket}
}

func rounding(step, cap time.Duration) RoundingConfig {
	return RoundingConfig{Rounding: step, DailyCap: cap, DefaultType: "Development"}
}

func mustZurich(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Zurich")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	return loc
}

func TestAggregateSplitsAtMidnight(t *testing.T) {
	start := time.Date(2026, 3, 2, 23, 30, 0, 0, time.UTC)
	items := Aggregate([]Block{blockAt(start, time.Hour, "AAA-1")}, rounding(time.Minute, 0), time.UTC)
	if len(items) != 2 {
		t.Fatalf("got %+v", items)
	}
	if items[0].Date != (civil.Date{Year: 2026, Month: 3, Day: 2}) || items[0].Minutes != 30 ||
		items[1].Date != (civil.Date{Year: 2026, Month: 3, Day: 3}) || items[1].Minutes != 30 {
		t.Fatalf("got %+v", items)
	}
	if items[0].Type != "Development" || items[0].Description != "AAA-1 — 1 block" {
		t.Fatalf("got %+v", items[0])
	}
}

func TestAggregateAcrossDST(t *testing.T) {
	loc := mustZurich(t)
	step := rounding(time.Minute, 0)
	// Spring forward 2026-03-29: the local day has 23 hours.
	springStart := time.Date(2026, 3, 29, 0, 0, 0, 0, loc)
	spring := Aggregate([]Block{{Start: springStart, End: springStart.Add(30 * time.Hour), Ticket: "A-1"}}, step, loc)
	if len(spring) != 2 || spring[0].Minutes != 23*60 || spring[1].Minutes != 7*60 {
		t.Fatalf("spring: %+v", spring)
	}
	// Fall back 2026-10-25: the local day has 25 hours.
	fallStart := time.Date(2026, 10, 25, 0, 0, 0, 0, loc)
	fall := Aggregate([]Block{{Start: fallStart, End: fallStart.Add(30 * time.Hour), Ticket: "A-1"}}, step, loc)
	if len(fall) != 2 || fall[0].Minutes != 25*60 || fall[1].Minutes != 5*60 {
		t.Fatalf("fall: %+v", fall)
	}
}

func TestAggregateRoundsTotalNotBlocks(t *testing.T) {
	// Three 5-minute blocks of A (15 min total) must give 15, not 3x0.
	var blocks []Block
	for i := 0; i < 3; i++ {
		blocks = append(blocks, blockAt(base.Add(time.Duration(i)*time.Hour), 5*time.Minute, "AAA-1"))
	}
	items := Aggregate(blocks, rounding(15*time.Minute, 0), time.UTC)
	if len(items) != 1 || items[0].Minutes != 15 || items[0].RawMinutes != 15 || items[0].Description != "AAA-1 — 3 blocks" {
		t.Fatalf("got %+v", items)
	}

	// 20 + 20 + 20 = 60 -> 60; floors 15+15+15 = 45, the three equal
	// remainders hand out 15 more to the first ticket in order.
	mixed := []Block{
		blockAt(base, 20*time.Minute, "AAA-1"),
		blockAt(base.Add(time.Hour), 20*time.Minute, "BBB-2"),
		blockAt(base.Add(2*time.Hour), 20*time.Minute, "CCC-3"),
	}
	items = Aggregate(mixed, rounding(15*time.Minute, 0), time.UTC)
	sum := 0
	for _, it := range items {
		sum += it.Minutes
	}
	if sum != 60 || items[0].Minutes != 30 || items[1].Minutes != 15 || items[2].Minutes != 15 {
		t.Fatalf("largest remainder failed: %+v", items)
	}
}

func TestAggregateLargestRemainderPrefersBiggerRemainder(t *testing.T) {
	// 22 + 16 = 38 -> 45; floors 15+15, remainders 7 and 1, so AAA-1 gets the extra unit.
	blocks := []Block{blockAt(base, 22*time.Minute, "AAA-1"), blockAt(base.Add(time.Hour), 16*time.Minute, "BBB-2")}
	items := Aggregate(blocks, rounding(15*time.Minute, 0), time.UTC)
	if len(items) != 2 || items[0].Minutes != 30 || items[1].Minutes != 15 {
		t.Fatalf("got %+v", items)
	}
}

func TestAggregateFlagsDailyCapOverflow(t *testing.T) {
	long := []Block{blockAt(base, 7*time.Hour, "AAA-1"), blockAt(base.Add(8*time.Hour), 4*time.Hour, "BBB-2")}
	items := Aggregate(long, rounding(15*time.Minute, 10*time.Hour), time.UTC)
	if len(items) != 2 || !items[0].OverCap || !items[1].OverCap {
		t.Fatalf("expected both items flagged: %+v", items)
	}
	if items[0].Minutes != 7*60 || items[1].Minutes != 4*60 {
		t.Fatalf("time was truncated: %+v", items)
	}
	short := Aggregate(long[:1], rounding(15*time.Minute, 10*time.Hour), time.UTC)
	if short[0].OverCap {
		t.Fatalf("under cap flagged: %+v", short)
	}
}

func TestAggregateKeepsUnknownTicket(t *testing.T) {
	items := Aggregate([]Block{blockAt(base, 30*time.Minute, "")}, rounding(15*time.Minute, 0), time.UTC)
	if len(items) != 1 || items[0].Ticket != "" || items[0].Minutes != 30 || items[0].Description != "unassigned — 1 block" {
		t.Fatalf("got %+v", items)
	}
}
