package cli

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestContextReaderPassesDataThrough(t *testing.T) {
	r := NewContextReader(context.Background(), strings.NewReader("yes\n"))
	got, err := io.ReadAll(r)
	if err != nil || string(got) != "yes\n" {
		t.Fatalf("ReadAll = %q, %v", got, err)
	}
}

func TestContextReaderAbortsBlockedReadOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	pr, pw := io.Pipe() // never written to: the read blocks like a prompt
	defer func() { _ = pw.Close() }()
	errs := make(chan error, 1)
	go func() {
		_, err := NewContextReader(ctx, pr).Read(make([]byte, 8))
		errs <- err
	}()

	cancel()

	select {
	case err := <-errs:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("blocked read ignored the cancellation")
	}
}

func TestContextReaderRefusesReadAfterCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewContextReader(ctx, strings.NewReader("x")).Read(make([]byte, 1)); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}
