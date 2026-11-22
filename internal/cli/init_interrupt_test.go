package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/token"
)

// cancelAfterReader serves the given input, then cancels the context and fails
// the next read with the cancellation, like the context reader of main does
// when a signal arrives while a prompt waits.
type cancelAfterReader struct {
	input  io.Reader
	cancel context.CancelFunc
	ctx    context.Context
}

func (c *cancelAfterReader) Read(p []byte) (int, error) {
	n, err := c.input.Read(p)
	if n > 0 {
		return n, nil
	}
	if errors.Is(err, io.EOF) {
		c.cancel()
		return 0, c.ctx.Err()
	}
	return n, err
}

func interruptedReader(ctx context.Context, cancel context.CancelFunc, input string) *bufio.Reader {
	return bufio.NewReader(&cancelAfterReader{input: strings.NewReader(input), cancel: cancel, ctx: ctx})
}

func assertInterrupted(t *testing.T, err error, out string) {
	t.Helper()
	if !errors.Is(err, context.Canceled) || ExitCode(err) != ExitInterrupted {
		t.Fatalf("err = %v, exit code %d; want the cancellation and 130", err, ExitCode(err))
	}
	if strings.Contains(out, msgInitFinished) || strings.Contains(out, msgInitFinishedWithFailures) {
		t.Fatalf("an interrupted init reported completion:\n%s", out)
	}
}

func TestInitInterruptedAtAConfirmationDoesNotConsentOrExitZero(t *testing.T) {
	env := newTestEnv(t, "", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	env.deps.In = interruptedReader(ctx, cancel, "")
	cfgPath, _ := config.DefaultPath()

	var out bytes.Buffer
	err := runInit(ctx, env.deps, initOptions{}, &out)

	assertInterrupted(t, err, out.String())
	if _, statErr := os.Stat(cfgPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("a cancelled confirmation wrote the config file")
	}
	if len(env.fl.calls) != 0 {
		t.Fatalf("later steps ran after the interruption: %v", env.fl.calls)
	}
}

func TestInitInterruptedAtTheURLPromptWritesNothingAndExitsInterrupted(t *testing.T) {
	env := newTestEnv(t, "", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The config confirmation is answered, then the signal hits the URL prompt.
	env.deps.In = interruptedReader(ctx, cancel, "y\n")
	cfgPath, _ := config.DefaultPath()

	var out bytes.Buffer
	err := runInit(ctx, env.deps, initOptions{}, &out)

	assertInterrupted(t, err, out.String())
	if _, statErr := os.Stat(cfgPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("a cancelled URL prompt was taken as an empty answer and wrote the config")
	}
}

func TestInitInterruptedAtTheSecretPromptStoresNoToken(t *testing.T) {
	env := newTestEnv(t, "", "y\n")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	env.deps.ReadSecret = func() (string, error) {
		cancel()
		// Even a value that was read before the cancellation is not stored.
		return testSecret, ctx.Err()
	}
	stored := false
	env.deps.SetToken = func(string) error { stored = true; return token.Set(testSecret) }

	var out bytes.Buffer
	err := runInit(ctx, env.deps, initOptions{SkipConfig: true}, &out)

	assertInterrupted(t, err, out.String())
	if stored {
		t.Fatal("a cancelled secret read stored a token")
	}
	if strings.Contains(out.String(), testSecret) {
		t.Fatal("the secret was printed")
	}
}

func TestInitWithAlreadyCancelledContextDoesNothing(t *testing.T) {
	env := newTestEnv(t, "", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var out bytes.Buffer
	err := runInit(ctx, env.deps, initOptions{Yes: true, URL: "https://yt.example.com"}, &out)

	assertInterrupted(t, err, out.String())
	if cfgPath, _ := config.DefaultPath(); fileExists(cfgPath) {
		t.Fatal("a cancelled init wrote the config file")
	}
}

func TestReadSecretUnlessCancelled(t *testing.T) {
	t.Run("returns the secret", func(t *testing.T) {
		restored := false
		got, err := readSecretUnlessCancelled(context.Background(),
			func() ([]byte, error) { return []byte("s3cret"), nil },
			func() { restored = true })
		if err != nil || got != "s3cret" || restored {
			t.Fatalf("got %q, %v, restored %v", got, err, restored)
		}
	})
	t.Run("cancel returns promptly with the terminal restored", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		block := make(chan struct{})
		defer close(block)
		restored := make(chan struct{}, 1)
		result := make(chan error, 1)
		go func() {
			got, err := readSecretUnlessCancelled(ctx,
				func() ([]byte, error) { <-block; return []byte("late"), nil },
				func() { restored <- struct{}{} })
			if got != "" {
				err = errors.New("a cancelled read returned input")
			}
			result <- err
		}()
		cancel()
		select {
		case err := <-result:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("err = %v, want context.Canceled", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the read did not return after the cancellation")
		}
		select {
		case <-restored:
		default:
			t.Fatal("the terminal state was not restored")
		}
	})
}
