package cli

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JtheGunner/ticktack/internal/store"
)

// purgePromptWording maps every data table to the words the confirmation
// question uses for it.
var purgePromptWording = map[string]string{
	"events":               "events",
	"overrides":            "overrides",
	"ledger":               "booking ledger",
	"sync_runs":            "sync history",
	"sync_entries":         "sync history",
	"confirmations":        "review confirmations",
	"cap_acknowledgements": "cap acknowledgements",
	"learned_rules":        "learned rules",
	"rule_changes":         "their change log",
	"review_log":           "review log",
	"digests":              "digests",
	"youtrack_projects":    "cached YouTrack project list",
	"default_tasks":        "standard tasks",
	"default_task_consent": "consent to create them",
	"archived_days":        "list of archived days",
	"autopilot_holds":      "autopilot holds",
}

// The question names everything purge deletes. The tables are read from the
// schema, so a new table fails here until the question mentions it.
func TestPurgePromptNamesEveryPurgedTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table'`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	seen := 0
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatal(err)
		}
		if table == "schema_migrations" || strings.HasPrefix(table, "sqlite_") {
			continue
		}
		seen++
		wording, known := purgePromptWording[table]
		if !known {
			t.Errorf("table %s is purged but the question has no wording for it", table)
			continue
		}
		if !strings.Contains(msgPurgePrompt, wording) {
			t.Errorf("the question does not mention %q (table %s): %q", wording, table, msgPurgePrompt)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if seen != len(purgePromptWording) {
		t.Errorf("schema has %d data tables, the wording map names %d: drop the stale entries", seen, len(purgePromptWording))
	}
}
