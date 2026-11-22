// Package logfile writes the shared ticktack log file and keeps it bounded.
//
// The shell hook, the agent and launchd (stdout and stderr of the agent) all
// append to the same file from different processes. Two mechanisms bound it:
//
//   - Rotation: once the file exceeds a size limit its tail is copied to a
//     single ".1" backup and the file is truncated in place. Truncating keeps
//     the inode, so writers that hold the file open in append mode (launchd)
//     continue at the new end.
//   - Throttling: an identical message is written at most once per window.
//     The state lives in a small file next to the log so it also holds across
//     the short-lived hook processes. It stores a hash per message, never the
//     message text.
package logfile

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultMaxBytes is the size above which the log file is rotated.
	DefaultMaxBytes int64 = 1 << 20
	// DefaultThrottle is the shortest time between two identical messages.
	DefaultThrottle = 10 * time.Minute

	// maxMessageBytes caps one message so a single write stays small.
	maxMessageBytes = 4 << 10
	// maxThrottleEntries caps the number of remembered messages.
	maxThrottleEntries = 32

	backupSuffix   = ".1"
	throttleSuffix = ".throttle"
	timeLayout     = "2006/01/02 15:04:05"
	filePerm       = 0o600
	dirPerm        = 0o700
)

// Logger appends prefixed, timestamped lines to the log file at Path.
// The zero value is not usable; build one with New.
type Logger struct {
	// Path is the log file.
	Path string
	// Prefix starts every line and names the writer ("agent: ").
	Prefix string
	// MaxBytes is the rotation threshold; zero or less disables rotation.
	MaxBytes int64
	// Throttle is the window in which an identical message is written only
	// once; zero or less disables throttling.
	Throttle time.Duration
	// Now is the clock.
	Now func() time.Time

	mu sync.Mutex
}

// New returns a Logger for path with the default limits.
func New(path, prefix string) *Logger {
	return &Logger{
		Path:     path,
		Prefix:   prefix,
		MaxBytes: DefaultMaxBytes,
		Throttle: DefaultThrottle,
		Now:      time.Now,
	}
}

// Printf formats and appends one message. Logging is best effort: when the
// file cannot be written there is nowhere left to report it, and neither the
// shell prompt nor the agent may fail because of it, so the error is dropped.
func (l *Logger) Printf(format string, args ...any) {
	_ = l.write(fmt.Sprintf(format, args...))
}

// Rotate rotates the log file when it exceeds MaxBytes. A missing file is not
// an error. Printf rotates on its own; call Rotate to bound a file that other
// writers (launchd) grew while this process logged nothing.
func (l *Logger) Rotate() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rotate()
}

func (l *Logger) write(message string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(message) > maxMessageBytes {
		message = strings.ToValidUTF8(message[:maxMessageBytes], "") + " [truncated]"
	}
	now := l.Now()
	suppressed, due := l.throttle(message, now)
	if !due {
		return nil
	}
	if suppressed > 0 {
		message += fmt.Sprintf(" (%d identical messages suppressed)", suppressed)
	}

	if err := os.MkdirAll(filepath.Dir(l.Path), dirPerm); err != nil {
		return err
	}
	// A failed rotation must not cost the message: the throttle has already
	// counted it as written, so dropping it here would silence the log for as
	// long as the rotation keeps failing.
	rotateErr := l.rotate()
	f, err := os.OpenFile(l.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, filePerm)
	if err != nil {
		return errors.Join(rotateErr, err)
	}
	_, err = fmt.Fprintf(f, "%s%s %s\n", l.Prefix, now.Format(timeLayout), message)
	return errors.Join(rotateErr, err, f.Close())
}

// rotate copies the last MaxBytes of an oversized log file to the backup and
// truncates the log in place. Lines another process appends between the copy
// and the truncation are lost; that is accepted for a diagnostic log. When the
// backup cannot be written the log is left untouched.
func (l *Logger) rotate() error {
	if l.MaxBytes <= 0 {
		return nil
	}
	info, err := os.Stat(l.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Size() <= l.MaxBytes {
		return nil
	}
	copied, err := copyTail(l.Path, l.Path+backupSuffix, l.MaxBytes)
	if err != nil {
		return fmt.Errorf("rotate log file: %w", err)
	}
	if !copied {
		// Another process rotated since the size check; its backup stands.
		return nil
	}
	if err := os.Truncate(l.Path, 0); err != nil {
		return fmt.Errorf("rotate log file: %w", err)
	}
	return nil
}

// copyTail atomically replaces dst with the last limit bytes of src. It
// reports false and leaves dst alone when src holds no more than limit bytes
// or is gone: the hook and the agent rotate the same file, so another process
// may have truncated it since the caller measured it.
func copyTail(src, dst string, limit int64) (copied bool, err error) {
	in, err := os.Open(src)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() { _ = in.Close() }()
	info, err := in.Stat()
	if err != nil {
		return false, err
	}
	if info.Size() <= limit {
		return false, nil
	}
	// An absolute offset stays valid even if the file shrinks from here on.
	if _, err := in.Seek(info.Size()-limit, io.SeekStart); err != nil {
		return false, err
	}
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, filePerm)
	if err != nil {
		return false, err
	}
	_, err = io.Copy(out, io.LimitReader(in, limit))
	if err = errors.Join(err, out.Close()); err != nil {
		_ = os.Remove(tmp)
		return false, err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return false, err
	}
	return true, nil
}

// throttleEntry is the state of one remembered message.
type throttleEntry struct {
	// at is when the message was last written to the log.
	at time.Time
	// suppressed counts the identical messages dropped since then.
	suppressed int
}

// throttle reports whether message may be written now and how many identical
// messages were dropped since it was last written. It updates the state file.
// A state file that cannot be read or written never blocks logging: the size
// limit still bounds the log.
func (l *Logger) throttle(message string, now time.Time) (suppressed int, due bool) {
	if l.Throttle <= 0 {
		return 0, true
	}
	statePath := l.Path + throttleSuffix
	entries := readThrottleState(statePath)
	key := messageKey(l.Prefix, message)

	entry, known := entries[key]
	if age := now.Sub(entry.at); known && age >= 0 && age < l.Throttle {
		entry.suppressed++
		entries[key] = entry
		_ = writeThrottleState(statePath, entries)
		return 0, false
	}
	for k, e := range entries {
		if age := now.Sub(e.at); age < 0 || age >= l.Throttle {
			delete(entries, k)
		}
	}
	entries[key] = throttleEntry{at: now}
	_ = writeThrottleState(statePath, entries)
	return entry.suppressed, true
}

// messageKey identifies a message without revealing its text.
func messageKey(prefix, message string) string {
	sum := sha256.Sum256([]byte(prefix + "\x00" + message))
	return hex.EncodeToString(sum[:8])
}

// readThrottleState loads the state file. A missing or damaged file, and any
// damaged line in it, counts as no state.
func readThrottleState(path string) map[string]throttleEntry {
	entries := map[string]throttleEntry{}
	f, err := os.Open(path)
	if err != nil {
		return entries
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() && len(entries) < maxThrottleEntries {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 3 {
			continue
		}
		nanos, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			continue
		}
		suppressed, err := strconv.Atoi(fields[2])
		if err != nil || suppressed < 0 {
			continue
		}
		entries[fields[0]] = throttleEntry{at: time.Unix(0, nanos), suppressed: suppressed}
	}
	return entries
}

// writeThrottleState atomically replaces the state file, keeping only the
// most recent maxThrottleEntries messages.
func writeThrottleState(path string, entries map[string]throttleEntry) error {
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := entries[keys[i]], entries[keys[j]]
		if !a.at.Equal(b.at) {
			return a.at.After(b.at)
		}
		return keys[i] < keys[j]
	})
	if len(keys) > maxThrottleEntries {
		keys = keys[:maxThrottleEntries]
	}
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s %d %d\n", k, entries[k].at.UnixNano(), entries[k].suppressed)
	}
	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), filePerm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
