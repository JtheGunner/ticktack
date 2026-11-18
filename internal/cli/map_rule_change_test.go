package cli

import (
	"context"
	"testing"

	"github.com/JtheGunner/ticktack/internal/store"
)

func ruleChangesIn(t *testing.T, dbPath string) []store.RuleChange {
	t.Helper()
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	got, err := st.RuleChanges(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// Every successful `map add/edit/rm` leaves one rule change for the trust
// gate, stamped with the time of the change; a rejected one leaves none.
func TestMapRecordsRuleChangesForTheTrustGate(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name    string
		run     func(deps dailyDeps) error
		wantErr bool
		want    []store.RuleChangeAction
	}{
		{
			name: "add",
			run:  func(deps dailyDeps) error { return runMapAdd(ctx, deps, "/work/api", "dev", "abc-1") },
			want: []store.RuleChangeAction{store.RuleAdded},
		},
		{
			name: "edit",
			run: func(deps dailyDeps) error {
				if err := runMapAdd(ctx, deps, "/work/api", "dev", "abc-1"); err != nil {
					return err
				}
				return runMapEdit(ctx, deps, "1", mapChanges{ticket: ptrIf(true, "abc-2")})
			},
			want: []store.RuleChangeAction{store.RuleAdded, store.RuleEdited},
		},
		{
			name: "rm",
			run: func(deps dailyDeps) error {
				if err := runMapAdd(ctx, deps, "/work/api", "dev", "abc-1"); err != nil {
					return err
				}
				return runMapRemove(ctx, deps, "1")
			},
			want: []store.RuleChangeAction{store.RuleAdded, store.RuleRemoved},
		},
		{
			name:    "rejected add",
			run:     func(deps dailyDeps) error { return runMapAdd(ctx, deps, "relative/path", "", "abc-1") },
			wantErr: true,
		},
		{
			name: "edit of an unknown rule",
			run: func(deps dailyDeps) error {
				return runMapEdit(ctx, deps, "99", mapChanges{ticket: ptrIf(true, "abc-2")})
			},
			wantErr: true,
		},
		{
			name:    "rm of an unknown rule",
			run:     func(deps dailyDeps) error { return runMapRemove(ctx, deps, "99") },
			wantErr: true,
		},
		{
			name: "list",
			run:  func(deps dailyDeps) error { return runMapList(ctx, deps) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps, dbPath, _ := mapEnv(t)
			if err := tt.run(deps); (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			got := ruleChangesIn(t, dbPath)
			if len(got) != len(tt.want) {
				t.Fatalf("rule changes = %+v, want actions %v", got, tt.want)
			}
			for i, action := range tt.want {
				if got[i].Action != action || !got[i].ChangedAt.Equal(deps.Now()) {
					t.Errorf("change %d = %+v, want %s at %s", i, got[i], action, deps.Now())
				}
			}
		})
	}
}

// `ticktack switch` is information given in advance, not a correction: it
// leaves nothing the trust gate would count.
func TestSwitchRecordsNoRuleChange(t *testing.T) {
	ctx := context.Background()
	deps, dbPath, _ := mapEnv(t)
	for _, args := range [][]string{{"proj-12"}, {"auto"}} {
		if err := runSwitch(ctx, deps, args, false); err != nil {
			t.Fatal(err)
		}
	}
	if got := ruleChangesIn(t, dbPath); len(got) != 0 {
		t.Fatalf("rule changes = %+v, want none", got)
	}
}
