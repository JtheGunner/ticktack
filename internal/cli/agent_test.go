package cli

import (
	"bytes"
	"testing"
	"time"
)

func TestAgentCommandFlags(t *testing.T) {
	cmd := newAgentCommand()
	f := cmd.Flags().Lookup("interval")
	if f == nil || f.DefValue != (30*time.Second).String() {
		t.Fatalf("interval flag missing or wrong default: %+v", f)
	}
	root := NewRootCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"agent", "--interval", "0s"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected error for non-positive interval")
	}
}
