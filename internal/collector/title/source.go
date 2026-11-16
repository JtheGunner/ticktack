package title

import (
	"context"
	"errors"
	"sync"
)

// ErrPermission means the OS denied reading window titles (macOS
// Accessibility or Automation permission missing).
var ErrPermission = errors.New("window title access denied")

// Window is what a Source reports about the frontmost window. It exists in
// memory only and must never be stored, logged or put into an error.
type Window struct {
	App   string // bundle ID
	Title string
}

// Source reads the frontmost window.
type Source interface {
	Frontmost(ctx context.Context) (Window, error)
}

// FakeResult is one scripted Fake result.
type FakeResult struct {
	Window Window
	Err    error
}

// Fake is a scripted Source for tests. Queued results are returned in order;
// once the queue is empty Frontmost returns an error.
type Fake struct {
	mu    sync.Mutex
	queue []FakeResult
}

// Push queues a successful result.
func (f *Fake) Push(app, title string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queue = append(f.queue, FakeResult{Window: Window{App: app, Title: title}})
}

// PushError queues a failing result.
func (f *Fake) PushError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queue = append(f.queue, FakeResult{Err: err})
}

// Frontmost implements Source.
func (f *Fake) Frontmost(context.Context) (Window, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.queue) == 0 {
		return Window{}, errors.New("fake source: no result queued")
	}
	r := f.queue[0]
	f.queue = f.queue[1:]
	return r.Window, r.Err
}
