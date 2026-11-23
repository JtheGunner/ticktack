package git

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/pause"
)

type countingSink struct{ batches int }

func (s *countingSink) Append(context.Context, event.Event) error { s.batches++; return nil }
func (s *countingSink) AppendBatch(context.Context, []event.Event) error {
	s.batches++
	return nil
}

// A pass cancelled in the middle reports the repositories it had skipped or
// scanned so far, and stores nothing.
func TestCancelledPassStillReportsWhatItSkipped(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	now := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
	c := &Collector{
		Repos: []string{"missing", "first", "second"},
		Resolve: func(entry string) (string, string) {
			if entry == "missing" {
				return "", "not a directory"
			}
			return entry, ""
		},
		ScanRepo: func(_ context.Context, repo string, _, _ time.Time, _ *regexp.Regexp) ([]event.Event, error) {
			if repo == "first" {
				cancel()
			}
			return []event.Event{{At: now, Kind: event.KindContext, Key: repo}}, nil
		},
		Describe:    func(error, bool) string { return "failed" },
		Window:      func(time.Time) (time.Time, time.Time) { return now.Add(-time.Hour), now },
		Now:         func() time.Time { return now },
		LoadPauses:  func(time.Time) (pause.Intervals, error) { return nil, nil },
		RepoTimeout: time.Minute,
	}
	var got Report
	var gotErr error
	calls := 0
	c.OnReport = func(r Report, err error) { got, gotErr = r, err; calls++ }
	sink := &countingSink{}

	err := c.Run(ctx, sink)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if calls != 1 || !errors.Is(gotErr, context.Canceled) {
		t.Fatalf("OnReport called %d times with %v, want once with the cancellation", calls, gotErr)
	}
	if len(got.Skipped) != 1 || got.Skipped[0].Entry != "missing" || got.Scanned != 1 || got.Total != 3 {
		t.Fatalf("report = %+v, want the skipped entry and the one scanned repository", got)
	}
	if sink.batches != 0 {
		t.Errorf("a cancelled pass stored %d batches", sink.batches)
	}
}

func TestPassCancelledInsideARepoScanReportsToo(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	now := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
	c := &Collector{
		Repos: []string{"missing", "only"},
		Resolve: func(e string) (string, string) {
			if e == "missing" {
				return "", "gone"
			}
			return e, ""
		},
		ScanRepo: func(context.Context, string, time.Time, time.Time, *regexp.Regexp) ([]event.Event, error) {
			cancel()
			return nil, context.Canceled
		},
		Describe:    func(error, bool) string { return "failed" },
		Window:      func(time.Time) (time.Time, time.Time) { return now, now },
		Now:         func() time.Time { return now },
		LoadPauses:  func(time.Time) (pause.Intervals, error) { return nil, nil },
		RepoTimeout: time.Minute,
	}
	var got Report
	calls := 0
	c.OnReport = func(r Report, _ error) { got = r; calls++ }

	if err := c.Run(ctx, &countingSink{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if calls != 1 || len(got.Skipped) != 1 {
		t.Fatalf("calls = %d, report = %+v", calls, got)
	}
}
