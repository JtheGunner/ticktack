package cli

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"cloud.google.com/go/civil"

	"github.com/JtheGunner/ticktack/internal/config"
)

const (
	// autopilotInterval is how often the agent runs the autopilot. It is fixed.
	autopilotInterval = 30 * time.Minute
	// autopilotStartDelay is the wait before the first run after the agent
	// starts, so the agent has recorded presence before it books anything.
	autopilotStartDelay = time.Minute
)

// loadAutopilotConfig reads the configuration file anew. The agent calls it
// before every run, so a change of [auto] takes effect without a restart. A
// missing file is the default configuration, in which the autopilot is off.
func loadAutopilotConfig() (config.Config, string, error) {
	path, err := config.DefaultPath()
	if err != nil {
		return config.Config{}, "", err
	}
	cfg, err := config.LoadOrDefault(path)
	return cfg, path, err
}

// agentAutopilotDeps returns the dependencies of the autopilot job in the
// agent: the same store, YouTrack sink and booking lock as the commands.
func agentAutopilotDeps() syncDeps {
	return syncDeps{
		LoadConfig:  loadAutopilotConfig,
		OpenStore:   openLedgerStore,
		NewSink:     newYouTrackSink,
		Now:         func() time.Time { return time.Now().Round(0) },
		AcquireLock: acquireBookingLock,
	}
}

// newAutopilotJob builds the autopilot job of the agent: the first run after
// startDelay, then one every interval. Each run starts with the checks that
// keep it harmless:
//   - While paused reports true it does nothing (the pause check is the
//     fail-closed one of the other jobs).
//   - It loads the configuration anew; a configuration that cannot be loaded
//     skips the run with a log line, and unless the autopilot is active
//     (enabled and mode "autopilot") the run does nothing at all, with no lock
//     and no log line.
//   - It holds the booking lock, so it never runs beside a sync or an undo;
//     while another run holds it, the run is skipped until the next interval.
//
// A failure is logged once per run, with context and without the token, and
// never stops the agent. The next run retries; the idempotent ledger keeps a
// retry from booking anything twice.
func newAutopilotJob(deps syncDeps, paused func() bool, startDelay, interval time.Duration, logf func(format string, args ...any)) backgroundJob {
	return func(ctx context.Context) {
		once := func() {
			if paused() {
				return
			}
			cfg, cfgPath, err := deps.LoadConfig()
			if err != nil {
				logf(msgAutopilotLogConfig, err)
				return
			}
			if !cfg.Auto.Autopilot() {
				return
			}
			release, err := deps.lockBookingRun(false)
			if errors.Is(err, ErrBookingRunActive) {
				logf(msgAutopilotLogBusy)
				return
			}
			if err != nil {
				logf(msgAutopilotLogLock, err)
				return
			}
			defer release()
			logHeld := func(days []string) { logf(msgAutopilotLogHeld, strings.Join(days, ", ")) }
			if err := autopilotRun(ctx, deps, cfg, cfgPath, logHeld); err != nil && ctx.Err() == nil {
				logf(msgAutopilotLogFail, err)
			}
		}
		timer := time.NewTimer(startDelay)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				once()
				timer.Reset(interval)
			}
		}
	}
}

// autopilotRun opens the store and runs one autopilot pass over the lookback
// window, with no output. The booking lock is held by the caller.
func autopilotRun(ctx context.Context, deps syncDeps, cfg config.Config, cfgPath string, onHeld func(days []string)) error {
	st, err := deps.OpenStore()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	loc := cfg.Location
	if loc == nil {
		loc = time.Local
	}
	return runAutopilotPass(ctx, io.Discard, deps, cfg, cfgPath, st, civil.DateOf(deps.Now().In(loc)), false, onHeld)
}
