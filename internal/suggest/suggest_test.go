package suggest

import (
	"reflect"
	"testing"

	"github.com/JtheGunner/ticktack/internal/youtrack"
)

func keys(c []Candidate) []string {
	var out []string
	for _, x := range c {
		out = append(out, x.Key)
	}
	return out
}

func TestRankTable(t *testing.T) {
	issues := []youtrack.Issue{
		{ID: "ABC-1", Updated: 100},
		{ID: "ABC-12", Updated: 300},
		{ID: "DEF-2", Updated: 200},
		{ID: "abc-1", Updated: 999}, // duplicate of ABC-1 (first wins)
		{ID: "", Updated: 5},
	}
	tests := []struct {
		name, hint string
		want       []string
	}{
		{"no hint orders by recency", "", []string{"ABC-12", "DEF-2", "ABC-1"}},
		{"key in branch first", "/work/app feature/def-2-x", []string{"DEF-2", "ABC-12", "ABC-1"}},
		{"key must not match longer key", "/work/app abc-123", []string{"ABC-12", "DEF-2", "ABC-1"}},
		{"exact key at end", "x/abc-1", []string{"ABC-1", "ABC-12", "DEF-2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := keys(Rank(issues, tt.hint)); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRankTiesAndLimit(t *testing.T) {
	var issues []youtrack.Issue
	for _, k := range []string{"G-7", "G-6", "G-5", "G-4", "G-3", "G-2", "G-1"} {
		issues = append(issues, youtrack.Issue{ID: k, Updated: 1})
	}
	got := keys(Rank(issues, ""))
	want := []string{"G-1", "G-2", "G-3", "G-4", "G-5"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if Rank(nil, "x") != nil && len(Rank(nil, "x")) != 0 {
		t.Error("expected empty result")
	}
}
