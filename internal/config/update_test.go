package config

import (
	"reflect"
	"strings"
	"testing"
)

// optionBlock returns the template lines of one option, newline-terminated.
func optionBlock(t *testing.T, id string) string {
	t.Helper()
	for _, option := range templateOptions() {
		if option.ID == id {
			return strings.Join(option.Lines, "\n") + "\n"
		}
	}
	t.Fatalf("no template option %q", id)
	return ""
}

// cutBetween removes the text from the first occurrence of from up to (not
// including) the first occurrence of until after it; until "" cuts to the end.
func cutBetween(t *testing.T, text, from, until string) string {
	t.Helper()
	start := strings.Index(text, from)
	if start < 0 {
		t.Fatalf("%q not found", from)
	}
	if until == "" {
		return text[:start]
	}
	end := strings.Index(text[start:], until)
	if end < 0 {
		t.Fatalf("%q not found after %q", until, from)
	}
	return text[:start] + text[start+end:]
}

func TestPlanUpdate(t *testing.T) {
	template := DefaultTOML("")
	withoutRetention := strings.Replace(template, optionBlock(t, "retention_days")+"\n", "", 1)
	activeAuto := cutBetween(t, template, "# [auto]\n", "") + "[auto]\nenabled = true\n"
	withoutActivityWatch := cutBetween(t, template, "# Optional import from a local ActivityWatch", "# Optional auto mode")
	activityWatchBlock := template[strings.Index(template, "# Optional import from a local ActivityWatch"):]
	activityWatchBlock = activityWatchBlock[:strings.Index(activityWatchBlock, "\n\n")+1]
	withoutLookback := strings.Replace(template, optionBlock(t, "auto.autopilot_lookback_days"), "", 1)
	withoutRules := cutBetween(t, template, "# Fixed repository to ticket mappings.", "# Optional import from a local ActivityWatch")
	userData := strings.NewReplacer(
		"# [app_classes]\n# \"com.tinyspeck.slackmacgap\" = \"neutral\"\n",
		"[app_classes]\n\"com.tinyspeck.slackmacgap\" = \"neutral\"\n",
		"# [[rules]]\n# repo = \"/path/to/repo\"\n# ticket = \"ABC-1\"\n",
		"[[rules]]\nrepo = \"/path/to/repo\"\nticket = \"ABC-1\"\n\n[[rules]]\nrepo = \"/path/to/other\"\nticket = \"ABC-2\"\n",
		"# [titlehint.projects]\n# \"my-ide-project\" = \"/abs/path/to/repo\"\n",
		"[titlehint.projects]\n\"my-ide-project\" = \"/abs/path/to/repo\"\n",
	).Replace(template)
	defaultRoundingLine := "# rounding = \"" + DefaultRounding.String() + "\"\n"
	const userRounding = "# keep this exact, team rule\nrounding = \"1m\" # booked as is\n"
	userValues := strings.Replace(withoutRetention, defaultRoundingLine, userRounding, 1)
	crlf := func(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }

	tests := []struct {
		name        string
		current     string
		wantAdded   []string
		wantRemoved []RemovedOption
		check       func(t *testing.T, plan UpdatePlan)
	}{
		{
			name:    "up to date",
			current: template,
			check: func(t *testing.T, plan UpdatePlan) {
				if plan.Changed() || plan.Result != plan.Original {
					t.Errorf("plan changed an up-to-date file")
				}
			},
		},
		{
			name:    "up to date with a YouTrack URL",
			current: DefaultTOML("https://example.youtrack.cloud"),
		},
		{
			name:      "missing top-level key",
			current:   withoutRetention,
			wantAdded: []string{"retention_days"},
			check: func(t *testing.T, plan UpdatePlan) {
				if plan.Result != template {
					t.Errorf("result differs from the template:\n%s", plan.Result)
				}
				block := strings.Index(plan.Result, "# retention_days")
				header := strings.Index(plan.Result, "# [default_issue]")
				if block < 0 || block > header {
					t.Errorf("block at %d, want before the first header at %d", block, header)
				}
			},
		},
		{
			name:      "missing keys in an existing active table",
			current:   activeAuto,
			wantAdded: []string{"auto.mode", "auto.trusted_days", "auto.autopilot_lookback_days"},
			check: func(t *testing.T, plan UpdatePlan) {
				want := activeAuto + "\n" + optionBlock(t, "auto.mode") + "\n" +
					optionBlock(t, "auto.trusted_days") + "\n" + optionBlock(t, "auto.autopilot_lookback_days")
				if plan.Result != want {
					t.Errorf("result =\n%s\nwant\n%s", plan.Result, want)
				}
			},
		},
		{
			name:      "missing key in a commented table",
			current:   withoutLookback,
			wantAdded: []string{"auto.autopilot_lookback_days"},
			check: func(t *testing.T, plan UpdatePlan) {
				want := withoutLookback + "\n" + optionBlock(t, "auto.autopilot_lookback_days")
				if plan.Result != want {
					t.Errorf("result =\n%s\nwant\n%s", plan.Result, want)
				}
			},
		},
		{
			name:      "missing keys go above the intro of the next table",
			current:   strings.Replace(withoutActivityWatch, "# Optional auto mode", "[activitywatch]\nenabled = true\n\n# Optional auto mode", 1),
			wantAdded: []string{"activitywatch.url", "activitywatch.hostname"},
			check: func(t *testing.T, plan UpdatePlan) {
				want := strings.Replace(withoutActivityWatch, "# Optional auto mode",
					"[activitywatch]\nenabled = true\n\n"+optionBlock(t, "activitywatch.url")+"\n"+
						optionBlock(t, "activitywatch.hostname")+"\n# Optional auto mode", 1)
				if plan.Result != want {
					t.Errorf("result =\n%s\nwant\n%s", plan.Result, want)
				}
			},
		},
		{
			name:        "removed unknown table keeps comments in its region",
			current:     template + "\n[legacy]\na = 1\n# [auto]\n# enabled = false\nb = 2\n",
			wantRemoved: []RemovedOption{{ID: "legacy", Line: strings.Count(template, "\n") + 2}},
			check: func(t *testing.T, plan UpdatePlan) {
				if plan.Result != template+"\n# [auto]\n# enabled = false\n" {
					t.Errorf("result =\n%s", plan.Result)
				}
			},
		},
		{
			name:    "indented commented key counts as present",
			current: activeAuto + "  # mode = \"gated\"\n  # trusted_days = 10\n  #autopilot_lookback_days = 7\n",
		},
		{
			name:      "missing table entirely",
			current:   withoutActivityWatch,
			wantAdded: []string{"activitywatch.enabled", "activitywatch.url", "activitywatch.hostname"},
			check: func(t *testing.T, plan UpdatePlan) {
				want := withoutActivityWatch + "\n" + activityWatchBlock
				if plan.Result != want {
					t.Errorf("result =\n%s\nwant\n%s", plan.Result, want)
				}
			},
		},
		{
			name:      "missing free-form table",
			current:   withoutRules,
			wantAdded: []string{"rules"},
			check: func(t *testing.T, plan UpdatePlan) {
				if !strings.HasSuffix(plan.Result, "\n\n"+optionBlock(t, "rules")) {
					t.Errorf("rules block not appended:\n%s", plan.Result)
				}
			},
		},
		{
			name:        "removed active key",
			current:     "legacy_option = 1\n" + template,
			wantRemoved: []RemovedOption{{ID: "legacy_option", Line: 1, Value: "1"}},
			check: func(t *testing.T, plan UpdatePlan) {
				if plan.Result != template {
					t.Errorf("result =\n%s", plan.Result)
				}
			},
		},
		{
			name:        "removed unknown table with two keys",
			current:     template + "\n[legacy]\na = 1\nb = \"x\"\n",
			wantRemoved: []RemovedOption{{ID: "legacy", Line: strings.Count(template, "\n") + 2}},
			check: func(t *testing.T, plan UpdatePlan) {
				if plan.Result != template {
					t.Errorf("result =\n%s", plan.Result)
				}
			},
		},
		{
			name:    "removed repeated unknown array table",
			current: template + "\n[[legacy]]\na = 1\n\n[[legacy]]\nb = 2\n",
			wantRemoved: []RemovedOption{
				{ID: "legacy", Line: strings.Count(template, "\n") + 2},
				{ID: "legacy", Line: strings.Count(template, "\n") + 5},
			},
			check: func(t *testing.T, plan UpdatePlan) {
				if strings.Contains(plan.Result, "legacy") || strings.Contains(plan.Result, "a = 1") {
					t.Errorf("result =\n%s", plan.Result)
				}
			},
		},
		{
			name:        "removed unknown key in a known table",
			current:     template + "\n[auto]\nenabled = true\nlegacy = 2\n",
			wantRemoved: []RemovedOption{{ID: "auto.legacy", Line: strings.Count(template, "\n") + 4, Value: "2"}},
			check: func(t *testing.T, plan UpdatePlan) {
				if plan.Result != template+"\n[auto]\nenabled = true\n" {
					t.Errorf("result =\n%s", plan.Result)
				}
			},
		},
		{
			name:        "removed key with a multi-line array of arrays",
			current:     template + "\n[auto]\nenabled = true\nlegacy = [\n  [1],\n  [2]\n]\n",
			wantRemoved: []RemovedOption{{ID: "auto.legacy", Line: strings.Count(template, "\n") + 4, Value: "["}},
			check: func(t *testing.T, plan UpdatePlan) {
				if plan.Result != template+"\n[auto]\nenabled = true\n" {
					t.Errorf("result =\n%s", plan.Result)
				}
			},
		},
		{
			name:        "removed multi-line array keeps the following key and comment",
			current:     "legacy_paths = [\n  \"a\", # note\n  [\"b\"],\n]\n# my comment\nrounding = \"1m\"\n" + template,
			wantRemoved: []RemovedOption{{ID: "legacy_paths", Line: 1, Value: "["}},
			check: func(t *testing.T, plan UpdatePlan) {
				if plan.Result != "# my comment\nrounding = \"1m\"\n"+template {
					t.Errorf("result =\n%s", plan.Result)
				}
			},
		},
		{
			name:        "removed multi-line string that looks like keys and headers",
			current:     "legacy = \"\"\"\nrounding = 1\n[auto]\n\"\"\"\n" + template,
			wantRemoved: []RemovedOption{{ID: "legacy", Line: 1, Value: `"""`}},
			check: func(t *testing.T, plan UpdatePlan) {
				if plan.Result != template {
					t.Errorf("result =\n%s", plan.Result)
				}
			},
		},
		{
			name:        "removed unknown key inside rules",
			current:     userData + "\n[[rules]]\nrepo = \"/r\"\nticket = \"ABC-3\"\nlegacy = true\n",
			wantRemoved: []RemovedOption{{ID: "rules.legacy", Line: strings.Count(userData, "\n") + 5, Value: "true"}},
		},
		{
			name:      "user values and comments preserved",
			current:   userValues,
			wantAdded: []string{"retention_days"},
			check: func(t *testing.T, plan UpdatePlan) {
				if !strings.Contains(plan.Result, userRounding) {
					t.Errorf("user lines lost:\n%s", plan.Result)
				}
				if plan.Result != strings.Replace(template, defaultRoundingLine, userRounding, 1) {
					t.Errorf("result =\n%s", plan.Result)
				}
			},
		},
		{
			name:    "app_classes, rules and project entries are never touched",
			current: userData,
		},
		{
			name:      "CRLF input",
			current:   crlf(withoutRetention),
			wantAdded: []string{"retention_days"},
			check: func(t *testing.T, plan UpdatePlan) {
				if plan.Result != crlf(template) {
					t.Errorf("result =\n%q", plan.Result)
				}
			},
		},
		{
			name:    "empty file",
			current: "",
			wantAdded: func() []string {
				var ids []string
				for _, option := range templateOptions() {
					ids = append(ids, option.ID)
				}
				return ids
			}(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := PlanUpdate(tt.current)
			if err != nil {
				t.Fatalf("PlanUpdate: %v", err)
			}
			if plan.Original != tt.current {
				t.Errorf("Original is not the input")
			}
			var added []string
			for _, option := range plan.Added {
				added = append(added, option.ID)
				if len(option.Lines) == 0 {
					t.Errorf("added %q without lines", option.ID)
				}
			}
			if !reflect.DeepEqual(added, tt.wantAdded) {
				t.Errorf("added = %v, want %v", added, tt.wantAdded)
			}
			if !reflect.DeepEqual(plan.Removed, tt.wantRemoved) {
				t.Errorf("removed = %+v, want %+v", plan.Removed, tt.wantRemoved)
			}
			wantChanged := len(tt.wantAdded)+len(tt.wantRemoved) > 0
			if plan.Changed() != wantChanged {
				t.Errorf("Changed() = %v, want %v", plan.Changed(), wantChanged)
			}
			if _, err := Parse([]byte(plan.Result)); err != nil {
				t.Errorf("result does not parse: %v", err)
			}
			if tt.check != nil {
				tt.check(t, plan)
			}
			again, err := PlanUpdate(plan.Result)
			if err != nil {
				t.Fatalf("second PlanUpdate: %v", err)
			}
			if again.Changed() {
				t.Errorf("second run not idempotent: added %+v removed %+v", again.Added, again.Removed)
			}
		})
	}
}

func TestPlanUpdateErrors(t *testing.T) {
	template := DefaultTOML("")
	tests := []struct {
		name    string
		current string
	}{
		{"invalid value", "rounding = \"7s\"\n" + template},
		{"syntax error", "rounding = \n" + template},
		{"wrong type", "rounding = 5\n" + template},
		{"unknown key inside an inline table", "auto = { enabled = true, legacy = 1 }\n" + template},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := PlanUpdate(tt.current); err == nil {
				t.Fatal("PlanUpdate returned no error")
			}
		})
	}
}

func TestPlanUpdateDoesNotEchoValues(t *testing.T) {
	_, err := PlanUpdate("rounding = \"secret-7s\"\nx = \n")
	if err == nil {
		t.Fatal("PlanUpdate returned no error")
	}
	if strings.Contains(err.Error(), "secret") {
		t.Errorf("error echoes the source: %v", err)
	}
}
