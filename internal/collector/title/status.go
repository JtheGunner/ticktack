package title

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ProbeState is the outcome of one title read.
type ProbeState string

const (
	// ProbeOK means the title source answered, so the permission is present.
	ProbeOK ProbeState = "ok"
	// ProbeDenied means macOS refused to read window titles.
	ProbeDenied ProbeState = "denied"
	// ProbeFailed means the read failed for another reason.
	ProbeFailed ProbeState = "failed"
)

// statusRefresh is how often an unchanged state is written again, so the
// timestamp shows that the agent is still probing without a write every tick.
const statusRefresh = 5 * time.Minute

// ProbeStatus is what the status file holds: the outcome and the time of the
// last title probe. It never holds a window title or any other content of the
// window.
type ProbeStatus struct {
	State ProbeState `json:"state"`
	At    time.Time  `json:"at"`
}

// ErrBadStatus means the status file exists but cannot be understood.
var ErrBadStatus = errors.New("unreadable title probe status")

// StatusRecorder stores the outcome of a title probe.
type StatusRecorder interface {
	Record(state ProbeState, at time.Time) error
}

// StatusFile records the last title probe in a small file that `ticktack
// doctor` reads. It writes when the state changes and otherwise at most every
// few minutes.
type StatusFile struct {
	Path string

	mu   sync.Mutex
	last ProbeStatus
}

// Record implements StatusRecorder.
func (f *StatusFile) Record(state ProbeState, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.last.State == state && at.Sub(f.last.At) < statusRefresh {
		return nil
	}
	data, err := json.Marshal(ProbeStatus{State: state, At: at})
	if err != nil {
		return fmt.Errorf("encode title probe status: %w", err)
	}
	if err := writeFileReplacing(f.Path, data); err != nil {
		return err
	}
	f.last = ProbeStatus{State: state, At: at}
	return nil
}

// statusTempPrefix starts the name of the temp file the status writer creates
// next to the status file; a crash can leave one behind.
const statusTempPrefix = ".title-status-"

// StatusFiles lists every file the status writer owns for the status file at
// path: the file itself and any temp file a crashed write left in the same
// directory (recognised by the writer's own prefix, nothing else). Callers
// that delete the status file use it so no leftover stays behind. The files
// need not exist; an unreadable directory yields just path.
func StatusFiles(path string) []string {
	files := []string{path}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		return files
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), statusTempPrefix) {
			continue
		}
		files = append(files, filepath.Join(filepath.Dir(path), entry.Name()))
	}
	return files
}

// ReadStatus reads the status file at path. A missing file yields an error
// matching os.ErrNotExist; a file that is not a valid status yields
// ErrBadStatus.
func ReadStatus(path string) (ProbeStatus, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ProbeStatus{}, err
	}
	var status ProbeStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return ProbeStatus{}, fmt.Errorf("%w: %w", ErrBadStatus, err)
	}
	switch status.State {
	case ProbeOK, ProbeDenied, ProbeFailed:
	default:
		return ProbeStatus{}, fmt.Errorf("%w: unknown state %q", ErrBadStatus, status.State)
	}
	return status, nil
}

// writeFileReplacing writes data to path through a temp file and a rename, so
// a reader never sees a half-written file.
func writeFileReplacing(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, statusTempPrefix+"*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write title probe status: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close title probe status: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace title probe status: %w", err)
	}
	return nil
}
