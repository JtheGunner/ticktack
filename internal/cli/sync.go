package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cloud.google.com/go/civil"
	"github.com/spf13/cobra"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/ledger"
	"github.com/JtheGunner/ticktack/internal/paths"
	"github.com/JtheGunner/ticktack/internal/render"
	"github.com/JtheGunner/ticktack/internal/review"
	"github.com/JtheGunner/ticktack/internal/sink"
	"github.com/JtheGunner/ticktack/internal/store"
	"github.com/JtheGunner/ticktack/internal/token"
	"github.com/JtheGunner/ticktack/internal/youtrack"
)

// User-facing strings of the sync and undo commands live here so a German
// variant can be added later.
const (
	msgConfigMissing = "no configuration found at %s: run `ticktack init` first"
	msgNoURL         = "youtrack_url is not set in the configuration: run `ticktack init` or add it to %s"
	msgNoToken       = "no YouTrack token in the keychain: run `ticktack init` to store one"
	msgCapBlocked    = "%s: the day total %s exceeds the daily cap of %s and was not acknowledged: run `ticktack review` and acknowledge it (a)"

	msgNothingConfirmed = "Nothing is confirmed for %s, so nothing was booked. Run `ticktack review` first."
)

// syncDeps holds everything the sync and undo commands touch outside pure logic.
type syncDeps struct {
	LoadConfig func() (config.Config, string, error)
	OpenStore  func() (*store.Store, error)
	// NewSink builds the booking target from the configuration. It must not
	// log or return the token.
	NewSink func(cfg config.Config, configPath string) (sink.TimeSink, error)
	Now     func() time.Time
	// AcquireLock takes the lock that admits one booking run at a time and
	// returns the function that releases it. It fails with
	// ErrBookingRunActive while another run holds the lock. Nil runs without
	// a lock.
	AcquireLock func() (release func(), err error)
	// Render styles the output; the zero value renders plain text.
	Render render.Renderer
	// In supplies the answer to the question before standard tasks are
	// created; nil declines. forCommand fills it from the command.
	In io.Reader
	// RenderFor builds Render from the running command, which does not exist
	// yet when the default deps are built. Nil keeps Render as given.
	RenderFor func(cmd *cobra.Command, w io.Writer) render.Renderer
}

func defaultSyncDeps() syncDeps {
	return syncDeps{
		LoadConfig:  loadConfigFile,
		OpenStore:   openLedgerStore,
		NewSink:     newYouTrackSink,
		Now:         func() time.Time { return time.Now().Round(0) },
		AcquireLock: acquireBookingLock,
		RenderFor:   rendererFor,
	}
}

// forCommand returns deps with Render resolved for cmd's output stream. A
// Render that was set explicitly (not the zero value) is never overridden.
func (d syncDeps) forCommand(cmd *cobra.Command) syncDeps {
	if d.In == nil {
		d.In = cmd.InOrStdin()
	}
	if d.RenderFor != nil && d.Render == (render.Renderer{}) {
		d.Render = d.RenderFor(cmd, cmd.OutOrStdout())
	}
	return d
}

func loadConfigFile() (config.Config, string, error) {
	path, err := config.DefaultPath()
	if err != nil {
		return config.Config{}, "", err
	}
	cfg, err := config.Load(path)
	if errors.Is(err, os.ErrNotExist) {
		return config.Config{}, path, fmt.Errorf(msgConfigMissing, path)
	}
	return cfg, path, err
}

func openLedgerStore() (*store.Store, error) {
	dbPath, err := paths.DBPath()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	return store.Open(dbPath)
}

func newYouTrackSink(cfg config.Config, configPath string) (sink.TimeSink, error) {
	if cfg.YouTrackURL == "" {
		return nil, fmt.Errorf(msgNoURL, configPath)
	}
	secret, err := token.Get()
	if errors.Is(err, token.ErrNotSet) {
		return nil, errors.New(msgNoToken)
	}
	if err != nil {
		return nil, err
	}
	opts := []youtrack.Option{}
	if cfg.Location != nil {
		opts = append(opts, youtrack.WithLocation(cfg.Location))
	}
	return youtrack.New(cfg.YouTrackURL, secret, opts...)
}

// newIssueSource builds the YouTrack client used for ticket suggestions in
// review. Errors are expected (not configured, no token) and non-fatal.
func newIssueSource(cfg config.Config) (review.IssueSource, error) {
	if cfg.YouTrackURL == "" {
		return nil, errors.New("youtrack_url is not set")
	}
	secret, err := token.Get()
	if err != nil {
		return nil, err
	}
	opts := []youtrack.Option{}
	if cfg.Location != nil {
		opts = append(opts, youtrack.WithLocation(cfg.Location))
	}
	return youtrack.New(cfg.YouTrackURL, secret, opts...)
}

func newSyncCommand() *cobra.Command {
	return newSyncCommandWith(defaultSyncDeps())
}

func newSyncCommandWith(deps syncDeps) *cobra.Command {
	var dayFlag string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Book the confirmed time of a day in YouTrack",
		Long: `Book the items confirmed in "ticktack review" for one day in YouTrack, one
work item per ticket, day and type. Repeated runs update instead of
duplicating. Unconfirmed items are never booked; a day above the daily cap is
only booked after the cap warning was acknowledged in review.

A work item booked earlier is deleted when the day no longer has time for its
ticket, for example after the block was reassigned or discarded in review.
"ticktack undo" books it again.` + msgBookingLockHelpDryRun,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSync(cmd.Context(), cmd.OutOrStdout(), deps.forCommand(cmd), dayFlag, dryRun)
		},
	}
	cmd.Flags().StringVar(&dayFlag, "day", "", "local day to book as YYYY-MM-DD (default: today)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the plan without booking anything")
	return cmd
}

func runSync(ctx context.Context, out io.Writer, deps syncDeps, dayFlag string, dryRun bool) error {
	// The lock comes first: nothing read below may change under another run.
	release, err := deps.lockBookingRun(dryRun)
	if err != nil {
		return err
	}
	defer release()
	cfg, cfgPath, err := deps.LoadConfig()
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

	// Only confirmed items are ever booked.
	items, err := review.ConfirmedItems(ctx, st, cfg, day)
	var blocked *review.CapBlockedError
	if errors.As(err, &blocked) {
		return fmt.Errorf(msgCapBlocked, blocked.Day, formatMinutes(blocked.TotalMinutes), formatMinutes(blocked.CapMinutes))
	}
	if err != nil {
		return err
	}
	// Bookings of time the day no longer has are removed even when nothing
	// is confirmed, for example after every block was discarded.
	d, err := review.LoadDay(ctx, st, cfg, day)
	if err != nil {
		return err
	}
	keep, stale, err := reconciliation(ctx, st, d.Items, day)
	if err != nil {
		return err
	}
	if len(items) == 0 && !stale {
		_, err = fmt.Fprintln(out, deps.Render.Gutter(render.StatusInfo, fmt.Sprintf(msgNothingConfirmed, day)))
		return err
	}
	target, err := deps.NewSink(cfg, cfgPath)
	if err != nil {
		return err
	}
	items, provisioned, err := provisionStandardTasks(ctx, out, deps.In, deps.Render, cfg, st, target, items, dryRun)
	if err != nil {
		return err
	}
	if len(items) == 0 && len(provisioned.WouldBook) > 0 && !stale {
		// A dry run left out time that waits for a new standard task; the
		// lines above name it, so "No time to book" would be wrong.
		return nil
	}
	report, syncErr := ledger.Sync(ctx, items, target, ledger.SyncOptions{
		Repo: st, DryRun: dryRun, Now: deps.Now,
		Reconcile: []civil.Date{day}, Keep: keep,
	})
	printReport(out, deps.Render, report, dryRun)
	return syncErr
}

func parseDay(flag string, now time.Time) (civil.Date, error) {
	if flag == "" {
		return civil.DateOf(now), nil
	}
	d, err := civil.ParseDate(flag)
	if err != nil {
		return civil.Date{}, fmt.Errorf("invalid --day %q: expected YYYY-MM-DD", flag)
	}
	return d, nil
}

// printReport writes one gutter line per item and a summary box. Text from
// outside the program (keys, reasons, server errors) is sanitised before it is
// laid out or styled.
func printReport(out io.Writer, r render.Renderer, report ledger.Report, planOnly bool) {
	if len(report.Items) == 0 {
		_, _ = fmt.Fprintln(out, r.Gutter(render.StatusInfo, msgNoTimeToBook))
		return
	}
	lines := make([]string, 0, len(report.Items)+4)
	for _, it := range report.Items {
		text := fmt.Sprintf(msgSyncItem, SanitizeForTerminal(it.Key), it.Minutes, it.Action)
		switch {
		case it.Error != "":
			text += ": " + SanitizeForTerminal(it.Error)
		case it.Reason != "":
			text += " (" + SanitizeForTerminal(it.Reason) + ")"
		}
		lines = append(lines, r.Gutter(actionStatus(string(it.Action)), text))
	}
	title := msgSyncSummaryTitle
	summary := []string{
		fmt.Sprintf(msgSyncTotalsBooked, report.Created, report.Updated, report.Recovered, report.Repaired, report.Deleted),
		fmt.Sprintf(msgSyncTotalsOutcome, report.Skipped, report.Failed, report.NotSent),
	}
	if planOnly {
		title, summary = msgSyncPlanTitle, []string{fmt.Sprintf(msgSyncPlan, report.Planned, report.Skipped, report.Failed)}
	}
	status, paint := render.StatusOK, r.Colors.OK
	if report.Failed > 0 || report.NotSent > 0 {
		status, paint = render.StatusFail, r.Colors.Fail
	}
	for i, row := range summary {
		summary[i] = paint(row)
	}
	lines = append(lines, "", r.BoxStatus(status, title, summary))
	_, _ = fmt.Fprintln(out, strings.Join(lines, "\n"))
}
