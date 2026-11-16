package title

import (
	"errors"
	"strings"
	"testing"
)

func TestParseFrontmostOutput(t *testing.T) {
	tests := []struct {
		name    string
		out     string
		want    Window
		wantErr bool
	}{
		{"app and title", "com.jetbrains.goland\nproj – main.go\n", Window{App: "com.jetbrains.goland", Title: "proj – main.go"}, false},
		{"no window", "com.apple.finder\n\n", Window{App: "com.apple.finder"}, false},
		{"title with line feed keeps the rest", "a.b\nx\ny", Window{App: "a.b", Title: "x\ny"}, false},
		{"empty output", "", Window{}, true},
		{"missing bundle id", "\ntitle\n", Window{}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseFrontmostOutput(tc.out)
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Errorf("got %+v, %v; want %+v (err %v)", got, err, tc.want, tc.wantErr)
			}
		})
	}
}

func TestParseFrontmostErrorsNeverContainOutput(t *testing.T) {
	_, err := parseFrontmostOutput("\nSECRET-TITLE")
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("err = %v", err)
	}
}

func TestClassifyFailure(t *testing.T) {
	runErr := errors.New("exit status 1")
	for _, stderr := range []string{
		"execution error: osascript is not allowed assistive access. (-25211)",
		"Not authorized to send Apple events to System Events. (-1743)",
	} {
		if err := classifyFailure(stderr, runErr); !errors.Is(err, ErrPermission) {
			t.Errorf("stderr %q: err = %v, want ErrPermission", stderr, err)
		}
	}
	err := classifyFailure("some SECRET-TITLE stderr", runErr)
	if err == nil || errors.Is(err, ErrPermission) || strings.Contains(err.Error(), "SECRET") {
		t.Errorf("generic failure mishandled: %v", err)
	}
}
