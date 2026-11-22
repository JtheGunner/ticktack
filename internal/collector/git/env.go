package git

import (
	"os"
	"strings"
)

// repositoryEnv lists the variables that point git at a repository, work tree
// or object store regardless of the directory it runs in. A process started
// from a git hook, an alias or `git rebase --exec` inherits them, and they
// would win over `-C <dir>` and attribute events to the wrong repository.
var repositoryEnv = map[string]bool{
	"GIT_DIR":                          true,
	"GIT_WORK_TREE":                    true,
	"GIT_INDEX_FILE":                   true,
	"GIT_COMMON_DIR":                   true,
	"GIT_OBJECT_DIRECTORY":             true,
	"GIT_ALTERNATE_OBJECT_DIRECTORIES": true,
}

// CommandEnv returns the environment for a read-only git child process: the
// current environment without the variables that select a repository, plus
// GIT_OPTIONAL_LOCKS=0 so the call never blocks other git work, and two
// settings that keep git off the network: GIT_NO_LAZY_FETCH=1 makes a missing
// object of a partial clone a read error instead of a fetch from the promisor
// remote, and GIT_TERMINAL_PROMPT=0 stops credential prompts. ticktack talks
// only to YouTrack and ActivityWatch. Every git call of ticktack that names
// its directory with `-C` must use it.
func CommandEnv() []string {
	return commandEnv(os.Environ())
}

func commandEnv(environ []string) []string {
	env := make([]string, 0, len(environ)+3)
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if repositoryEnv[name] {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "GIT_OPTIONAL_LOCKS=0", "GIT_NO_LAZY_FETCH=1", "GIT_TERMINAL_PROMPT=0")
}
