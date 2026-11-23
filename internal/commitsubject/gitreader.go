package commitsubject

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/JtheGunner/ticktack/internal/collector/git"
)

const (
	// readTimeout bounds one git call.
	readTimeout = 5 * time.Second
	// maxOutputBytes caps what is taken from git: a subject is one line.
	maxOutputBytes = 16 * 1024

	fieldSeparator = "\x1f"
)

// GitReader reads commits with the local git binary, read-only.
type GitReader struct{}

// Read returns the subject and the merge flag of the commit hash in repo.
// The repository path comes from a stored event and the hash is checked to be
// a full object id before it reaches git. git is started without a shell,
// with the repository-selecting environment removed, under a timeout.
func (GitReader) Read(ctx context.Context, repo, hash string) (Commit, error) {
	if !filepath.IsAbs(repo) {
		return Commit{}, fmt.Errorf("repository %q is not an absolute path", repo)
	}
	if !git.IsObjectID(hash) {
		return Commit{}, errors.New("not a commit hash")
	}
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "-C", repo,
		"-c", "log.showSignature=false",
		"log", "-1", "--no-walk", "--no-color", "--no-decorate",
		"--format=%P"+fieldSeparator+"%s", hash, "--")
	cmd.Env = git.CommandEnv()
	out := &cappedBuffer{limit: maxOutputBytes}
	cmd.Stdout = out
	if err := cmd.Run(); err != nil {
		return Commit{}, fmt.Errorf("read commit: %w", err)
	}
	parents, subject, ok := strings.Cut(strings.TrimRight(out.String(), "\r\n"), fieldSeparator)
	if !ok {
		return Commit{}, errors.New("unexpected git output")
	}
	return Commit{Subject: subject, Merge: len(strings.Fields(parents)) > 1}, nil
}

// cappedBuffer keeps the first limit bytes and drops the rest, so a hostile
// repository cannot make the read grow without bound.
type cappedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if room := c.limit - c.buf.Len(); room > 0 {
		c.buf.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}

func (c *cappedBuffer) String() string { return c.buf.String() }
