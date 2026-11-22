package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/event"
)

const escape = "\x1b"

func TestTodaySanitizesExternallyDerivedText(t *testing.T) {
	deps, st, out := newDailyEnv(t, "")
	start := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC)
	// A stored ticket only counts on an event without a branch.
	ctx := shellContext(start, "/work/api", "")
	ctx.Ticket = "EVIL-1" + escape + "]0;pwned\a\r"
	mustAppend(t, st, ctx)
	for m := 0; m <= 40; m++ {
		mustAppend(t, st, event.Event{At: start.Add(time.Duration(m) * time.Minute), Source: "presence", Kind: event.KindPresence})
	}
	if err := runToday(context.Background(), deps, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "EVIL-1") {
		t.Fatalf("test setup: the ticket is not shown:\n%q", out.String())
	}
	assertNoControlBytes(t, out.String())
}

func TestInitSanitizesExternallyDerivedText(t *testing.T) {
	env := newTestEnv(t, "", "")
	hostile := errors.New("denied" + escape + "[2J\r\nfake line")
	env.deps.Launchctl = func(context.Context, ...string) (string, error) {
		return "out" + escape + "]0;x\a", hostile
	}
	env.deps.SetToken = func(string) error { return hostile }
	env.deps.ReadSecret = func() (string, error) { return "secret", nil }
	env.deps.Binary = func() (string, error) { return "", hostile }
	var out bytes.Buffer
	if err := runInit(context.Background(), env.deps, initOptions{Yes: true, URL: "https://x.example/" + escape + "[2J"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "denied") {
		t.Fatalf("test setup: the error text is not shown:\n%q", out.String())
	}
	assertNoControlBytes(t, out.String())
}

func TestInitSanitizesLaunchctlOutput(t *testing.T) {
	env := newTestEnv(t, "", "")
	env.deps.Launchctl = func(_ context.Context, args ...string) (string, error) {
		if args[0] == "bootstrap" {
			return "bad" + escape + "[2Jout", errors.New("exit status 5")
		}
		return "", errors.New("not loaded")
	}
	var out bytes.Buffer
	opts := initOptions{Yes: true, SkipToken: true, SkipConfig: true, SkipHook: true}
	if err := runInit(context.Background(), env.deps, opts, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "badout") && !strings.Contains(out.String(), "bad[2Jout") {
		t.Fatalf("test setup: launchctl output is not shown:\n%q", out.String())
	}
	assertNoControlBytes(t, out.String())
}

// assertNoControlBytes fails when s holds an ESC, CR or other control byte
// besides the newlines that end output lines.
func assertNoControlBytes(t *testing.T, s string) {
	t.Helper()
	for i, r := range s {
		if r != '\n' && (r < 0x20 || r == 0x7f) {
			t.Errorf("control character %q at byte %d of output:\n%q", r, i, s)
			return
		}
	}
}
