package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"cloud.google.com/go/civil"
	"github.com/spf13/cobra"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/ledger"
	"github.com/JtheGunner/ticktack/internal/paths"
	"github.com/JtheGunner/ticktack/internal/resolver"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
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
	msgUnreviewed    = "WARNING: booking time that has not been reviewed. Nothing is confirmed yet; check the plan above."
	msgPlanOnly      = "Plan only: nothing was booked. Review is not available yet; pass --unreviewed to book the derived time anyway."
)

// syncDeps holds everything the sync and undo commands touch outside pure logic.
type syncDeps struct {
	LoadConfig func() (config.Config, string, error)
	OpenStore  func() (*store.Store, error)
	// NewSink builds the booking target from the configuration. It must not
	// log or return the token.
	NewSink func(cfg config.Config, configPath string) (sink.TimeSink, error)
	Now     func() time.Time
}

func defaultSyncDeps() syncDeps {
	return syncDeps{
		LoadConfig: loadConfigFile,
		OpenStore:  openLedgerStore,
		NewSink:    newYouTrackSink,
		Now:        func() time.Time { return time.Now().Round(0) },
	}
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

func newSyncCommand() *cobra.Command {
	return newSyncCommandWith(defaultSyncDeps())
}

func newSyncCommandWith(deps syncDeps) *cobra.Command {
	var dayFlag string
	var dryRun, unreviewed bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Book the derived time of a day in YouTrack",
		Long: `Derive the time of one day from the recorded events and book it in YouTrack,
one work item per ticket, day and type. Repeated runs update instead of
duplicating.

Review is not available yet, so sync only prints its plan (as with --dry-run)
unless --unreviewed is given. With --unreviewed the derived, unconfirmed time
is booked for real; a later release replaces this with reviewed items.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSync(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), deps, dayFlag, dryRun, unreviewed)
		},
	}
	cmd.Flags().StringVar(&dayFlag, "day", "", "local day to book as YYYY-MM-DD (default: today)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the plan without booking anything")
	cmd.Flags().BoolVar(&unreviewed, "unreviewed", false, "book derived time without review (temporary until review exists)")
	return cmd
}

func runSync(ctx context.Context, out, errOut io.Writer, deps syncDeps, dayFlag string, dryRun, unreviewed bool) error {
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

	items, err := dayItems(ctx, st, cfg, day, loc)
	if err != nil {
		return err
	}
	target, err := deps.NewSink(cfg, cfgPath)
	if err != nil {
		return err
	}

	// Nothing may be booked without review. Until the review command exists,
	// a real booking needs the explicit --unreviewed opt-in.
	// TODO(T14): replace this gate with confirmed items from review.
	book := unreviewed && !dryRun
	if book {
		_, _ = fmt.Fprintln(errOut, msgUnreviewed)
	}
	report, syncErr := ledger.Sync(ctx, items, target, ledger.SyncOptions{Repo: st, DryRun: !book, Now: deps.Now})
	printReport(out, report, !book)
	if !book && !dryRun {
		_, _ = fmt.Fprintln(out, msgPlanOnly)
	}
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

// dayItems derives the aggregated items of one local day from the store.
func dayItems(ctx context.Context, st *store.Store, cfg config.Config, day civil.Date, loc *time.Location) ([]sessionizer.DayItem, error) {
	from := time.Date(day.Year, day.Month, day.Day, 0, 0, 0, 0, loc)
	to := time.Date(day.Year, day.Month, day.Day+1, 0, 0, 0, 0, loc)
	events, err := st.Events(ctx, from, to)
	if err != nil {
		return nil, err
	}
	overrides, err := st.Overrides(ctx, from, to)
	if err != nil {
		return nil, err
	}
	rules := resolver.Rules{TicketPattern: cfg.TicketPattern}
	for _, r := range cfg.Rules {
		rules.Repos = append(rules.Repos, resolver.RepoRule(r))
	}
	blocks := sessionizer.Derive(events, overrides, sessionizer.ConfigFrom(cfg), rules)
	var items []sessionizer.DayItem
	for _, it := range sessionizer.Aggregate(blocks, sessionizer.RoundingConfigFrom(cfg), loc) {
		if it.Date == day {
			items = append(items, it)
		}
	}
	return items, nil
}

func printReport(out io.Writer, r ledger.Report, planOnly bool) {
	if len(r.Items) == 0 {
		_, _ = fmt.Fprintln(out, "No time to book.")
		return
	}
	for _, it := range r.Items {
		line := fmt.Sprintf("%-28s %4d min  %s", it.Key, it.Minutes, it.Action)
		switch {
		case it.Error != "":
			line += ": " + it.Error
		case it.Reason != "":
			line += " (" + it.Reason + ")"
		}
		_, _ = fmt.Fprintln(out, line)
	}
	if planOnly {
		_, _ = fmt.Fprintf(out, "Plan: %d to change, %d skipped, %d failed.\n", r.Planned, r.Skipped, r.Failed)
		return
	}
	_, _ = fmt.Fprintf(out, "Created %d, updated %d, recovered %d, skipped %d, failed %d, not sent %d.\n",
		r.Created, r.Updated, r.Recovered, r.Skipped, r.Failed, r.NotSent)
}
