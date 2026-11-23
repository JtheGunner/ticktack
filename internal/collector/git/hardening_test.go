package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// signedLookingCommit makes HEAD a commit that carries a gpgsig header, so a
// log with signature checking asks the configured gpg program to verify it.
func signedLookingCommit(t *testing.T, repo string) {
	t.Helper()
	when := at(2, 9)
	commit(t, repo, when, "a.txt")
	raw := run(t, repo, when, "cat-file", "commit", "HEAD")
	head, body, _ := strings.Cut(raw, "\n\n")
	signed := head + "\ngpgsig -----BEGIN PGP SIGNATURE-----\n \n fake\n -----END PGP SIGNATURE-----\n\n" + body + "\n"

	cmd := exec.Command("git", "-C", repo, "hash-object", "-t", "commit", "-w", "--stdin")
	cmd.Env = os.Environ()
	cmd.Stdin = strings.NewReader(signed)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("hash-object: %v\n%s", err, out)
	}
	run(t, repo, when, "reset", "-q", "--soft", strings.TrimSpace(string(out)))
}

// markerProgram writes a script that creates a marker file when executed and
// returns the script and marker paths.
func markerProgram(t *testing.T) (program, marker string) {
	t.Helper()
	dir := t.TempDir()
	marker = filepath.Join(dir, "executed")
	program = filepath.Join(dir, "evil.sh")
	script := "#!/bin/sh\ntouch '" + marker + "'\nexit 1\n"
	if err := os.WriteFile(program, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return program, marker
}

func TestScanNeverRunsRepositoryConfiguredPrograms(t *testing.T) {
	repo := newRepo(t)
	signedLookingCommit(t, repo)
	program, marker := markerProgram(t)
	run(t, repo, at(2, 9), "config", "log.showSignature", "true")
	run(t, repo, at(2, 9), "config", "gpg.program", program)
	run(t, repo, at(2, 9), "config", "core.fsmonitor", program)

	// Control: the repository really makes plain git run the program.
	run(t, repo, at(2, 9), "log", "-1", "--format=%H", "HEAD")
	if _, err := os.Stat(marker); err != nil {
		t.Skipf("this git does not run gpg.program for log.showSignature: %v", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}

	if _, err := Scan(context.Background(), repo, time.Time{}, time.Time{}, nil); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("scan executed a program configured by the repository")
	}
}
