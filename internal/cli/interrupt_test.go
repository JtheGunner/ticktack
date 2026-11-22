package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/sink"
)

func TestExitCodeOfACancelledRun(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"cancelled", context.Canceled, ExitInterrupted},
		{"wrapped cancellation", fmt.Errorf("undo: %w", context.Canceled), ExitInterrupted},
		{"joined cancellations", errors.Join(context.Canceled, context.Canceled), ExitInterrupted},
		{"failure plus cancellation", errors.Join(errors.New("abc-1"), context.Canceled), 1},
		{"booking run active and cancelled", errors.Join(ErrBookingRunActive, context.Canceled), ExitBookingRunActive},
		{"booking run active", ErrBookingRunActive, ExitBookingRunActive},
		{"other failure", errors.New("boom"), 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExitCode(tt.err); got != tt.want {
				t.Fatalf("ExitCode = %d, want %d", got, tt.want)
			}
		})
	}
	if ExitInterrupted != 130 || ExitBookingRunActive != 75 {
		t.Fatal("documented exit codes changed")
	}
}

func TestSyncWithCancelledContextSendsNothingAndReportsNotSent(t *testing.T) {
	deps, rec := syncTestDeps(t)
	confirmDay(t, deps)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The signal arrives once the reads are done and booking is about to start.
	newSink := deps.NewSink
	deps.NewSink = func(cfg config.Config, path string) (sink.TimeSink, error) {
		cancel()
		return newSink(cfg, path)
	}

	cmd := newSyncCommandWith(deps)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--day", "2026-01-05"})
	err := cmd.ExecuteContext(ctx)

	if !errors.Is(err, context.Canceled) || ExitCode(err) != ExitInterrupted {
		t.Fatalf("err = %v, exit code %d", err, ExitCode(err))
	}
	if !strings.Contains(out.String(), "not-sent") || !strings.Contains(out.String(), "not sent 1") {
		t.Fatalf("items not reported as not sent:\n%s", out.String())
	}
	if len(rec.items) != 0 {
		t.Fatalf("a cancelled run booked %d items", len(rec.items))
	}
}
