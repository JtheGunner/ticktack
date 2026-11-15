package title

import (
	"reflect"
	"regexp"
	"testing"
)

func TestParse(t *testing.T) {
	custom := regexp.MustCompile(`TT-\d+`)
	tests := []struct {
		name    string
		app     string
		title   string
		pattern *regexp.Regexp
		want    Hint
	}{
		{"unknown app is ignored", "com.example.notes", "PROJ-1 secret — proj", nil, Hint{}},
		{"empty title", "com.jetbrains.goland", "", nil, Hint{}},
		{"empty app", "", "proj – main.go", nil, Hint{}},
		{"jetbrains project and file", "com.jetbrains.goland", "ticktack – main.go", nil, Hint{Project: "ticktack"}},
		{"jetbrains with path", "com.jetbrains.intellij", "my-app [~/Projects/my-app] – Main.java", nil, Hint{Project: "my-app"}},
		{"jetbrains hyphen separator", "com.jetbrains.PhpStorm", "shop - index.php", nil, Hint{Project: "shop"}},
		{"jetbrains project only", "com.jetbrains.goland", "ticktack", nil, Hint{Project: "ticktack"}},
		{"android studio", "com.google.android.studio", "app – MainActivity.kt", nil, Hint{Project: "app"}},
		{"vscode file project app", "com.microsoft.VSCode", "main.go — ticktack — Visual Studio Code", nil, Hint{Project: "ticktack"}},
		{"vscode dirty marker", "com.microsoft.VSCode", "● main.go — ticktack — Visual Studio Code", nil, Hint{Project: "ticktack"}},
		{"vscode no file", "com.microsoft.VSCode", "ticktack — Visual Studio Code", nil, Hint{Project: "ticktack"}},
		{"vscode workspace suffix", "com.microsoft.VSCodeInsiders", "a.go — mono (Workspace) — Visual Studio Code - Insiders", nil, Hint{Project: "mono"}},
		{"vscode welcome tab is no project", "com.microsoft.VSCode", "Welcome — Visual Studio Code", nil, Hint{}},
		{"xcode", "com.apple.dt.Xcode", "Ticker — AppDelegate.swift", nil, Hint{Project: "Ticker"}},
		{"unicode project", "com.jetbrains.goland", "Übungsprojekt – größe.go", nil, Hint{Project: "Übungsprojekt"}},
		{"unicode no match", "com.jetbrains.goland", "日本語 – 한국어.go", nil, Hint{Project: "日本語"}},
		{"ticket in IDE title", "com.jetbrains.goland", "PROJ-12 – main.go", nil, Hint{Ticket: "PROJ-12", Project: "PROJ-12"}},
		{"youtrack tab in browser", "com.google.Chrome", "PROJ-123 Fix the login - YouTrack", nil, Hint{Ticket: "PROJ-123"}},
		{"safari youtrack tab", "com.apple.Safari", "ABC-7: Title – JetBrains YouTrack", nil, Hint{Ticket: "ABC-7"}},
		{"browser without youtrack marker", "com.google.Chrome", "Re: see PROJ-123 in the thread - Mail", nil, Hint{}},
		{"ticket only inside a url", "com.google.Chrome", "https://example.com/PROJ-123/edit - YouTrack", nil, Hint{}},
		{"url ignored but sentence kept", "com.google.Chrome", "https://x.io/ABC-1 vs PROJ-2 - YouTrack", nil, Hint{Ticket: "PROJ-2"}},
		{"custom ticket pattern", "com.google.Chrome", "TT-5 thing - YouTrack", custom, Hint{Ticket: "TT-5"}},
		{"custom pattern rejects default shape", "com.google.Chrome", "PROJ-5 thing - YouTrack", custom, Hint{}},
		{"browser title without ticket", "org.mozilla.firefox", "Dashboard - YouTrack", nil, Hint{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Parse(tc.app, tc.title, tc.pattern); got != tc.want {
				t.Errorf("Parse(%q, %q) = %+v, want %+v", tc.app, tc.title, got, tc.want)
			}
		})
	}
}

func TestHintCannotHoldRawTitleText(t *testing.T) {
	typ := reflect.TypeOf(Hint{})
	allowed := map[string]bool{"Ticket": true, "Project": true}
	for i := 0; i < typ.NumField(); i++ {
		if !allowed[typ.Field(i).Name] {
			t.Errorf("Hint has unexpected field %q; only Ticket and Project are allowed", typ.Field(i).Name)
		}
	}
}

func TestHintIsZeroDetection(t *testing.T) {
	if !(Hint{}).IsZero() || (Hint{Ticket: "A-1"}).IsZero() || (Hint{Project: "p"}).IsZero() {
		t.Error("IsZero misreports")
	}
}
