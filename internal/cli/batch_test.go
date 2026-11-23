package cli

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/event"
)

// batchSink records every AppendBatch call and can fail it.
type batchSink struct {
	batches [][]event.Event
	err     error
}

func (b *batchSink) AppendBatch(_ context.Context, events []event.Event) error {
	b.batches = append(b.batches, events)
	return b.err
}

func (b *batchSink) Close() error { return nil }

func TestHookWritesBothEventsInOneBatch(t *testing.T) {
	sink := &batchSink{}
	deps, _ := newTestDeps(&fakeSink{}, "/work/repo\nmain\n", nil)
	deps.OpenStore = func() (eventSink, error) { return sink, nil }
	if err := runHook(context.Background(), deps, "/work/repo"); err != nil {
		t.Fatal(err)
	}
	if len(sink.batches) != 1 || len(sink.batches[0]) != 2 {
		t.Fatalf("batches = %+v, want one batch of the context and the presence event", sink.batches)
	}
}

func TestHookLogsAFailedBatchAndStaysSilent(t *testing.T) {
	sink := &batchSink{err: errors.New("database is locked")}
	deps, logs := newTestDeps(&fakeSink{}, "/work/repo\nmain\n", nil)
	deps.OpenStore = func() (eventSink, error) { return sink, nil }
	if err := runHook(context.Background(), deps, "/work/repo"); err != nil {
		t.Fatalf("hook must not fail the prompt: %v", err)
	}
	if logs.Len() == 0 {
		t.Error("failed batch was not logged")
	}
}

func TestScanStoresAPassInOneBatch(t *testing.T) {
	repoA, repoB := fakeRepoDir(t), fakeRepoDir(t)
	at := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
	scan := func(_ context.Context, repo string, _, _ time.Time, _ *regexp.Regexp) ([]event.Event, error) {
		return []event.Event{{At: at, Source: "git", Kind: event.KindContext, Repo: repo, Key: "k-" + repo}}, nil
	}
	tests := []struct {
		name        string
		sinkErr     error
		wantErr     bool
		wantBatches int
	}{
		{"all repositories in one transaction", nil, false, 1},
		{"a failing store aborts the pass once", errors.New("locked"), true, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sink := &batchSink{err: tt.sinkErr}
			report, err := scanRepositories(context.Background(), []string{repoA, repoB}, "/home/u", at.Add(-time.Hour), at.Add(time.Hour), nil, scan, nil, sink)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if len(sink.batches) != tt.wantBatches || len(sink.batches[0]) != 2 {
				t.Fatalf("batches = %+v, want %d batch with both repositories' events", sink.batches, tt.wantBatches)
			}
			if !tt.wantErr && report.Found != 2 {
				t.Errorf("found = %d, want 2", report.Found)
			}
		})
	}
}

func (b *batchSink) Append(ctx context.Context, e event.Event) error {
	return b.AppendBatch(ctx, []event.Event{e})
}
