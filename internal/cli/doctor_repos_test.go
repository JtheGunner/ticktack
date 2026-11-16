package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func writeRepoPathsConfig(t *testing.T, body string) {
	t.Helper()
	cfgPath := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "ticktack", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func tomlList(paths ...string) string {
	quoted := make([]string, len(paths))
	for i, p := range paths {
		quoted[i] = strconv.Quote(p)
	}
	return fmt.Sprintf("repo_paths = [%s]\n", strings.Join(quoted, ", "))
}

func TestCheckRepositoriesIsSkippedWithoutRepoPaths(t *testing.T) {
	env := newTestEnv(t, "", "")
	res := checkRepositories(context.Background(), env.deps.Doctor)
	if res.Status != statusSkip {
		t.Fatalf("result = %+v, want skip", res)
	}
}

func TestCheckRepositoriesIsSkippedWhenConfigIsBroken(t *testing.T) {
	env := newTestEnv(t, "", "")
	writeRepoPathsConfig(t, "repo_paths = [\"relative\"]\n")
	if res := checkRepositories(context.Background(), env.deps.Doctor); res.Status != statusSkip {
		t.Fatalf("result = %+v, want skip", res)
	}
}

func TestCheckRepositoriesAcceptsReadableRepositories(t *testing.T) {
	env := newTestEnv(t, "", "")
	repo := fakeRepoDir(t)
	linked := t.TempDir()
	if err := os.WriteFile(filepath.Join(linked, ".git"), []byte("gitdir: /elsewhere"), 0o600); err != nil {
		t.Fatal(err)
	}
	home := fakeRepoDir(t)
	env.deps.Doctor.Home = filepath.Dir(home)
	writeRepoPathsConfig(t, tomlList(repo, linked, "~/"+filepath.Base(home)))
	res := checkRepositories(context.Background(), env.deps.Doctor)
	if res.Status != statusOK || !strings.Contains(res.Detail, "3 repositories") {
		t.Fatalf("result = %+v", res)
	}
}

func TestCheckRepositoriesNamesEachUnusablePath(t *testing.T) {
	env := newTestEnv(t, "", "")
	good := fakeRepoDir(t)
	parent := t.TempDir()
	aFile := filepath.Join(parent, "afile")
	if err := os.WriteFile(aFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(parent, "plain")
	if err := os.Mkdir(plain, 0o700); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(parent, "missing")
	writeRepoPathsConfig(t, tomlList(good, missing, aFile, plain))

	var out bytes.Buffer
	res := checkRepositories(context.Background(), env.deps.Doctor)
	printResult(&out, res)
	text := out.String()
	if res.Status != statusFail {
		t.Fatalf("result = %+v", res)
	}
	for _, want := range []string{
		"3 of 4 repositories unusable",
		missing + " does not exist",
		aFile + " is not a directory",
		plain + " is not the root of a git repository",
		"     fix: ",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, good+" ") {
		t.Errorf("healthy path reported as a problem:\n%s", text)
	}
}

func TestCheckRepositoriesReportsUnreadableDirectory(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("permissions are not enforced for root")
	}
	env := newTestEnv(t, "", "")
	repo := fakeRepoDir(t)
	if err := os.Chmod(repo, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(repo, 0o700) })
	writeRepoPathsConfig(t, tomlList(repo))
	res := checkRepositories(context.Background(), env.deps.Doctor)
	if res.Status != statusFail || !strings.Contains(res.Detail, "cannot be read") || res.Hint == "" {
		t.Fatalf("result = %+v", res)
	}
}

func TestDoctorSanitisesRepositoryPaths(t *testing.T) {
	env := newTestEnv(t, "", "")
	writeRepoPathsConfig(t, tomlList(filepath.Join(t.TempDir(), "gone\x1b[31m")))
	var out bytes.Buffer
	_ = runDoctor(context.Background(), env.deps.Doctor, &out)
	if strings.Contains(out.String(), "\x1b") || !strings.Contains(out.String(), "FAIL git repositories:") {
		t.Fatalf("unsafe or missing output:\n%q", out.String())
	}
}
