package title

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStatusFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "title-status.json")
	f := &StatusFile{Path: path}
	if err := f.Record(ProbeDenied, testNow); err != nil {
		t.Fatal(err)
	}
	got, err := ReadStatus(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != ProbeDenied || !got.At.Equal(testNow) {
		t.Errorf("status = %+v", got)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, %v; want 0600", info.Mode(), err)
	}
}

func TestStatusFileWritesOnChangeAndThenRarely(t *testing.T) {
	path := filepath.Join(t.TempDir(), "title-status.json")
	f := &StatusFile{Path: path}
	record := func(state ProbeState, at time.Time) ProbeStatus {
		t.Helper()
		if err := f.Record(state, at); err != nil {
			t.Fatal(err)
		}
		got, err := ReadStatus(path)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	tests := []struct {
		name    string
		state   ProbeState
		at      time.Time
		wantAt  time.Time
		wantNew ProbeState
	}{
		{"first probe is written", ProbeOK, testNow, testNow, ProbeOK},
		{"same state soon after is not rewritten", ProbeOK, testNow.Add(30 * time.Second), testNow, ProbeOK},
		{"same state after the refresh period is rewritten", ProbeOK, testNow.Add(10 * time.Minute), testNow.Add(10 * time.Minute), ProbeOK},
		{"a changed state is written at once", ProbeDenied, testNow.Add(10*time.Minute + time.Second), testNow.Add(10*time.Minute + time.Second), ProbeDenied},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := record(tt.state, tt.at)
			if got.State != tt.wantNew || !got.At.Equal(tt.wantAt) {
				t.Errorf("status = %+v, want %s at %s", got, tt.wantNew, tt.wantAt)
			}
		})
	}
}

func TestReadStatusErrors(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.json")
	if _, err := ReadStatus(missing); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing file: err = %v", err)
	}
	for name, content := range map[string]string{
		"not json":      "ok",
		"unknown state": `{"state":"maybe","at":"2026-03-02T09:00:00Z"}`,
		"empty":         "",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, strings.ReplaceAll(name, " ", "-"))
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadStatus(path); !errors.Is(err, ErrBadStatus) {
				t.Errorf("err = %v, want ErrBadStatus", err)
			}
		})
	}
}

func TestCollectorRecordsTheProbeOutcome(t *testing.T) {
	tests := []struct {
		name string
		push func(f *Fake)
		want ProbeState
	}{
		{"read works", func(f *Fake) { f.Push("com.apple.Terminal", "SECRET-TITLE") }, ProbeOK},
		{"permission denied", func(f *Fake) { f.PushError(ErrPermission) }, ProbeDenied},
		{"other failure", func(f *Fake) { f.PushError(errors.New("osascript missing")) }, ProbeFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "title-status.json")
			src := &Fake{}
			tt.push(src)
			c := newCollector(src, &recorder{}, nil)
			c.Status = &StatusFile{Path: path}
			c.Tick(context.Background())

			got, err := ReadStatus(path)
			if err != nil || got.State != tt.want || !got.At.Equal(testNow) {
				t.Fatalf("status = %+v, %v; want %s", got, err, tt.want)
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "SECRET-TITLE") || strings.Contains(string(raw), "Terminal") {
				t.Errorf("status file holds window content: %s", raw)
			}
		})
	}
}

func TestCollectorRecordsNothingWhilePaused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "title-status.json")
	src := &Fake{}
	src.Push("com.apple.Terminal", "x")
	c := newCollector(src, &recorder{}, nil)
	c.Paused = func() bool { return true }
	c.Status = &StatusFile{Path: path}
	c.Tick(context.Background())
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a paused collector wrote a status: %v", err)
	}
}

type failingRecorder struct{}

func (failingRecorder) Record(ProbeState, time.Time) error { return errors.New("disk full") }

func TestCollectorSurvivesAStatusWriteFailure(t *testing.T) {
	src := &Fake{}
	src.Push("com.apple.Terminal", "x")
	src.Push("com.apple.Terminal", "x")
	var logged []string
	c := newCollector(src, &recorder{}, nil)
	c.Status = failingRecorder{}
	c.Logf = func(format string, _ ...any) { logged = append(logged, format) }
	c.Tick(context.Background())
	c.Tick(context.Background())
	if len(logged) != 1 {
		t.Errorf("logged %d times, want once per streak: %v", len(logged), logged)
	}
}
