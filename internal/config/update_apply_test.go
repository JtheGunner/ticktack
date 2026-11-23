package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s mode = %o, want %o", path, got, want)
	}
}

func assertNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "config.toml" && e.Name() != "config.toml.bak" {
			t.Errorf("unexpected leftover file %q", e.Name())
		}
	}
}

func TestApplyUpdate(t *testing.T) {
	t.Run("writes result and backup", func(t *testing.T) {
		path := writeConfig(t, "a = 1\n")
		plan := UpdatePlan{Original: "a = 1\n", Result: "a = 1\n# b = 2\n"}
		if err := ApplyUpdate(path, plan); err != nil {
			t.Fatal(err)
		}
		if got := readFile(t, path); got != plan.Result {
			t.Errorf("config = %q, want %q", got, plan.Result)
		}
		if got := readFile(t, path+".bak"); got != plan.Original {
			t.Errorf("backup = %q, want %q", got, plan.Original)
		}
		assertMode(t, path, 0o600)
		assertMode(t, path+".bak", 0o600)
		assertNoTempFiles(t, filepath.Dir(path))
	})

	t.Run("second apply overwrites backup", func(t *testing.T) {
		path := writeConfig(t, "a = 1\n")
		first := UpdatePlan{Original: "a = 1\n", Result: "a = 2\n"}
		second := UpdatePlan{Original: "a = 2\n", Result: "a = 3\n"}
		if err := ApplyUpdate(path, first); err != nil {
			t.Fatal(err)
		}
		if err := ApplyUpdate(path, second); err != nil {
			t.Fatal(err)
		}
		if got := readFile(t, path+".bak"); got != "a = 2\n" {
			t.Errorf("backup = %q, want the second original", got)
		}
		if got := readFile(t, path); got != "a = 3\n" {
			t.Errorf("config = %q", got)
		}
	})

	t.Run("file changed since planning", func(t *testing.T) {
		path := writeConfig(t, "a = 1\n# edited\n")
		plan := UpdatePlan{Original: "a = 1\n", Result: "a = 2\n"}
		err := ApplyUpdate(path, plan)
		if err == nil || !strings.Contains(err.Error(), "changed while updating") {
			t.Fatalf("err = %v, want changed while updating", err)
		}
		if got := readFile(t, path); got != "a = 1\n# edited\n" {
			t.Errorf("config modified: %q", got)
		}
		if _, statErr := os.Stat(path + ".bak"); !os.IsNotExist(statErr) {
			t.Errorf("backup must not exist, stat err = %v", statErr)
		}
		assertNoTempFiles(t, filepath.Dir(path))
	})

	t.Run("no change writes nothing", func(t *testing.T) {
		path := writeConfig(t, "a = 1\n")
		if err := ApplyUpdate(path, UpdatePlan{Original: "a = 1\n", Result: "a = 1\n"}); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
			t.Errorf("backup must not exist, stat err = %v", err)
		}
		assertNoTempFiles(t, filepath.Dir(path))
	})

	t.Run("missing file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.toml")
		if err := ApplyUpdate(path, UpdatePlan{Original: "a = 1\n", Result: "a = 2\n"}); err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("unwritable directory", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("directory permission bits are not enforced on Windows")
		}
		if os.Geteuid() == 0 {
			t.Skip("running as root: directory permissions are not enforced")
		}
		path := writeConfig(t, "a = 1\n")
		dir := filepath.Dir(path)
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

		err := ApplyUpdate(path, UpdatePlan{Original: "a = 1\n", Result: "a = 2\n"})
		if err == nil {
			t.Fatal("expected an error")
		}
		if got := readFile(t, path); got != "a = 1\n" {
			t.Errorf("config modified: %q", got)
		}
	})
}
