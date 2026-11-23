package config

import (
	"regexp"
	"strings"
)

// Option is one documented setting of the configuration template: the exact
// template lines that describe and show it. The config update command
// compares these blocks with a user's file.
type Option struct {
	// ID is Key for a top-level key, Table + "." + Key for a key inside a
	// table, and Table for a free-form table.
	ID string
	// Table is empty for top-level keys.
	Table string
	// Key is empty for a free-form table option.
	Key string
	// Lines are the template lines of the block: the description comments and
	// the commented key line, or the whole commented block of a free-form
	// table.
	Lines []string
}

// freeFormTables are tables whose entries are user data, so the table as a
// whole is one option rather than one option per key.
var freeFormTables = map[string]bool{
	"titlehint.projects": true,
	"app_classes":        true,
	"rules":              true,
}

type lineKind int

const (
	kindBlank lineKind = iota
	kindComment
	kindHeader
	kindKey
)

// lineEntry classifies one line of a configuration file. Table is the table
// a header opens or the table a key sits in ("" before the first header).
type lineEntry struct {
	Index  int
	Kind   lineKind
	Table  string
	Key    string
	Active bool
}

var (
	activeHeaderPattern    = regexp.MustCompile(`^\[\[?([A-Za-z0-9_.-]+)\]\]?\s*(#.*)?$`)
	commentedHeaderPattern = regexp.MustCompile(`^# \[\[?([A-Za-z0-9_.-]+)\]\]?$`)
	activeKeyPattern       = regexp.MustCompile(`^("[^"]+"|[A-Za-z0-9_.-]+)\s*=`)
	commentedKeyPattern    = regexp.MustCompile(`^# ("[^"]+"|[A-Za-z0-9_.-]+)\s*=`)
)

// parseLines classifies every line of text. Line endings are normalised for
// matching only; the caller keeps its own copy of the text.
func parseLines(text string) []lineEntry {
	lines := strings.Split(text, "\n")
	entries := make([]lineEntry, 0, len(lines))
	table := ""
	for i, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		entry := lineEntry{Index: i}
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			entry.Kind = kindBlank
		case activeHeaderPattern.MatchString(line):
			table = activeHeaderPattern.FindStringSubmatch(line)[1]
			entry.Kind, entry.Table, entry.Active = kindHeader, table, true
		case commentedHeaderPattern.MatchString(line):
			table = commentedHeaderPattern.FindStringSubmatch(line)[1]
			entry.Kind, entry.Table = kindHeader, table
		case activeKeyPattern.MatchString(line):
			entry.Kind, entry.Table, entry.Active = kindKey, table, true
			entry.Key = strings.Trim(activeKeyPattern.FindStringSubmatch(line)[1], `"`)
		case commentedKeyPattern.MatchString(line):
			entry.Kind, entry.Table = kindKey, table
			entry.Key = strings.Trim(commentedKeyPattern.FindStringSubmatch(line)[1], `"`)
		default:
			entry.Kind = kindComment
		}
		entries = append(entries, entry)
	}
	return entries
}

// templateOptions parses DefaultTOML into its options, in template order.
// The template is the single source of truth for which options exist.
func templateOptions() []Option {
	lines := strings.Split(DefaultTOML(""), "\n")
	entries := parseLines(DefaultTOML(""))
	var options []Option
	for i := 0; i < len(entries); i++ {
		entry := entries[i]
		switch {
		case entry.Kind == kindHeader && freeFormTables[entry.Table]:
			start := commentRunStart(entries, i)
			end := i + 1
			for end < len(entries) && entries[end].Kind != kindBlank && entries[end].Kind != kindHeader {
				end++
			}
			options = append(options, Option{
				ID:    entry.Table,
				Table: entry.Table,
				Lines: append([]string(nil), lines[start:end]...),
			})
			i = end - 1
		case entry.Kind == kindKey:
			start := commentRunStart(entries, i)
			id := entry.Key
			if entry.Table != "" {
				id = entry.Table + "." + entry.Key
			}
			options = append(options, Option{
				ID:    id,
				Table: entry.Table,
				Key:   entry.Key,
				Lines: append([]string(nil), lines[start:i+1]...),
			})
		}
	}
	return options
}

// commentRunStart returns the index of the first comment line of the run of
// comments directly above entries[index].
func commentRunStart(entries []lineEntry, index int) int {
	start := index
	for start > 0 && entries[start-1].Kind == kindComment {
		start--
	}
	return start
}
