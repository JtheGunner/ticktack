package cli

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/JtheGunner/ticktack/internal/shellhook"
)

// aliasPlan is the decision `init` takes about the optional tt alias.
type aliasPlan struct {
	// Line is the alias definition to put into the managed block; empty when
	// no alias is written.
	Line string
	// Skipped is true when the user passed --skip-alias.
	Skipped bool
	// Conflict names what already provides `tt` (a command on PATH or a
	// definition in the rc file); empty when nothing was found.
	Conflict string
}

// userDefinesTT matches a `tt` alias or function definition at the start of a
// line in zsh, bash or fish syntax. Comment lines never match.
var userDefinesTT = regexp.MustCompile(
	`^\s*(?:alias\s+(?:-\S+\s+)*tt(?:=|\s|$)|function\s+tt(?:\s|\(|$)|tt\s*\(\))`)

// rcDefinesTT reports whether the rc file content defines an alias or function
// named tt outside the managed block. Only this text match is made: the file
// is read, never executed, so an alias that is created some other way (another
// file the rc sources, a plugin, the current session) is not seen.
func rcDefinesTT(content string) bool {
	if rest, removed, err := removeManagedBlock(content); err == nil && removed {
		content = rest
	}
	for _, line := range strings.Split(content, "\n") {
		if userDefinesTT.MatchString(line) {
			return true
		}
	}
	return false
}

// findTTConflict returns a description of what already provides `tt`, or "".
// A command is looked up on PATH; an alias or function only in the rc content
// passed in. Nothing is executed.
func findTTConflict(lookPath func(string) (string, error), rcContent string) string {
	if path, err := lookPath(shellhook.AliasName); err == nil {
		return fmt.Sprintf(msgAliasConflictCommand, path)
	}
	if rcDefinesTT(rcContent) {
		return msgAliasConflictRC
	}
	return ""
}

// planAlias decides whether the managed block of the given shell gets the tt
// alias. The decision is recomputed on every run, so rerunning `init` rewrites
// the block to match it: an older block without the alias gains it, and a
// block with it loses it once --skip-alias is given or `tt` is taken.
func planAlias(skip bool, shell, rc string, lookPath func(string) (string, error)) (aliasPlan, error) {
	if skip {
		return aliasPlan{Skipped: true}, nil
	}
	line, err := shellhook.AliasLine(shell)
	if err != nil {
		return aliasPlan{}, err
	}
	existing, err := os.ReadFile(rc)
	if err != nil && !os.IsNotExist(err) {
		return aliasPlan{}, fmt.Errorf("read %s: %w", rc, err)
	}
	if conflict := findTTConflict(lookPath, string(existing)); conflict != "" {
		return aliasPlan{Conflict: conflict}, nil
	}
	return aliasPlan{Line: line}, nil
}

// withAlias appends the alias line to the hook script of the managed block.
func (p aliasPlan) withAlias(script string) string {
	if p.Line == "" {
		return script
	}
	return strings.TrimRight(script, "\n") + "\n" + p.Line + "\n"
}

// managedBlockOf returns the text of the managed block in content.
func managedBlockOf(content string) (string, bool) {
	begin := strings.Index(content, rcBlockBegin)
	if begin < 0 {
		return "", false
	}
	endRel := strings.Index(content[begin:], rcBlockEnd)
	if endRel < 0 {
		return "", false
	}
	return content[begin : begin+endRel], true
}

// blockHasAlias reports whether the managed block in content defines the tt
// alias in either shell syntax.
func blockHasAlias(content string) bool {
	block, ok := managedBlockOf(content)
	if !ok {
		return false
	}
	for _, shell := range []string{"zsh", "fish"} {
		line, _ := shellhook.AliasLine(shell)
		for _, l := range strings.Split(block, "\n") {
			if strings.TrimSpace(l) == line {
				return true
			}
		}
	}
	return false
}

// checkAlias reports the state of the tt alias as an informational line. It
// never fails: the alias is optional.
func checkAlias(_ context.Context, deps doctorDeps) checkResult {
	rc, installed := installedRCFile(deps)
	if !installed {
		return infoResult(msgCheckAlias, msgAliasNoBlock)
	}
	data, err := os.ReadFile(rc)
	if err != nil {
		return infoResult(msgCheckAlias, fmt.Sprintf(msgAliasUnreadable, rc))
	}
	if blockHasAlias(string(data)) {
		return infoResult(msgCheckAlias, fmt.Sprintf(msgAliasSet, shellhook.AliasName, rc))
	}
	if conflict := findTTConflict(deps.LookPath, string(data)); conflict != "" {
		return infoResult(msgCheckAlias, fmt.Sprintf(msgAliasSkippedConflict, shellhook.AliasName, conflict))
	}
	return infoResult(msgCheckAlias, fmt.Sprintf(msgAliasNotSet, shellhook.AliasName))
}
