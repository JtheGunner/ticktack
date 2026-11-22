// Package commitsubject builds the description of a work item from the
// subjects of the commits made while the time was running.
//
// The subjects are read from the repository at booking time, with the local
// git binary, and are never stored on their own: they only travel inside the
// booked text, which is kept in the sync history for undo. Events carry no
// text. Nothing here talks to the network.
package commitsubject

import (
	"context"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/JtheGunner/ticktack/internal/sessionizer"
)

const (
	// MaxSubjects is the number of subjects named in a description.
	MaxSubjects = 5
	// MaxSubjectRunes caps one subject.
	MaxSubjectRunes = 200
	// maxCommitsRead bounds the repository reads for one item.
	maxCommitsRead = 100

	ellipsis      = "…"
	separator     = "; "
	markerPrefix  = "[ticktack:"
	markerReplace = "(ticktack:"
)

// Commit is what a reader reports about one commit.
type Commit struct {
	Subject string
	// Merge is set for a commit with more than one parent.
	Merge bool
}

// Reader reads one commit from a repository. An error means the commit cannot
// be read (repository gone, hash unknown, git missing); the commit is then
// left out of the description.
type Reader interface {
	Read(ctx context.Context, repo, hash string) (Commit, error)
}

// Describer builds descriptions from commit subjects. The zero value is not
// usable: Reader is required.
type Describer struct {
	Reader Reader
}

// Describe returns the description of it: the unique subjects of its commits
// in the order the commits were made, at most MaxSubjects of them, followed
// by "+N more" when there are others. Merge commits and fixup!, squash! and
// amend! commits are left out. When nothing is left, or no commit can be
// read, it returns the item's own description.
func (d Describer) Describe(ctx context.Context, it sessionizer.DayItem) string {
	var subjects []string
	seen := map[string]bool{}
	for i, ref := range it.Commits {
		if i >= maxCommitsRead || ctx.Err() != nil {
			break
		}
		c, err := d.Reader.Read(ctx, ref.Repo, ref.Hash)
		if err != nil || c.Merge {
			continue
		}
		subject := Clean(c.Subject)
		if subject == "" || isAutosquash(subject) || seen[subject] {
			continue
		}
		seen[subject] = true
		subjects = append(subjects, subject)
	}
	if len(subjects) == 0 {
		return it.Description
	}
	shown := subjects
	if len(shown) > MaxSubjects {
		shown = shown[:MaxSubjects]
	}
	text := strings.Join(shown, separator)
	if more := len(subjects) - len(shown); more > 0 {
		text += fmt.Sprintf(separator+msgMore, more)
	}
	return text
}

// msgMore is the user-facing tail of a description with omitted subjects.
const msgMore = "+%d more"

func isAutosquash(subject string) bool {
	for _, prefix := range []string{"fixup!", "squash!", "amend!"} {
		if strings.HasPrefix(subject, prefix) {
			return true
		}
	}
	return false
}

// Clean makes a commit subject safe to send: invalid UTF-8, control and
// format characters (newlines, escape sequences, bidirectional overrides)
// become spaces, runs of white space collapse, a text that looks like a
// ticktack marker is defused so it cannot be mistaken for one, and the result
// is capped at MaxSubjectRunes.
func Clean(subject string) string {
	subject = strings.ToValidUTF8(subject, " ")
	subject = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.IsSpace(r) {
			return ' '
		}
		return r
	}, subject)
	subject = strings.Join(strings.Fields(subject), " ")
	subject = strings.ReplaceAll(subject, markerPrefix, markerReplace)
	if utf8.RuneCountInString(subject) <= MaxSubjectRunes {
		return subject
	}
	runes := []rune(subject)
	return strings.TrimSpace(string(runes[:MaxSubjectRunes-1])) + ellipsis
}
