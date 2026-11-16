package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/civil"
	"github.com/spf13/cobra"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/render"
	"github.com/JtheGunner/ticktack/internal/review"
)

// reviewStore is the store as the review command uses it.
type reviewStore interface {
	review.Store
	Close() error
}

// reviewDeps holds everything the review command touches outside pure logic.
type reviewDeps struct {
	OpenStore  func() (reviewStore, error)
	LoadConfig func() (config.Config, error)
	Now        func() time.Time
	// NewIssueSource builds the source of ticket suggestions. It is optional;
	// an error (no URL, no token) just means the review shows no suggestions.
	NewIssueSource func(cfg config.Config) (review.IssueSource, error)
	// In supplies the scripted or interactive answers, one action per line.
	In     io.Reader
	Out    io.Writer
	Render render.Renderer
}

func defaultReviewDeps(cmd *cobra.Command) reviewDeps {
	return reviewDeps{
		OpenStore: func() (reviewStore, error) {
			st, err := openLedgerStore()
			if err != nil {
				return nil, err
			}
			return st, nil
		},
		LoadConfig: func() (config.Config, error) {
			path, err := config.DefaultPath()
			if err != nil {
				return config.Config{}, err
			}
			return config.LoadOrDefault(path)
		},
		Now:            func() time.Time { return time.Now().Round(0) },
		NewIssueSource: newIssueSource,
		In:             cmd.InOrStdin(),
		Out:            cmd.OutOrStdout(),
		Render:         rendererFor(cmd, cmd.OutOrStdout()),
	}
}

func newReviewCommand() *cobra.Command {
	var day string
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Review the day's blocks and confirm what may be booked",
		Long: `Show the day as a numbered timeline with tier and reason per block, then
confirm, reassign, discard, mark as break or split entries. Only confirmed
items are booked by ` + "`ticktack sync`" + `. Corrections are stored as overrides.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runReview(cmd.Context(), defaultReviewDeps(cmd), day)
		},
	}
	cmd.Flags().StringVar(&day, "day", "", "day to review as YYYY-MM-DD (default: today)")
	return cmd
}

// runReview drives one review session from line-based input.
func runReview(ctx context.Context, deps reviewDeps, dayFlag string) error {
	cfg, err := deps.LoadConfig()
	if err != nil {
		return err
	}
	loc := cfg.Location
	if loc == nil {
		loc = time.Local
	}
	day, err := parseDay(dayFlag, deps.Now().In(loc))
	if err != nil {
		return err
	}
	st, err := deps.OpenStore()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	var opts []review.Option
	if deps.NewIssueSource != nil {
		// Suggestions are a convenience: any failure to set them up is ignored.
		if src, err := deps.NewIssueSource(cfg); err == nil && src != nil {
			opts = append(opts, review.WithIssueSource(src))
		}
	}
	session, err := review.NewSession(ctx, st, cfg, day, deps.Now, opts...)
	if err != nil {
		return err
	}
	r := &reviewRun{ctx: ctx, s: session, out: deps.Out, render: deps.Render, in: bufio.NewScanner(deps.In), loc: loc, day: day}
	return r.loop()
}

type reviewRun struct {
	ctx    context.Context
	s      *review.Session
	out    io.Writer
	render render.Renderer
	in     *bufio.Scanner
	loc    *time.Location
	day    civil.Date
}

func (r *reviewRun) printf(format string, args ...any) {
	_, _ = fmt.Fprintf(r.out, format, args...)
}

func (r *reviewRun) println(line string) { _, _ = fmt.Fprintln(r.out, line) }

// say prints one gutter line; text must already be sanitised.
func (r *reviewRun) say(s render.Status, text string) {
	r.println(r.render.Gutter(s, text))
}

func (r *reviewRun) loop() error {
	r.show()
	for {
		r.printf("%s", msgReviewPrompt)
		if !r.in.Scan() {
			r.println("")
			break
		}
		line := strings.TrimSpace(r.in.Text())
		if line == "" {
			continue
		}
		if line == "q" {
			break
		}
		refreshed, err := r.execute(strings.Fields(line))
		if err != nil {
			r.say(render.StatusFail, msgReviewError(err))
		}
		if refreshed {
			r.show()
		}
	}
	if err := r.in.Err(); err != nil {
		return err
	}
	r.finish()
	return r.s.Finish(r.ctx)
}

func (r *reviewRun) show() {
	entries := r.s.Entries()
	if len(entries) == 0 {
		r.say(render.StatusInfo, fmt.Sprintf(msgReviewNoEntries, r.day))
		return
	}
	rows := make([][]string, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, r.entryRow(e))
	}
	r.println(r.render.Heading(msgReviewHeading(r.day.String())))
	r.println("")
	for _, line := range r.render.Table([]render.Column{
		{Header: msgColBlock, Right: true},
		{Header: msgColTime},
		{Header: msgColLength, Right: true},
		{Header: msgColTicket},
		{Header: msgColTier},
		{Header: msgColReason, Flex: true, Wrap: true},
		{Header: msgColState},
	}, rows) {
		r.println(line)
	}
	for _, e := range entries {
		r.showSuggestions(e)
	}
	if w := r.s.CapWarning(); w != nil {
		r.say(render.StatusWarn, msgReviewCapWarning(*w))
	}
	r.println("")
	r.say(render.StatusInfo, msgReviewHelp)
}

// entryRow returns the table cells of one entry. The block number is the same
// 1-based index the actions accept.
func (r *reviewRun) entryRow(e review.Entry) []string {
	ticket := ticketLabel(e.Ticket)
	state := ""
	switch {
	case e.Kind == review.KindGap:
		state = r.render.Colors.Warn(msgReviewStateNeedsTicket)
	case e.Confirmed:
		state = r.render.Colors.OK(msgReviewStateConfirmed)
	}
	return []string{
		strconv.Itoa(e.Index),
		e.Start.In(r.loc).Format("15:04") + "-" + e.End.In(r.loc).Format("15:04"),
		formatMinutes(e.Minutes),
		SanitizeForTerminal(ticket),
		colorTier(r.render, e.Tier),
		SanitizeForTerminal(e.Reason),
		state,
	}
}

func (r *reviewRun) showSuggestions(e review.Entry) {
	if e.Kind != review.KindGap {
		return
	}
	list, err := r.s.Suggestions(r.ctx, e.Index)
	if err != nil || len(list) == 0 {
		return
	}
	r.println("")
	r.say(render.StatusInfo, msgReviewSuggestionsHeader(e.Index))
	for i, c := range list {
		r.println(msgReviewSuggestion(i+1, c))
	}
}

func (r *reviewRun) offerRemember() {
	if offer, ok := r.s.Remembering(); ok {
		r.say(render.StatusInfo, msgReviewRememberOffer(offer))
	}
}

func (r *reviewRun) remember(args []string) error {
	wholeRepo := false
	switch {
	case len(args) == 1 && args[0] == "repo":
		wholeRepo = true
	case len(args) > 0:
		r.say(render.StatusWarn, msgReviewUsage("r [repo]"))
		return nil
	}
	rule, err := r.s.Remember(r.ctx, wholeRepo)
	if err != nil {
		return err
	}
	r.say(render.StatusOK, msgReviewRemembered(rule))
	return nil
}

func (r *reviewRun) finish() {
	sum := r.s.Summary()
	r.println("")
	r.say(render.StatusInfo, msgReviewSummary(sum))
	if sum.CapBlocked {
		r.say(render.StatusWarn, msgReviewCapBlocked)
	}
	if sum.Confirmed == 0 {
		r.say(render.StatusInfo, msgReviewNothingYet)
		return
	}
	r.say(render.StatusGo, msgReviewNextStep)
}

// execute runs one action. refreshed reports whether the entry list changed
// and must be printed again.
func (r *reviewRun) execute(f []string) (refreshed bool, err error) {
	cmd, args := f[0], f[1:]
	switch cmd {
	case "a":
		if err := r.s.AcknowledgeCap(r.ctx); err != nil {
			return false, err
		}
		r.say(render.StatusOK, msgReviewCapAcked)
		return true, nil
	case "y":
		n, err := r.s.ConfirmAll(r.ctx)
		if err != nil {
			return false, err
		}
		r.say(render.StatusOK, fmt.Sprintf(msgReviewConfirmAll, n))
		return true, nil
	case "r":
		err := r.remember(args)
		return err == nil, err
	case "c", "d", "b", "t", "s", "u":
		return r.entryAction(cmd, args)
	}
	r.say(render.StatusWarn, msgReviewUnknownCommand(strings.Join(f, " ")))
	return false, nil
}

func (r *reviewRun) entryAction(cmd string, args []string) (bool, error) {
	usage := map[string]string{
		"c": "c N", "d": "d N", "b": "b N", "t": "t N PROJ-12", "s": "s N HH:MM [PROJ-12]", "u": "u N K",
	}[cmd]
	min, max := 1, 1
	switch cmd {
	case "t", "u":
		min, max = 2, 2
	case "s":
		min, max = 2, 3
	}
	if len(args) < min || len(args) > max {
		r.say(render.StatusWarn, msgReviewUsage(usage))
		return false, nil
	}
	n, err := strconv.Atoi(args[0])
	if err != nil {
		r.say(render.StatusWarn, msgReviewUsage(usage))
		return false, nil
	}
	switch cmd {
	case "c":
		err = r.confirm(n)
	case "d":
		if err = r.s.Discard(r.ctx, n); err == nil {
			r.say(render.StatusOK, fmt.Sprintf(msgReviewDiscarded, n))
		}
	case "b":
		if err = r.s.MarkBreak(r.ctx, n); err == nil {
			r.say(render.StatusOK, fmt.Sprintf(msgReviewBreak, n))
		}
	case "t":
		if err = r.s.Reassign(r.ctx, n, args[1]); err == nil {
			r.say(render.StatusOK, fmt.Sprintf(msgReviewReassigned, n, SanitizeForTerminal(strings.ToUpper(args[1]))))
			r.offerRemember()
		}
	case "u":
		err = r.useSuggestion(n, args[1])
	case "s":
		err = r.split(n, args[1:])
	}
	return err == nil, err
}

func (r *reviewRun) useSuggestion(n int, rawK string) error {
	k, err := strconv.Atoi(rawK)
	if err != nil {
		r.say(render.StatusWarn, msgReviewUsage("u N K"))
		return nil
	}
	ticket, err := r.s.ReassignSuggestion(r.ctx, n, k)
	if err != nil {
		return err
	}
	r.say(render.StatusOK, fmt.Sprintf(msgReviewReassigned, n, SanitizeForTerminal(ticket)))
	r.say(render.StatusInfo, msgReviewSuggestionNeedsConfirm)
	r.offerRemember()
	return nil
}

func (r *reviewRun) confirm(n int) error {
	if err := r.s.Confirm(r.ctx, n); err != nil {
		return err
	}
	r.say(render.StatusOK, fmt.Sprintf(msgReviewConfirmed, SanitizeForTerminal(r.s.Entries()[n-1].Ticket)))
	return nil
}

func (r *reviewRun) split(n int, rest []string) error {
	// Validate the time before asking for the ticket.
	if _, err := r.s.SplitAt(n, rest[0]); err != nil {
		return err
	}
	ticket := ""
	if len(rest) > 1 {
		ticket = rest[1]
	} else {
		current := r.s.Entries()[n-1].Ticket
		if current == "" {
			current = msgUnassigned
		}
		r.printf(msgReviewSplitPrompt, SanitizeForTerminal(current))
		if r.in.Scan() {
			ticket = strings.TrimSpace(r.in.Text())
		}
	}
	if err := r.s.Split(r.ctx, n, rest[0], ticket); err != nil {
		return err
	}
	r.say(render.StatusOK, fmt.Sprintf(msgReviewSplit, n, SanitizeForTerminal(rest[0])))
	return nil
}
