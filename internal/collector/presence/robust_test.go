package presence

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/event"
)

func TestAgentStoresSampleWithoutBundleID(t *testing.T) {
	h := newHarness()
	h.fake.Push(Sample{Idle: 7 * time.Second})
	h.tick(0)
	want := event.Event{Source: "presence", Kind: event.KindPresence, App: "", IdleSeconds: 7, At: t0}
	if len(h.sink.events) != 1 || h.sink.events[0] != want {
		t.Fatalf("got %+v, want %+v", h.sink.events, want)
	}
	if len(h.logs) != 0 {
		t.Fatalf("an empty bundle ID is not an error, logged %q", h.logs)
	}
}

func TestAgentSampleTimeoutIsLoggedAndAgentContinues(t *testing.T) {
	h := newHarness()
	h.agent.SampleTimeout = 20 * time.Millisecond
	h.fake.Push(Sample{Block: true})
	h.fake.Push(Sample{Idle: time.Second, App: "a"})

	done := make(chan struct{})
	go func() {
		defer close(done)
		h.tick(0)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a hanging sampler blocked the agent")
	}
	if len(h.sink.events) != 0 {
		t.Fatalf("timed-out sample was stored: %+v", h.sink.events)
	}
	if len(h.logs) != 1 || !strings.Contains(h.logs[0], "timed out") {
		t.Fatalf("timeout not logged as such: %q", h.logs)
	}

	h.tick(30 * time.Second)
	if len(h.sink.events) != 1 || h.sink.events[0].App != "a" {
		t.Fatalf("agent did not keep running after the timeout: %+v", h.sink.events)
	}
}

func TestAgentSampleTimeoutDefault(t *testing.T) {
	h := newHarness()
	var deadline time.Time
	h.agent.Sampler = samplerFunc(func(ctx context.Context) (time.Duration, string, error) {
		deadline, _ = ctx.Deadline()
		return 0, "a", nil
	})
	before := time.Now()
	h.tick(0)
	if deadline.IsZero() {
		t.Fatal("sampler was called without a deadline")
	}
	if got := deadline.Sub(before); got < DefaultSampleTimeout-time.Second || got > DefaultSampleTimeout+time.Second {
		t.Fatalf("deadline %v away, want about %v", got, DefaultSampleTimeout)
	}
}

type samplerFunc func(ctx context.Context) (time.Duration, string, error)

func (f samplerFunc) Sample(ctx context.Context) (time.Duration, string, error) { return f(ctx) }

const (
	ioregOutput = `    | |   "HIDIdleTime" = 4000000000` + "\n"
	frontOutput = "ASN:0x0-0x1b01b:\n"
	infoOutput  = `"CFBundleIdentifier"="com.apple.Terminal"` + "\n"
)

// scriptedRunner answers the three sampler commands from canned results.
type scriptedRunner struct {
	ioreg, front, info string
	ioregErr, frontErr error
	hangInfo           bool
}

func (r *scriptedRunner) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	call := strings.Join(append([]string{name}, args...), " ")
	switch {
	case name == "ioreg":
		return []byte(r.ioreg), r.ioregErr
	case strings.HasPrefix(call, "lsappinfo front"):
		return []byte(r.front), r.frontErr
	case strings.HasPrefix(call, "lsappinfo info"):
		if r.hangInfo {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return []byte(r.info), nil
	}
	return nil, fmt.Errorf("unexpected command %q", call)
}

func TestCommandSampler(t *testing.T) {
	tests := []struct {
		name     string
		runner   scriptedRunner
		wantIdle time.Duration
		wantApp  string
		wantErr  bool
	}{
		{
			name:     "idle and bundle ID",
			runner:   scriptedRunner{ioreg: ioregOutput, front: frontOutput, info: infoOutput},
			wantIdle: 4 * time.Second, wantApp: "com.apple.Terminal",
		},
		{
			name:     "no frontmost application",
			runner:   scriptedRunner{ioreg: ioregOutput, front: "\n"},
			wantIdle: 4 * time.Second,
		},
		{
			name:     "lsappinfo front fails",
			runner:   scriptedRunner{ioreg: ioregOutput, frontErr: errors.New("exit status 1")},
			wantIdle: 4 * time.Second,
		},
		{
			name:     "application without bundle ID",
			runner:   scriptedRunner{ioreg: ioregOutput, front: frontOutput, info: `"CFBundleIdentifier"=[ NULL ]`},
			wantIdle: 4 * time.Second,
		},
		{
			name:    "ioreg fails",
			runner:  scriptedRunner{ioregErr: errors.New("exit status 1")},
			wantErr: true,
		},
		{
			name:    "idle time missing",
			runner:  scriptedRunner{ioreg: "no idle here", front: frontOutput, info: infoOutput},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := tt.runner
			idle, app, err := commandSampler{run: runner.run}.Sample(context.Background())
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if idle != tt.wantIdle || app != tt.wantApp {
				t.Fatalf("got idle=%v app=%q, want idle=%v app=%q", idle, app, tt.wantIdle, tt.wantApp)
			}
		})
	}
}

func TestCommandSamplerHangingAppLookupYieldsEmptyApp(t *testing.T) {
	runner := scriptedRunner{ioreg: ioregOutput, front: frontOutput, hangInfo: true}
	sampler := commandSampler{run: runner.run, appLookupTimeout: 20 * time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	idle, app, err := sampler.Sample(ctx)
	if err != nil {
		t.Fatalf("a hanging app lookup must not drop the sample: %v", err)
	}
	if idle != 4*time.Second || app != "" {
		t.Fatalf("got idle=%v app=%q, want 4s and an empty app", idle, app)
	}
}

func TestCommandSamplerReportsExpiredCallDeadline(t *testing.T) {
	runner := scriptedRunner{ioreg: ioregOutput, front: frontOutput, hangInfo: true}
	sampler := commandSampler{run: runner.run, appLookupTimeout: time.Minute}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, _, err := sampler.Sample(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the expired call deadline", err)
	}
}
