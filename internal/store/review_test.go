package store

import (
	"context"
	"testing"
	"time"
)

func TestConfirmationsRoundTripAndReplace(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	c := Confirmation{Day: "2026-01-06", Issue: "ABC-1", Type: "Development", Minutes: 30, ConfirmedAt: base}
	if err := s.Confirm(ctx, c); err != nil {
		t.Fatal(err)
	}
	c.Minutes = 45
	if err := s.Confirm(ctx, c); err != nil {
		t.Fatal(err)
	}
	other := Confirmation{Day: "2026-01-07", Issue: "ABC-1", Type: "Development", Minutes: 5, ConfirmedAt: base}
	if err := s.Confirm(ctx, other); err != nil {
		t.Fatal(err)
	}
	got, err := s.Confirmations(ctx, "2026-01-06")
	if err != nil || len(got) != 1 || got[0].Minutes != 45 || !got[0].ConfirmedAt.Equal(base) {
		t.Fatalf("confirmations = %+v, %v", got, err)
	}
}

func TestCapAcknowledgementRoundTrip(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	if _, ok, err := s.CapAcknowledgementFor(ctx, "2026-01-06"); err != nil || ok {
		t.Fatalf("unexpected acknowledgement: ok=%v err=%v", ok, err)
	}
	if err := s.AcknowledgeCap(ctx, CapAcknowledgement{Day: "2026-01-06", TotalMinutes: 700, AcknowledgedAt: base}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.CapAcknowledgementFor(ctx, "2026-01-06")
	if err != nil || !ok || got.TotalMinutes != 700 {
		t.Fatalf("ack = %+v ok=%v err=%v", got, ok, err)
	}
}

func TestConfirmedDaysListsEachDayOnceInOrder(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	at := time.Date(2026, 1, 5, 20, 0, 0, 0, time.UTC)
	for _, c := range []Confirmation{
		{Day: "2026-01-05", Issue: "ABC-1", Type: "Development", Minutes: 60, ConfirmedAt: at},
		{Day: "2026-01-05", Issue: "ABC-2", Type: "Development", Minutes: 30, ConfirmedAt: at},
		{Day: "2026-01-03", Issue: "ABC-1", Type: "Development", Minutes: 15, ConfirmedAt: at},
	} {
		if err := s.Confirm(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ConfirmedDays(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "2026-01-03" || got[1] != "2026-01-05" {
		t.Fatalf("days = %v", got)
	}
}
