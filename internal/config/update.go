package config

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// Update messages. They name keys and lines only, never values, because a
// value could be a secret.
const (
	msgUpdateUnsafeRemoval = "config update: cannot remove unknown key %s at line %d automatically; remove it by hand"
	msgUpdateInvalidResult = "config update: the updated config would be invalid: %w"
)

// AddedOption is a template option the user's file lacks. Lines are the
// template lines of the option as they are inserted (commented out).
type AddedOption struct {
	ID    string
	Lines []string
}

// RemovedOption is an active key or table the current version no longer
// knows. Line is 1-based; Value is the raw text right of "=" on the key line
// (empty for a removed table).
type RemovedOption struct {
	ID    string
	Line  int
	Value string
}

// UpdatePlan is the outcome of comparing a config file with the current
// option templates. Result is Original with the added blocks inserted and the
// removed keys deleted; every other line is unchanged byte for byte.
type UpdatePlan struct {
	Original string
	Result   string
	Added    []AddedOption
	Removed  []RemovedOption
}

// Changed reports whether applying the plan would change the file.
func (p UpdatePlan) Changed() bool {
	return p.Result != p.Original
}

var (
	// Commented forms with leading whitespace, no space after "#" or extra
	// spacing; parseLines only knows the column-0 "# key =" form.
	looseCommentedHeaderPattern = regexp.MustCompile(`^\s*#\s*\[\[?\s*([A-Za-z_][A-Za-z0-9_.-]*)\s*\]\]?\s*$`)
	looseCommentedKeyPattern    = regexp.MustCompile(`^\s*#\s*("[^"]+"|[A-Za-z0-9_.-]+)\s*=`)
)

// PlanUpdate compares current with the option templates of DefaultTOML and
// computes the updated text. It adds every missing option as a commented
// block and deletes every active key or table the strict decoder rejects. It
// fails when current is not valid TOML or the result would not pass Parse.
func PlanUpdate(current string) (UpdatePlan, error) {
	unknown, err := unknownKeys(current)
	if err != nil {
		return UpdatePlan{}, err
	}
	lines := strings.Split(current, "\n")
	entries := classifyForUpdate(lines)
	newline := "\n"
	if strings.Contains(current, "\r\n") {
		newline = "\r\n"
	}

	deleted := make([]bool, len(lines))
	removed, err := planRemovals(lines, entries, unknown, deleted)
	if err != nil {
		return UpdatePlan{}, err
	}
	collapseBlankLines(lines, deleted)

	added, inserts := planAdditions(lines, entries)
	result := render(lines, deleted, inserts, strings.TrimSuffix(newline, "\n"))

	if _, err := Parse([]byte(result)); err != nil {
		return UpdatePlan{}, fmt.Errorf(msgUpdateInvalidResult, err)
	}
	return UpdatePlan{Original: current, Result: result, Added: added, Removed: removed}, nil
}

// unknownKeys strictly decodes text and returns the keys it does not know.
// Any other decode error is returned.
func unknownKeys(text string) ([]toml.DecodeError, error) {
	var raw fileConfig
	dec := toml.NewDecoder(bytes.NewReader([]byte(text)))
	dec.DisallowUnknownFields()
	err := dec.Decode(&raw)
	if err == nil {
		return nil, nil
	}
	var strict *toml.StrictMissingError
	if errors.As(err, &strict) {
		return strict.Errors, nil
	}
	return nil, describeDecodeError(err)
}

// classifyForUpdate refines parseLines for editing: lines inside a multi-line
// value become continuations (so array or string text is never read as a key
// or header), any other line starting with "[" is an active header, any other
// uncommented line is an active key, and commented keys and headers are also
// recognised with leading whitespace. An active key's Table follows active
// headers only; a commented key's Table follows any header.
func classifyForUpdate(lines []string) []lineEntry {
	base := parseLines(strings.Join(lines, "\n"))
	entries := make([]lineEntry, len(base))
	activeTable, anyTable := "", ""
	var value valueScanner
	for i, entry := range base {
		line := strings.TrimRight(lines[i], "\r")
		if value.open() {
			value.scan(line)
			entries[i] = lineEntry{Index: i, Kind: kindContinuation}
			continue
		}
		trimmed := strings.TrimSpace(line)
		switch {
		case entry.Kind == kindBlank:
		case strings.HasPrefix(trimmed, "#"):
			entry = classifyCommentLine(i, line, entry)
		case strings.HasPrefix(trimmed, "["):
			entry = lineEntry{Index: i, Kind: kindHeader, Active: true, Table: headerTable(trimmed)}
		default:
			entry = lineEntry{Index: i, Kind: kindKey, Active: true}
			if match := activeKeyPattern.FindStringSubmatch(line); match != nil {
				entry.Key = strings.Trim(match[1], `"`)
			}
			value.scan(valueText(line))
		}
		switch {
		case entry.Kind == kindHeader:
			anyTable = entry.Table
			if entry.Active {
				activeTable = entry.Table
			}
		case entry.Kind == kindKey && entry.Active:
			entry.Table = activeTable
		case entry.Kind == kindKey:
			entry.Table = anyTable
		}
		entries[i] = entry
	}
	return entries
}

// classifyCommentLine keeps parseLines' result for a commented header or key
// and otherwise checks the looser commented forms.
func classifyCommentLine(index int, line string, entry lineEntry) lineEntry {
	if entry.Kind == kindHeader || entry.Kind == kindKey {
		return entry
	}
	if match := looseCommentedHeaderPattern.FindStringSubmatch(line); match != nil {
		return lineEntry{Index: index, Kind: kindHeader, Table: match[1]}
	}
	if match := looseCommentedKeyPattern.FindStringSubmatch(line); match != nil {
		return lineEntry{Index: index, Kind: kindKey, Key: strings.Trim(match[1], `"`)}
	}
	return lineEntry{Index: index, Kind: kindComment}
}

// headerTable returns the dotted table name of an active header line, with
// brackets, quotes, spacing and a trailing comment removed.
func headerTable(trimmed string) string {
	if match := activeHeaderPattern.FindStringSubmatch(trimmed); match != nil {
		return match[1]
	}
	name := trimmed
	if end := strings.LastIndex(name, "]"); end >= 0 {
		name = name[:end]
	}
	name = strings.Trim(name, "[] \t")
	parts := strings.Split(name, ".")
	for i, part := range parts {
		parts[i] = strings.Trim(strings.TrimSpace(part), `"'`)
	}
	return strings.Join(parts, ".")
}

// valueText returns the text right of the first "=" of a key line.
func valueText(line string) string {
	if match := activeKeyPattern.FindString(line); match != "" {
		return line[len(match):]
	}
	if index := strings.Index(line, "="); index >= 0 {
		return line[index+1:]
	}
	return ""
}

// valueScanner follows brackets and strings across lines to find where a
// multi-line value ends. The input has already passed the TOML decoder.
type valueScanner struct {
	depth          int
	inBasicBlock   bool
	inLiteralBlock bool
}

func (s *valueScanner) open() bool {
	return s.depth > 0 || s.inBasicBlock || s.inLiteralBlock
}

func (s *valueScanner) scan(text string) {
	for i := 0; i < len(text); {
		switch {
		case s.inBasicBlock:
			if text[i] == '\\' {
				i += 2
				continue
			}
			if strings.HasPrefix(text[i:], `"""`) {
				s.inBasicBlock = false
				i += 3
				continue
			}
			i++
		case s.inLiteralBlock:
			if strings.HasPrefix(text[i:], `'''`) {
				s.inLiteralBlock = false
				i += 3
				continue
			}
			i++
		case strings.HasPrefix(text[i:], `"""`):
			s.inBasicBlock = true
			i += 3
		case strings.HasPrefix(text[i:], `'''`):
			s.inLiteralBlock = true
			i += 3
		case text[i] == '"':
			i = skipBasicString(text, i+1)
		case text[i] == '\'':
			end := strings.IndexByte(text[i+1:], '\'')
			if end < 0 {
				return
			}
			i += end + 2
		case text[i] == '#':
			return
		case text[i] == '[' || text[i] == '{':
			s.depth++
			i++
		case text[i] == ']' || text[i] == '}':
			s.depth--
			i++
		default:
			i++
		}
	}
}

// skipBasicString returns the index after the closing quote of a one-line
// basic string that starts at i.
func skipBasicString(text string, i int) int {
	for i < len(text) {
		switch text[i] {
		case '\\':
			i += 2
		case '"':
			return i + 1
		default:
			i++
		}
	}
	return i
}

// planRemovals marks the lines of every unknown key or table in deleted. A
// key loses its line and the continuation lines of its value; a table loses
// its header and every active key of its region. Comments stay. A reported
// key that cannot be matched to its own line is an error, so nothing is
// deleted by guesswork.
func planRemovals(lines []string, entries []lineEntry, unknown []toml.DecodeError, deleted []bool) ([]RemovedOption, error) {
	var removed []RemovedOption
	for i := range unknown {
		id := strings.Join(unknown[i].Key(), ".")
		line, _ := unknown[i].Position()
		index := line - 1
		if index < 0 || index >= len(entries) {
			return nil, fmt.Errorf(msgUpdateUnsafeRemoval, id, line)
		}
		entry := entries[index]
		switch {
		case entry.Kind == kindHeader && entry.Active && entry.Table == id:
			markTableRegion(entries, index, deleted)
			removed = append(removed, RemovedOption{ID: id, Line: line})
		case entry.Kind == kindKey && entry.Active && entry.Key != "" && optionID(entry.Table, entry.Key) == id:
			markKeyExtent(entries, index, deleted)
			value := strings.TrimSpace(strings.TrimRight(valueText(lines[index]), "\r"))
			removed = append(removed, RemovedOption{ID: id, Line: line, Value: value})
		default:
			return nil, fmt.Errorf(msgUpdateUnsafeRemoval, id, line)
		}
	}
	return removed, nil
}

// markTableRegion marks an active header and every active key (with its
// value) up to the next active header.
func markTableRegion(entries []lineEntry, header int, deleted []bool) {
	deleted[header] = true
	for i := header + 1; i < len(entries); i++ {
		entry := entries[i]
		if entry.Kind == kindHeader && entry.Active {
			break
		}
		if (entry.Kind == kindKey && entry.Active) || entry.Kind == kindContinuation {
			deleted[i] = true
		}
	}
}

// markKeyExtent marks a key line and the continuation lines of its value.
func markKeyExtent(entries []lineEntry, key int, deleted []bool) {
	deleted[key] = true
	for i := key + 1; i < len(entries) && entries[i].Kind == kindContinuation; i++ {
		deleted[i] = true
	}
}

// collapseBlankLines removes one blank line where a deletion would leave two
// blank lines in a row (or a blank line at the start of the file). The final
// empty element after the last newline is kept so the line ending stays.
func collapseBlankLines(lines []string, deleted []bool) {
	last := len(lines) - 1
	for i := 0; i < len(lines); {
		if !deleted[i] {
			i++
			continue
		}
		end := i
		for end < len(lines) && deleted[end] {
			end++
		}
		prev := i - 1
		for prev >= 0 && deleted[prev] {
			prev--
		}
		prevBlank := prev < 0 || isBlank(lines[prev])
		switch {
		case !prevBlank || end >= len(lines) || !isBlank(lines[end]):
		case end == last && lines[end] == "":
			if prev >= 0 {
				deleted[prev] = true
			}
		default:
			deleted[end] = true
		}
		i = end + 1
	}
}

func isBlank(line string) bool {
	return strings.TrimSpace(line) == ""
}

func optionID(table, key string) string {
	if table == "" {
		return key
	}
	return table + "." + key
}

// insertion is a group of blocks inserted before line index at.
type insertion struct {
	at     int
	blocks [][]string
}

// planAdditions finds the template options the file lacks and where their
// blocks go: a top-level option after the last top-level key (or before the
// comments above the first header, or at the end); a key of a table that has
// a header at the end of that table's region; a key of a table without a
// header, and a missing free-form table, at the end of the file in a block
// that starts with the table's template intro and commented header.
func planAdditions(lines []string, entries []lineEntry) ([]AddedOption, []insertion) {
	keyIDs, headers := presentIDs(entries)
	isPresent := func(id string) bool {
		if keyIDs[id] {
			return true
		}
		for present := range keyIDs {
			if strings.HasPrefix(present, id+".") {
				return true
			}
		}
		return false
	}

	end := endPosition(lines)
	intros := templateTableIntros()
	var added []AddedOption
	var inserts []insertion
	appendBlock := func(at int, block []string) {
		for i := range inserts {
			if inserts[i].at == at {
				inserts[i].blocks = append(inserts[i].blocks, block)
				return
			}
		}
		inserts = append(inserts, insertion{at: at, blocks: [][]string{block}})
	}
	// Blocks of tables the file lacks are collected apart and placed after
	// every anchored block, so keys that belong to the last table of the file
	// stay directly below it instead of landing under an appended table.
	var appended [][]string
	newTables := map[string]int{}

	for _, option := range templateOptions() {
		switch {
		case option.Key == "":
			if headers[option.Table] || isPresent(option.ID) {
				continue
			}
			appended = append(appended, option.Lines)
		case isPresent(option.ID):
			continue
		case option.Table == "":
			appendBlock(topLevelAnchor(entries, end), option.Lines)
		case headers[option.Table]:
			appendBlock(tableAnchor(entries, option.Table), option.Lines)
		default:
			block, ok := newTables[option.Table]
			if !ok {
				appended = append(appended, append([]string(nil), intros[option.Table]...))
				block = len(appended) - 1
				newTables[option.Table] = block
			}
			appended[block] = append(appended[block], option.Lines...)
		}
		added = append(added, AddedOption{ID: option.ID, Lines: append([]string(nil), option.Lines...)})
	}
	for _, block := range appended {
		appendBlock(end, block)
	}
	return added, inserts
}

// presentIDs collects the option IDs of every key (active or commented) and
// the tables that have a header (active or commented).
func presentIDs(entries []lineEntry) (map[string]bool, map[string]bool) {
	keyIDs := map[string]bool{}
	headers := map[string]bool{}
	for _, entry := range entries {
		switch {
		case entry.Kind == kindHeader:
			headers[entry.Table] = true
		case entry.Kind == kindKey && entry.Key != "":
			keyIDs[optionID(entry.Table, entry.Key)] = true
		}
	}
	return keyIDs, headers
}

// endPosition is the insertion index for the end of the file: before the
// empty element that follows a final newline, or after the last line.
func endPosition(lines []string) int {
	if lines[len(lines)-1] == "" {
		return len(lines) - 1
	}
	return len(lines)
}

// topLevelAnchor returns where a top-level option goes: after the last
// top-level key and its value, else before the comments directly above the
// first header, else at the end.
func topLevelAnchor(entries []lineEntry, end int) int {
	firstHeader := -1
	lastKey := -1
	for i, entry := range entries {
		if entry.Kind == kindHeader {
			firstHeader = i
			break
		}
		if entry.Kind == kindKey {
			lastKey = i
		}
	}
	switch {
	case lastKey >= 0:
		at := lastKey + 1
		for at < len(entries) && entries[at].Kind == kindContinuation {
			at++
		}
		return at
	case firstHeader >= 0:
		return commentRunStart(entries, firstHeader)
	default:
		return end
	}
}

// tableAnchor returns the index after the last non-blank line of a table's
// region, leaving out the comments directly above the next header (they
// introduce that header). An active header is preferred over a commented one.
func tableAnchor(entries []lineEntry, table string) int {
	header := -1
	for i, entry := range entries {
		if entry.Kind != kindHeader || entry.Table != table {
			continue
		}
		if entry.Active {
			header = i
			break
		}
		if header < 0 {
			header = i
		}
	}
	next := len(entries)
	for i := header + 1; i < len(entries); i++ {
		if entries[i].Kind == kindHeader {
			next = i
			break
		}
	}
	at := next
	if next < len(entries) {
		at = commentRunStart(entries, next)
	}
	for at-1 > header && entries[at-1].Kind == kindBlank {
		at--
	}
	return at
}

// templateTableIntros returns, per fixed table of the template, the comment
// lines directly above its header plus the commented header itself.
func templateTableIntros() map[string][]string {
	text := DefaultTOML("")
	lines := strings.Split(text, "\n")
	entries := parseLines(text)
	intros := map[string][]string{}
	for i, entry := range entries {
		if entry.Kind != kindHeader {
			continue
		}
		start := commentRunStart(entries, i)
		intros[entry.Table] = append([]string(nil), lines[start:i+1]...)
	}
	return intros
}

// render writes the kept lines and the inserted blocks. Blocks of one group
// are separated by a blank line, and a group gets a blank line to each
// neighbour that is not blank already. carriageReturn is "\r" for a CRLF
// file and is appended to every inserted line.
func render(lines []string, deleted []bool, inserts []insertion, carriageReturn string) string {
	groups := map[int]insertion{}
	for _, group := range inserts {
		groups[group.at] = group
	}
	var out []string
	for i := 0; i <= len(lines); i++ {
		if group, ok := groups[i]; ok {
			out = appendGroup(out, group, nextKept(lines, deleted, i), carriageReturn)
		}
		if i == len(lines) {
			break
		}
		if !deleted[i] {
			out = append(out, lines[i])
		}
	}
	return strings.Join(out, "\n")
}

func appendGroup(out []string, group insertion, next *string, carriageReturn string) []string {
	if len(out) > 0 && !isBlank(out[len(out)-1]) {
		out = append(out, carriageReturn)
	}
	for b, block := range group.blocks {
		if b > 0 {
			out = append(out, carriageReturn)
		}
		for _, line := range block {
			out = append(out, line+carriageReturn)
		}
	}
	if next != nil && !isBlank(*next) {
		out = append(out, carriageReturn)
	}
	return out
}

// nextKept returns the first line at or after index that is not deleted.
func nextKept(lines []string, deleted []bool, index int) *string {
	for i := index; i < len(lines); i++ {
		if !deleted[i] {
			return &lines[i]
		}
	}
	return nil
}
