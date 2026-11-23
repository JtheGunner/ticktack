package config

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

// collectConfigKeys lists the option IDs a struct of toml-tagged fields
// declares. Maps and slices of structs are free-form tables and count as one
// option (they are not structs); nested structs are walked as table.key.
func collectConfigKeys(t reflect.Type, prefix string) []string {
	var ids []string
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("toml")
		if tag == "" {
			continue
		}
		id := prefix + tag
		ft := t.Field(i).Type
		for ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		switch {
		case ft.Kind() == reflect.Struct:
			ids = append(ids, collectConfigKeys(ft, id+".")...)
		default:
			ids = append(ids, id)
		}
	}
	return ids
}

func TestTemplateOptionsCoverEveryConfigKey(t *testing.T) {
	want := collectConfigKeys(reflect.TypeOf(fileConfig{}), "")
	var got []string
	for _, option := range templateOptions() {
		got = append(got, option.ID)
	}
	sort.Strings(want)
	sort.Strings(got)
	if !reflect.DeepEqual(want, got) {
		t.Errorf("template options differ from fileConfig keys:\n got  %v\n want %v", got, want)
	}
}

func TestEveryFixedOptionHasItsOwnDescription(t *testing.T) {
	for _, option := range templateOptions() {
		if option.Key == "" {
			continue
		}
		t.Run(option.ID, func(t *testing.T) {
			if len(option.Lines) < 2 {
				t.Fatalf("lines = %q, want description comments and a key line", option.Lines)
			}
			if !strings.HasPrefix(option.Lines[0], "#") {
				t.Errorf("first line %q is not a description comment", option.Lines[0])
			}
			last := option.Lines[len(option.Lines)-1]
			if !strings.Contains(last, option.Key) {
				t.Errorf("last line %q is not the key line of %q", last, option.Key)
			}
		})
	}
}

func TestParseLinesClassifiesActiveAndCommented(t *testing.T) {
	tests := []struct {
		name string
		line string
		want lineEntry
	}{
		{"active key", `gap_threshold = "5m"`, lineEntry{Kind: kindKey, Active: true, Key: "gap_threshold"}},
		{"commented key", `# gap_threshold = "5m"`, lineEntry{Kind: kindKey, Key: "gap_threshold"}},
		{"active header", `[auto]`, lineEntry{Kind: kindHeader, Active: true, Table: "auto"}},
		{"commented header", `# [auto]`, lineEntry{Kind: kindHeader, Table: "auto"}},
		{"prose with brackets", `# plain prose [x] here`, lineEntry{Kind: kindComment}},
		{"blank", ``, lineEntry{Kind: kindBlank}},
		{"array header", `[[rules]]`, lineEntry{Kind: kindHeader, Active: true, Table: "rules"}},
		{"commented array header", `# [[rules]]`, lineEntry{Kind: kindHeader, Table: "rules"}},
		{"indented key", `  enabled = true`, lineEntry{Kind: kindKey, Active: true, Key: "enabled"}},
		{"indented header", "\t[auto]", lineEntry{Kind: kindHeader, Active: true, Table: "auto"}},
		{"carriage return", "gap_threshold = \"5m\"\r", lineEntry{Kind: kindKey, Active: true, Key: "gap_threshold"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseLines(tt.line)
			if len(got) != 1 {
				t.Fatalf("entries = %d, want 1", len(got))
			}
			if got[0] != tt.want {
				t.Errorf("entry = %+v, want %+v", got[0], tt.want)
			}
		})
	}
}

func TestParseLinesTracksTableForIndentedLines(t *testing.T) {
	got := parseLines("  [auto]\n  enabled = true\n  # mode = \"gated\"")
	want := []lineEntry{
		{Index: 0, Kind: kindHeader, Active: true, Table: "auto"},
		{Index: 1, Kind: kindKey, Active: true, Table: "auto", Key: "enabled"},
		{Index: 2, Kind: kindComment},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("entries = %+v, want %+v", got, want)
	}
}
