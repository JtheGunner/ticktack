// Package presence samples whether the user is at the machine and which
// application is frontmost, and appends presence events to the store.
//
// Only the bundle identifier of the frontmost app is ever recorded, never a
// window title.
package presence

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Sampler measures the current presence signals.
type Sampler interface {
	// Sample returns the time since the last keyboard or mouse input and the
	// bundle identifier of the frontmost application.
	Sample(ctx context.Context) (idle time.Duration, app string, err error)
}

// defaultAppLookupTimeout bounds the frontmost-app lookup inside one sample.
// It is shorter than DefaultSampleTimeout so a hanging lookup still leaves a
// sample with an empty app instead of dropping it.
const defaultAppLookupTimeout = 2 * time.Second

// commandRunner runs one external command and returns its standard output.
type commandRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

// commandSampler reads idle time via ioreg and the frontmost app via
// lsappinfo. Neither requires any macOS permission.
type commandSampler struct {
	run commandRunner
	// appLookupTimeout overrides defaultAppLookupTimeout when positive.
	appLookupTimeout time.Duration
}

// Sample implements Sampler. The idle time is mandatory. The bundle
// identifier is not: presence is the signal that carries time, so a sample
// whose frontmost app cannot be determined is returned with an empty app
// rather than dropped. That also holds when the deadline of ctx expires during
// the app lookup; only a cancelled ctx fails the sample at that point.
func (s commandSampler) Sample(ctx context.Context) (time.Duration, string, error) {
	out, err := s.run(ctx, "ioreg", "-c", "IOHIDSystem")
	if err != nil {
		return 0, "", fmt.Errorf("run ioreg: %w", err)
	}
	idle, err := ParseIdle(string(out))
	if err != nil {
		return 0, "", err
	}
	app := s.frontmostApp(ctx)
	if err := ctx.Err(); errors.Is(err, context.Canceled) {
		// The caller gave up (shutdown): there is no one left to store the
		// sample.
		return 0, "", fmt.Errorf("look up frontmost app: %w", err)
	}
	// An expired call deadline is not an error here: the idle time is already
	// read, so the sample is kept with whatever app the lookup produced.
	return idle, app, nil
}

// frontmostApp returns the bundle identifier of the frontmost application, or
// "" when there is none or it cannot be read (no GUI session, an unbundled
// binary, a failing or hanging lsappinfo).
func (s commandSampler) frontmostApp(ctx context.Context) string {
	timeout := s.appLookupTimeout
	if timeout <= 0 {
		timeout = defaultAppLookupTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	front, err := s.run(ctx, "lsappinfo", "front")
	if err != nil {
		return ""
	}
	asn := strings.TrimSpace(string(front))
	if asn == "" {
		return ""
	}
	info, err := s.run(ctx, "lsappinfo", "info", "-only", "bundleid", asn)
	if err != nil {
		return ""
	}
	app, err := ParseBundleID(string(info))
	if err != nil {
		return ""
	}
	return app
}

// Sample is one scripted Fake result.
type Sample struct {
	Idle time.Duration
	App  string
	Err  error
	// Block makes the call hang until its context ends, like a subprocess
	// that never returns; it then yields the context error.
	Block bool
}

// Fake is a scripted Sampler for tests. Queued results are returned in order;
// once the queue is empty Sample returns an error.
type Fake struct {
	mu    sync.Mutex
	queue []Sample
	calls int
}

// Push queues a successful result.
func (f *Fake) Push(s Sample) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queue = append(f.queue, s)
}

// PushError queues a failing result.
func (f *Fake) PushError(err error) { f.Push(Sample{Err: err}) }

// Calls returns how often Sample was invoked.
func (f *Fake) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// Sample implements Sampler.
func (f *Fake) Sample(ctx context.Context) (time.Duration, string, error) {
	s, ok := f.next()
	if !ok {
		return 0, "", errors.New("fake sampler: no result queued")
	}
	if s.Block {
		<-ctx.Done()
		return 0, "", ctx.Err()
	}
	return s.Idle, s.App, s.Err
}

func (f *Fake) next() (Sample, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if len(f.queue) == 0 {
		return Sample{}, false
	}
	s := f.queue[0]
	f.queue = f.queue[1:]
	return s, true
}

var (
	idlePattern   = regexp.MustCompile(`"HIDIdleTime"\s*=\s*(\S+)`)
	bundlePattern = regexp.MustCompile(`"CFBundleIdentifier"\s*=\s*"([^"]+)"`)
)

// ParseIdle extracts the idle time from "ioreg -c IOHIDSystem" output, where
// HIDIdleTime is given in nanoseconds.
func ParseIdle(output string) (time.Duration, error) {
	m := idlePattern.FindStringSubmatch(output)
	if m == nil {
		return 0, errors.New("HIDIdleTime not found in ioreg output")
	}
	ns, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil || ns < 0 {
		return 0, fmt.Errorf("invalid HIDIdleTime %q", m[1])
	}
	return time.Duration(ns), nil
}

// ParseBundleID extracts the bundle identifier from
// "lsappinfo info -only bundleid" output.
func ParseBundleID(output string) (string, error) {
	m := bundlePattern.FindStringSubmatch(output)
	if m == nil {
		return "", errors.New("bundle identifier not found in lsappinfo output")
	}
	return m[1], nil
}
