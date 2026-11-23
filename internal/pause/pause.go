// Package pause records when capture was paused, so sources that read the
// past (the git scan, the ActivityWatch import) can leave paused time out.
//
// Capture is paused while the flag file exists; the file holds the time the
// pause began. Ending a pause appends the finished interval to a log file
// beside the flag. Both files hold timestamps only.
package pause

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/JtheGunner/ticktack/internal/event"
)

const (
	logName    = "pause-intervals"
	timeLayout = time.RFC3339Nano
	fileMode   = 0o600
	dirMode    = 0o700
)

// Interval is one pause. Start is inclusive, End is exclusive.
type Interval struct {
	Start time.Time
	End   time.Time
}

// Intervals is a set of pauses.
type Intervals []Interval

// Contains reports whether at lies inside one of the pauses.
func (ivs Intervals) Contains(at time.Time) bool {
	for _, iv := range ivs {
		if !at.Before(iv.Start) && at.Before(iv.End) {
			return true
		}
	}
	return false
}

// LogPath returns the file that holds the finished pauses for the flag file
// at flagPath.
func LogPath(flagPath string) string {
	return filepath.Join(filepath.Dir(flagPath), logName)
}

// Begin starts a pause at now by creating the flag file. A pause that is
// already running keeps its start.
func Begin(flagPath string, now time.Time) error {
	if err := os.MkdirAll(filepath.Dir(flagPath), dirMode); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	f, err := os.OpenFile(flagPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fileMode)
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("create pause flag: %w", err)
	}
	_, writeErr := f.WriteString(now.UTC().Format(timeLayout) + "\n")
	if err := errors.Join(writeErr, f.Close()); err != nil {
		return fmt.Errorf("write pause flag %s: %w", flagPath, err)
	}
	return nil
}

// Ended describes how a pause was finished.
type Ended struct {
	// Recovered is set when the flag file held no usable start time: the
	// interval then starts at the file's modification time instead. It names
	// what was wrong with the file.
	Recovered error
}

// End finishes the running pause at now: it stores the interval and then
// removes the flag file. Without a running pause it does nothing. A flag file
// whose content cannot be read or understood still ends the pause, so the
// user is never stuck paused: the interval starts at the file's modification
// time (the moment the flag was written), and Ended.Recovered reports the
// problem. The stored interval is never shorter than the pause was.
func End(flagPath string, now time.Time) (Ended, error) {
	start, running, recovered, err := runningSince(flagPath)
	if err != nil || !running {
		return Ended{}, err
	}
	logPath := LogPath(flagPath)
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, fileMode)
	if err != nil {
		return Ended{}, fmt.Errorf("open pause log: %w", err)
	}
	line := start.UTC().Format(timeLayout) + " " + now.UTC().Format(timeLayout) + "\n"
	_, writeErr := f.WriteString(line)
	if err := errors.Join(writeErr, f.Close()); err != nil {
		return Ended{}, fmt.Errorf("write pause log %s: %w", logPath, err)
	}
	// The interval is stored first: a failure here leaves a pause that is
	// recorded twice, never one that is lost.
	if err := os.Remove(flagPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Ended{}, fmt.Errorf("remove pause flag: %w", err)
	}
	return Ended{Recovered: recovered}, nil
}

// Load returns every finished pause and, when a pause is running, that pause
// as lasting until now. It fails when a file cannot be read or understood, so
// a caller never filters with an incomplete set.
func Load(flagPath string, now time.Time) (Intervals, error) {
	ivs, err := readLog(LogPath(flagPath))
	if err != nil {
		return nil, err
	}
	start, running, recovered, err := runningSince(flagPath)
	if err != nil {
		return nil, err
	}
	if recovered != nil {
		return nil, recovered
	}
	if running {
		ivs = append(ivs, Interval{Start: start, End: now})
	}
	return ivs, nil
}

// runningSince returns the start of the running pause. A flag file without
// content comes from a version that stored no start; its modification time
// stands in. A file whose content cannot be read or parsed is reported as
// recovered, with the modification time as start; Load refuses such a file,
// End uses the stand-in. Only a flag file that cannot even be examined is an
// error.
func runningSince(flagPath string) (start time.Time, running bool, recovered, err error) {
	raw, readErr := os.ReadFile(flagPath)
	if errors.Is(readErr, fs.ErrNotExist) {
		return time.Time{}, false, nil, nil
	}
	var problem error
	switch {
	case readErr != nil:
		problem = fmt.Errorf("read pause flag: %w", readErr)
	case strings.TrimSpace(string(raw)) == "":
		// Written by a version that stored no start: not a problem.
	default:
		parsed, parseErr := time.Parse(timeLayout, strings.TrimSpace(string(raw)))
		if parseErr == nil {
			return parsed, true, nil, nil
		}
		problem = fmt.Errorf("pause flag %s holds no valid start time: %w", flagPath, parseErr)
	}
	info, statErr := os.Stat(flagPath)
	if errors.Is(statErr, fs.ErrNotExist) {
		return time.Time{}, false, nil, nil
	}
	if statErr != nil {
		return time.Time{}, false, nil, fmt.Errorf("read pause flag: %w", statErr)
	}
	return info.ModTime(), true, problem, nil
}

func readLog(logPath string) (Intervals, error) {
	raw, err := os.ReadFile(logPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read pause log: %w", err)
	}
	var ivs Intervals
	for n, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		iv, err := parseLine(line)
		if err != nil {
			return nil, fmt.Errorf("pause log %s line %d: %w", logPath, n+1, err)
		}
		ivs = append(ivs, iv)
	}
	return ivs, nil
}

func parseLine(line string) (Interval, error) {
	fields := strings.Fields(line)
	if len(fields) != 2 {
		return Interval{}, errors.New("want a start and an end time")
	}
	start, err := time.Parse(timeLayout, fields[0])
	if err != nil {
		return Interval{}, fmt.Errorf("start time: %w", err)
	}
	end, err := time.Parse(timeLayout, fields[1])
	if err != nil {
		return Interval{}, fmt.Errorf("end time: %w", err)
	}
	return Interval{Start: start, End: end}, nil
}

// Running reports whether the pause flag file exists at flagPath. An error
// other than a missing file is returned, never read as "not paused".
func Running(flagPath string) (bool, error) {
	_, err := os.Stat(flagPath)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("check pause flag: %w", err)
}

// IsPaused is the one fail-closed pause check of every collector and the
// shell hook: when the flag cannot be read, capture counts as paused, so an
// unreadable state never stores time the user meant to keep out.
func IsPaused(flagPath string) bool {
	running, err := Running(flagPath)
	return err != nil || running
}

// Outside returns the events that do not lie inside a pause. Sources that
// read the past use it, so paused time is never stored afterwards.
//
// An event without a duration is kept or dropped by its time. A gap event
// (event.KindGap) lasts from At minus IdleSeconds until At; the paused part is
// cut off and the part after the last pause is kept, with At and IdleSeconds
// adjusted. A gap that lies wholly inside a pause is dropped. Only the
// timestamps of a stored gap change: nothing is invented, and the idempotency
// key stays, so the same pass gives the same event again.
func (ivs Intervals) Outside(events []event.Event) []event.Event {
	kept := make([]event.Event, 0, len(events))
	for _, e := range events {
		if e.Kind == event.KindGap && e.IdleSeconds > 0 {
			if clipped, ok := ivs.clipGap(e); ok {
				kept = append(kept, clipped)
			}
			continue
		}
		if !ivs.Contains(e.At) {
			kept = append(kept, e)
		}
	}
	return kept
}

// clipGap removes the paused time from a gap event. It reports false when
// nothing of the gap remains.
func (ivs Intervals) clipGap(e event.Event) (event.Event, bool) {
	start := e.At.Add(-time.Duration(e.IdleSeconds) * time.Second)
	end := e.At
	// Pauses can overlap or repeat; repeat until no pause cuts the gap any
	// more. Of a gap that spans a pause, the later part survives.
	for changed := true; changed && end.After(start); {
		changed = false
		for _, iv := range ivs {
			if !iv.Start.Before(end) || !iv.End.After(start) {
				continue
			}
			if iv.End.Before(end) {
				start = iv.End
			} else {
				end = iv.Start
			}
			changed = true
		}
	}
	idle := int(end.Sub(start).Round(time.Second) / time.Second)
	if !end.After(start) || idle <= 0 {
		return event.Event{}, false
	}
	e.At = end
	e.IdleSeconds = idle
	return e, true
}
