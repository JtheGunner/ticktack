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

// End finishes the running pause at now: it stores the interval and then
// removes the flag file. Without a running pause it does nothing.
func End(flagPath string, now time.Time) error {
	start, running, err := runningSince(flagPath)
	if err != nil || !running {
		return err
	}
	logPath := LogPath(flagPath)
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, fileMode)
	if err != nil {
		return fmt.Errorf("open pause log: %w", err)
	}
	line := start.UTC().Format(timeLayout) + " " + now.UTC().Format(timeLayout) + "\n"
	_, writeErr := f.WriteString(line)
	if err := errors.Join(writeErr, f.Close()); err != nil {
		return fmt.Errorf("write pause log %s: %w", logPath, err)
	}
	// The interval is stored first: a failure here leaves a pause that is
	// recorded twice, never one that is lost.
	if err := os.Remove(flagPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove pause flag: %w", err)
	}
	return nil
}

// Load returns every finished pause and, when a pause is running, that pause
// as lasting until now. It fails when a file cannot be read or understood, so
// a caller never filters with an incomplete set.
func Load(flagPath string, now time.Time) (Intervals, error) {
	ivs, err := readLog(LogPath(flagPath))
	if err != nil {
		return nil, err
	}
	start, running, err := runningSince(flagPath)
	if err != nil {
		return nil, err
	}
	if running {
		ivs = append(ivs, Interval{Start: start, End: now})
	}
	return ivs, nil
}

// runningSince returns the start of the running pause. A flag file without
// content comes from a version that stored no start; its modification time
// stands in.
func runningSince(flagPath string) (start time.Time, running bool, err error) {
	raw, err := os.ReadFile(flagPath)
	if errors.Is(err, fs.ErrNotExist) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("read pause flag: %w", err)
	}
	text := strings.TrimSpace(string(raw))
	if text == "" {
		info, err := os.Stat(flagPath)
		if err != nil {
			return time.Time{}, false, fmt.Errorf("read pause flag: %w", err)
		}
		return info.ModTime(), true, nil
	}
	start, err = time.Parse(timeLayout, text)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("pause flag %s holds no valid start time: %w", flagPath, err)
	}
	return start, true, nil
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
