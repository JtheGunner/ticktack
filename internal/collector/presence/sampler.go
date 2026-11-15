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
	"sync"
	"time"
)

// Sampler measures the current presence signals.
type Sampler interface {
	// Sample returns the time since the last keyboard or mouse input and the
	// bundle identifier of the frontmost application.
	Sample(ctx context.Context) (idle time.Duration, app string, err error)
}

// Sample is one scripted Fake result.
type Sample struct {
	Idle time.Duration
	App  string
	Err  error
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
func (f *Fake) Sample(context.Context) (time.Duration, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if len(f.queue) == 0 {
		return 0, "", errors.New("fake sampler: no result queued")
	}
	s := f.queue[0]
	f.queue = f.queue[1:]
	return s.Idle, s.App, s.Err
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
