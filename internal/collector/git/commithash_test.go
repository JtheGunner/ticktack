package git

import (
	"strings"
	"testing"

	"github.com/JtheGunner/ticktack/internal/event"
)

func TestCommitHash(t *testing.T) {
	hash := strings.Repeat("ab", 20)
	tests := []struct {
		name string
		e    event.Event
		want string
		ok   bool
	}{
		{"commit event", event.Event{Source: Source, Repo: "/r", Key: "git:/r:commit:100:" + hash + ":feat/ABC-1"}, hash, true},
		{"empty branch", event.Event{Source: Source, Repo: "/r", Key: "git:/r:commit:100:" + hash + ":"}, hash, true},
		{"repo path with colon", event.Event{Source: Source, Repo: "/a:b", Key: "git:/a:b:commit:100:" + hash + ":main"}, hash, true},
		{"checkout event", event.Event{Source: Source, Repo: "/r", Key: "git:/r:checkout:100:" + hash + ":main"}, "", false},
		{"other source", event.Event{Source: "shell", Repo: "/r", Key: "git:/r:commit:100:" + hash + ":main"}, "", false},
		{"hash is not an object id", event.Event{Source: Source, Repo: "/r", Key: "git:/r:commit:100:--output=x:main"}, "", false},
		{"short hash", event.Event{Source: Source, Repo: "/r", Key: "git:/r:commit:100:abc:main"}, "", false},
		{"other repo in key", event.Event{Source: Source, Repo: "/r", Key: "git:/x:commit:100:" + hash + ":main"}, "", false},
		{"no key", event.Event{Source: Source, Repo: "/r"}, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := CommitHash(tc.e)
			if got != tc.want || ok != tc.ok {
				t.Errorf("CommitHash = %q, %v; want %q, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}
