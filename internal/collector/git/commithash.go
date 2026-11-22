package git

import (
	"regexp"
	"strings"

	"github.com/JtheGunner/ticktack/internal/event"
)

// objectID matches a full SHA-1 or SHA-256 object id in lower case.
var objectID = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// IsObjectID reports whether s is a full hexadecimal git object id. A hash
// that comes out of the database is data, so it is checked before it is given
// to git.
func IsObjectID(s string) bool { return objectID.MatchString(s) }

// CommitHash returns the hash of the commit a scanner event stands for. The
// key of a commit event is git:<repo>:commit:<unix>:<hash>:<branch> (see
// buildEvents); the repository is taken from the event, because a path may
// hold colons. It reports false for every other event and for a key whose
// hash is not a full object id.
func CommitHash(e event.Event) (string, bool) {
	if e.Source != Source || e.Repo == "" {
		return "", false
	}
	rest, ok := strings.CutPrefix(e.Key, "git:"+e.Repo+":commit:")
	if !ok {
		return "", false
	}
	// rest is <unix>:<hash>:<branch>; a branch name cannot hold a colon.
	parts := strings.SplitN(rest, ":", 3)
	if len(parts) < 2 || !IsObjectID(parts[1]) {
		return "", false
	}
	return parts[1], true
}
