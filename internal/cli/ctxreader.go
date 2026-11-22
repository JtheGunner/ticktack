package cli

import (
	"context"
	"io"
)

// NewContextReader returns a reader whose Read gives up with ctx.Err() as soon
// as ctx is done. A terminal read cannot be interrupted, so the read keeps
// running in its own goroutine and its data is dropped; the process is about
// to end then, so the goroutine does not outlive anything. Without this, a
// prompt waiting for input would ignore the first Ctrl-C, because the signal
// only cancels the context.
func NewContextReader(ctx context.Context, r io.Reader) io.Reader {
	return &contextReader{ctx: ctx, r: r}
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

type readResult struct {
	n   int
	err error
}

func (c *contextReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	buf := make([]byte, len(p))
	done := make(chan readResult, 1)
	go func() {
		n, err := c.r.Read(buf)
		done <- readResult{n, err}
	}()
	select {
	case <-c.ctx.Done():
		return 0, c.ctx.Err()
	case res := <-done:
		copy(p, buf[:res.n])
		return res.n, res.err
	}
}
