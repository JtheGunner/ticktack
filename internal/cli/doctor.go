package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/JtheGunner/ticktack/internal/collector/activitywatch"
	"github.com/JtheGunner/ticktack/internal/collector/presence"
	"github.com/JtheGunner/ticktack/internal/collector/title"
	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/paths"
	"github.com/JtheGunner/ticktack/internal/render"
	"github.com/JtheGunner/ticktack/internal/sink"
	"github.com/JtheGunner/ticktack/internal/store"
	"github.com/JtheGunner/ticktack/internal/token"
	"github.com/JtheGunner/ticktack/internal/youtrack"
	"github.com/JtheGunner/ticktack/launchd"
)

const (
	hookFreshness = 24 * time.Hour
	httpTimeout   = 10 * time.Second
)

// checkStatus is the outcome of one doctor check.
type checkStatus int

const (
	statusOK checkStatus = iota
	statusFail
	statusSkip
	// statusWarn marks something worth a look that does not count as a failed
	// check and does not change the exit code.
	statusWarn
	// statusInfo marks an informational hint: printed, but not counted as a
	// check and never a failure.
	statusInfo
)

// checkResult is what a single check reports. Hint is set on failure and
// warning only.
type checkResult struct {
	Name   string
	Status checkStatus
	Detail string
	Hint   string
}

func okResult(name, detail string) checkResult {
	return checkResult{Name: name, Status: statusOK, Detail: detail}
}

func failResult(name, detail, hint string) checkResult {
	return checkResult{Name: name, Status: statusFail, Detail: detail, Hint: hint}
}

func warnResult(name, detail, hint string) checkResult {
	return checkResult{Name: name, Status: statusWarn, Detail: detail, Hint: hint}
}

func skipResult(name, detail string) checkResult {
	return checkResult{Name: name, Status: statusSkip, Detail: detail}
}

func infoResult(name, detail string) checkResult {
	return checkResult{Name: name, Status: statusInfo, Detail: detail}
}

// meClient is the part of the YouTrack client the token check needs.
type meClient interface {
	Me(ctx context.Context) (string, error)
}

// eventReader is the part of the store the doctor needs.
type eventReader interface {
	Events(ctx context.Context, from, to time.Time) ([]event.Event, error)
	Close() error
}

// doctorDeps holds everything the checks touch outside of pure logic.
type doctorDeps struct {
	Home string
	// ZDotDir is the value of $ZDOTDIR; zsh reads its startup files from there
	// when it is set.
	ZDotDir    string
	GOOS       string
	UID        int
	Now        func() time.Time
	ConfigPath func() (string, error)
	DBPath     func() (string, error)
	// PausePath returns the path of the pause flag file.
	PausePath func() (string, error)
	// TitleStatusPath returns the path of the status file the agent writes
	// after each window title probe.
	TitleStatusPath func() (string, error)
	TokenGet        func() (string, error)
	NewMe           func(baseURL, token string) (meClient, error)
	HTTP            *http.Client
	OpenStore       func(path string) (eventReader, error)
	LookPath        func(file string) (string, error)
	// Binary returns the path of the running executable; the agent check
	// compares the program of the launchd plist with it.
	Binary func() (string, error)
	// NewActivityWatch builds the bucket lister for the optional ActivityWatch
	// check; tests inject a client bound to an httptest server.
	NewActivityWatch func(url string) (bucketLister, error)
	// Render styles the output; the zero value renders plain text.
	Render render.Renderer
}

// bucketLister is the part of the ActivityWatch client the doctor needs.
type bucketLister interface {
	Buckets(ctx context.Context) ([]activitywatch.Bucket, error)
}

// defaultDoctorDeps wires the real dependencies; the renderer follows the
// command's output stream.
func defaultDoctorDeps(cmd *cobra.Command) doctorDeps {
	home, _ := os.UserHomeDir()
	return doctorDeps{
		Render:          rendererFor(cmd, cmd.OutOrStdout()),
		Home:            home,
		ZDotDir:         os.Getenv("ZDOTDIR"),
		GOOS:            runtime.GOOS,
		UID:             os.Getuid(),
		Now:             func() time.Time { return time.Now().Round(0) },
		ConfigPath:      config.DefaultPath,
		DBPath:          paths.DBPath,
		PausePath:       paths.PausePath,
		TitleStatusPath: paths.TitleStatusPath,
		TokenGet:        token.Get,
		NewMe: func(baseURL, tok string) (meClient, error) {
			return youtrack.New(baseURL, tok,
				youtrack.WithHTTPClient(&http.Client{Timeout: httpTimeout}),
				youtrack.WithMaxRetries(0))
		},
		HTTP: &http.Client{Timeout: httpTimeout},
		OpenStore: func(path string) (eventReader, error) {
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return nil, fmt.Errorf("create data directory: %w", err)
			}
			st, err := store.Open(path)
			if err != nil {
				return nil, err
			}
			return st, nil
		},
		LookPath: exec.LookPath,
		Binary:   currentBinary,
		NewActivityWatch: func(url string) (bucketLister, error) {
			return activitywatch.New(url)
		},
	}
}

func newDoctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check the setup and name the fix for each problem",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDoctor(cmd.Context(), defaultDoctorDeps(cmd), cmd.OutOrStdout())
		},
	}
}

// runDoctor runs every check independently, prints one line per check and
// returns ErrChecksFailed when any check failed.
func runDoctor(ctx context.Context, deps doctorDeps, out io.Writer) error {
	checks := []func(context.Context, doctorDeps) checkResult{
		checkConfig,
		checkHook,
		checkAgent,
		checkToken,
		checkReachable,
		checkDB,
		checkTools,
		checkActivityWatch,
		checkTitleHint,
		checkRepositories,
		checkRepoRoots,
		checkStandardTasks,
		checkAlias,
	}
	results := make([]checkResult, 0, len(checks))
	for _, check := range checks {
		results = append(results, check(ctx, deps))
	}
	if failed := writeDoctorReport(out, deps.Render, results); failed > 0 {
		return ErrChecksFailed
	}
	return nil
}

// writeDoctorReport prints one line per result and a summary box, coloured
// green when every check passed and red otherwise. The summary text states
// the outcome, so it reads without colour. It returns the number of failed
// checks.
func writeDoctorReport(out io.Writer, r render.Renderer, results []checkResult) int {
	failed, warned, checks := 0, 0, 0
	for _, res := range results {
		printResult(out, r, res)
		if res.Status == statusInfo {
			continue
		}
		checks++
		switch res.Status {
		case statusFail:
			failed++
		case statusWarn:
			warned++
		}
	}
	summary := fmt.Sprintf(msgDoctorSummaryOK, checks)
	status, paint := render.StatusOK, r.Colors.OK
	switch {
	case failed > 0:
		summary = fmt.Sprintf(msgDoctorSummaryFailed, failed, checks)
		status, paint = render.StatusFail, r.Colors.Fail
	case warned > 0:
		summary = fmt.Sprintf(msgDoctorSummaryWarned, warned, checks)
		status, paint = render.StatusWarn, r.Colors.Warn
	}
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, r.BoxStatus(status, msgDoctorSummaryTitle, []string{paint(summary)}))
	return failed
}

// printResult writes one gutter line per check and, for a failure with a
// hint, an indented follow-up line naming the fix. External text is
// sanitised before it is styled.
func printResult(out io.Writer, r render.Renderer, res checkResult) {
	status := render.StatusOK
	switch res.Status {
	case statusFail:
		status = render.StatusFail
	case statusWarn:
		status = render.StatusWarn
	case statusSkip:
		status = render.StatusSkip
	case statusInfo:
		status = render.StatusInfo
	}
	detail := ": " + SanitizeForTerminal(res.Detail)
	if res.Status != statusFail && res.Status != statusWarn {
		// A failure's or warning's diagnostic is what the user must read; it
		// stays at normal intensity.
		detail = r.Colors.Dim(detail)
	}
	_, _ = fmt.Fprintln(out, r.Gutter(status, r.Colors.Bold(res.Name)+detail))
	if (res.Status == statusFail || res.Status == statusWarn) && res.Hint != "" {
		_, _ = fmt.Fprintln(out, hintIndent+r.Gutter(render.StatusGo, msgFixPrefix+SanitizeForTerminal(res.Hint)))
	}
}

// hintIndent aligns a fix line under the check name of the gutter line above.
const hintIndent = "   "

// loadConfig resolves the config path and loads the file.
func loadConfig(deps doctorDeps) (config.Config, string, error) {
	path, err := deps.ConfigPath()
	if err != nil {
		return config.Config{}, "", err
	}
	cfg, err := config.Load(path)
	return cfg, path, err
}

func checkConfig(_ context.Context, deps doctorDeps) checkResult {
	path, err := deps.ConfigPath()
	if err != nil {
		return failResult(msgCheckConfig, fmt.Sprintf(msgConfigPathBad, err), msgConfigPathFix)
	}
	if _, err := config.Load(path); err != nil {
		return failResult(msgCheckConfig, fmt.Sprintf(msgConfigBad, err), fmt.Sprintf(msgConfigFix, path))
	}
	return okResult(msgCheckConfig, fmt.Sprintf(msgConfigOK, path))
}

// installedRCFile returns the first shell rc file holding a managed block.
func installedRCFile(deps doctorDeps) (string, bool) {
	for _, path := range rcFileCandidates(deps.Home, deps.ZDotDir) {
		data, err := os.ReadFile(path)
		if err == nil && hasManagedBlock(string(data)) {
			return path, true
		}
	}
	return "", false
}

// newestEvent returns the time of the newest event of source within window.
func newestEvent(ctx context.Context, deps doctorDeps, source string, window time.Duration) (time.Time, bool, error) {
	dbPath, err := deps.DBPath()
	if err != nil {
		return time.Time{}, false, err
	}
	st, err := deps.OpenStore(dbPath)
	if err != nil {
		return time.Time{}, false, err
	}
	defer func() { _ = st.Close() }()
	now := deps.Now()
	events, err := st.Events(ctx, now.Add(-window), now.Add(time.Minute))
	if err != nil {
		return time.Time{}, false, err
	}
	var newest time.Time
	found := false
	for _, e := range events {
		if e.Source == source && e.At.After(newest) {
			newest, found = e.At, true
		}
	}
	return newest, found, nil
}

// trackingPaused reports whether the pause flag file exists. A flag path that cannot
// be resolved counts as not paused.
func trackingPaused(deps doctorDeps) bool {
	if deps.PausePath == nil {
		return false
	}
	path, err := deps.PausePath()
	return err == nil && fileExists(path)
}

func checkHook(ctx context.Context, deps doctorDeps) checkResult {
	rc, installed := installedRCFile(deps)
	if !installed {
		return failResult(msgCheckHook, msgHookBlockMissing, msgHookBlockFix)
	}
	at, found, err := newestEvent(ctx, deps, hookSource, hookFreshness)
	if err != nil {
		return failResult(msgCheckHook, fmt.Sprintf(msgHookStoreErr, rc, err), msgHookStoreFix)
	}
	if !found {
		if trackingPaused(deps) {
			return okResult(msgCheckHook, fmt.Sprintf(msgHookPaused, rc))
		}
		// A quiet day is no defect: the block is in place.
		return warnResult(msgCheckHook, fmt.Sprintf(msgHookNoEvent, rc), msgHookNoEventFix)
	}
	return okResult(msgCheckHook, fmt.Sprintf(msgHookOK, rc, deps.Now().Sub(at).Round(time.Second)))
}

// agentPlistPath is where `init` installs the launchd agent.
func agentPlistPath(home string) string {
	return filepath.Join(home, "Library", "LaunchAgents", launchd.Label+".plist")
}

func checkAgent(ctx context.Context, deps doctorDeps) checkResult {
	if deps.GOOS != "darwin" {
		return skipResult(msgCheckAgent, msgAgentNotMac)
	}
	plist := agentPlistPath(deps.Home)
	if !fileExists(plist) {
		return failResult(msgCheckAgent, fmt.Sprintf(msgAgentNoPlist, plist), msgAgentNoPlistFix)
	}
	if problem, bad := checkAgentProgram(deps, plist); bad {
		return problem
	}
	window := 2 * installedAgentInterval(plist)
	at, found, err := newestEvent(ctx, deps, "presence", window)
	if err != nil {
		return failResult(msgCheckAgent, fmt.Sprintf(msgAgentStoreErr, err), msgHookStoreFix)
	}
	if !found {
		if trackingPaused(deps) {
			// The agent stores no samples while paused.
			return okResult(msgCheckAgent, msgAgentPaused)
		}
		return failResult(msgCheckAgent, fmt.Sprintf(msgAgentNoSample, window),
			fmt.Sprintf(msgAgentNoSampleFix, deps.UID, plist))
	}
	return okResult(msgCheckAgent, fmt.Sprintf(msgAgentOK, deps.Now().Sub(at).Round(time.Second)))
}

// checkAgentProgram verifies that the program the launchd plist runs exists
// and is the running binary, meaning both paths lead to the same file. It
// reports true together with the failure when it is not.
func checkAgentProgram(deps doctorDeps, plist string) (checkResult, bool) {
	data, err := os.ReadFile(plist)
	if err != nil {
		return failResult(msgCheckAgent, fmt.Sprintf(msgAgentPlistBad, plist, err), msgAgentProgramFix), true
	}
	program, err := launchd.ProgramPath(data)
	if err != nil {
		return failResult(msgCheckAgent, fmt.Sprintf(msgAgentPlistBad, plist, err), msgAgentProgramFix), true
	}
	if _, err := os.Stat(program); err != nil {
		return failResult(msgCheckAgent, fmt.Sprintf(msgAgentProgramMissing, program), msgAgentProgramFix), true
	}
	current, err := deps.Binary()
	if err != nil {
		return failResult(msgCheckAgent, fmt.Sprintf(msgAgentBinaryUnknown, err), msgAgentProgramFix), true
	}
	if !sameFile(program, current) {
		return failResult(msgCheckAgent, fmt.Sprintf(msgAgentProgramDiffers, program, current), msgAgentProgramFix), true
	}
	return checkResult{}, false
}

// installedAgentInterval returns the sampling interval the installed launchd
// agent runs with: the --interval argument of its plist, or the default.
func installedAgentInterval(plistPath string) time.Duration {
	data, err := os.ReadFile(plistPath)
	if err != nil {
		return presence.DefaultInterval
	}
	return parseAgentInterval(data)
}

// maxAgentInterval bounds an accepted interval so that doubling it for the
// freshness window can never overflow; anything longer is not a sane sampling
// interval and falls back to the default.
const maxAgentInterval = 24 * time.Hour

var plistStringRe = regexp.MustCompile("<string>([^<]*)</string>")

// parseAgentInterval reads "--interval 2m" or "--interval=2m" from the
// program arguments of a launchd plist. A missing, unparsable or non-positive
// value (or one above maxAgentInterval) yields the default.
func parseAgentInterval(plist []byte) time.Duration {
	var args []string
	for _, m := range plistStringRe.FindAllSubmatch(plist, -1) {
		args = append(args, string(m[1]))
	}
	for i, a := range args {
		value, ok := strings.CutPrefix(a, "--interval=")
		if !ok {
			if a != "--interval" || i+1 >= len(args) {
				continue
			}
			value = args[i+1]
		}
		if d, err := time.ParseDuration(value); err == nil && d > 0 && d <= maxAgentInterval {
			return d
		}
	}
	return presence.DefaultInterval
}

func checkToken(ctx context.Context, deps doctorDeps) checkResult {
	tok, err := deps.TokenGet()
	if errors.Is(err, token.ErrNotSet) {
		return failResult(msgCheckToken, msgTokenNone, msgTokenNoneFix)
	}
	if err != nil {
		return failResult(msgCheckToken, fmt.Sprintf(msgTokenKeychain, err), msgTokenKeychainFix)
	}
	cfg, path, err := loadConfig(deps)
	if err != nil || cfg.YouTrackURL == "" {
		return failResult(msgCheckToken, msgTokenNoURL, fmt.Sprintf(msgTokenNoURLFix, path))
	}
	client, err := deps.NewMe(cfg.YouTrackURL, tok)
	if err != nil {
		return failResult(msgCheckToken, fmt.Sprintf(msgTokenUnreachable, err), msgTokenUnreachFix)
	}
	login, err := client.Me(ctx)
	switch {
	case errors.Is(err, sink.ErrUnauthorized):
		return failResult(msgCheckToken, msgTokenUnauthorized, msgTokenUnauthFix)
	case errors.Is(err, sink.ErrForbidden):
		return failResult(msgCheckToken, msgTokenForbidden, msgTokenForbiddenFix)
	case err != nil:
		return failResult(msgCheckToken, fmt.Sprintf(msgTokenUnreachable, err), msgTokenUnreachFix)
	}
	return okResult(msgCheckToken, fmt.Sprintf(msgTokenOK, login))
}

func checkReachable(ctx context.Context, deps doctorDeps) checkResult {
	cfg, path, err := loadConfig(deps)
	if err != nil || cfg.YouTrackURL == "" {
		return failResult(msgCheckReachable, msgReachableNoURL, msgReachableNoURLFix+" "+path)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.YouTrackURL, nil)
	if err != nil {
		return failResult(msgCheckReachable, fmt.Sprintf(msgReachableFail, cfg.YouTrackURL, err), msgReachableFix)
	}
	resp, err := deps.HTTP.Do(req)
	if err != nil {
		return failResult(msgCheckReachable, fmt.Sprintf(msgReachableFail, cfg.YouTrackURL, err), msgReachableFix)
	}
	_ = resp.Body.Close()
	return okResult(msgCheckReachable, fmt.Sprintf(msgReachableOK, cfg.YouTrackURL, resp.StatusCode))
}

// checkDB opens the store, which creates it and applies pending migrations.
// That is a real write, but it adds no event. A database file without write
// permission, or one SQLite cannot write to, fails with a fix hint.
func checkDB(ctx context.Context, deps doctorDeps) checkResult {
	path, err := deps.DBPath()
	if err != nil {
		return failResult(msgCheckDB, fmt.Sprintf(msgDBFail, "database path", err), msgDBFix)
	}
	if info, err := os.Stat(path); err == nil && info.Mode().Perm()&0o200 == 0 {
		return failResult(msgCheckDB, fmt.Sprintf(msgDBReadOnly, path), fmt.Sprintf(msgDBReadOnlyFix, path))
	}
	st, err := deps.OpenStore(path)
	if err != nil {
		return failResult(msgCheckDB, fmt.Sprintf(msgDBFail, path, err), msgDBFix)
	}
	if w, ok := st.(interface{ CheckWritable(context.Context) error }); ok {
		if err := w.CheckWritable(ctx); errors.Is(err, store.ErrBusy) {
			// Another process (normally the agent) holds the write lock: the
			// database is writable, just busy right now.
			_ = st.Close()
			return okResult(msgCheckDB, fmt.Sprintf(msgDBBusy, path))
		} else if err != nil {
			_ = st.Close()
			return failResult(msgCheckDB, fmt.Sprintf(msgDBReadOnlyProbe, path, err), fmt.Sprintf(msgDBReadOnlyFix, path))
		}
	}
	if err := st.Close(); err != nil {
		return failResult(msgCheckDB, fmt.Sprintf(msgDBFail, path, err), msgDBFix)
	}
	return okResult(msgCheckDB, fmt.Sprintf(msgDBOK, path))
}

func checkTools(_ context.Context, deps doctorDeps) checkResult {
	if deps.GOOS != "darwin" {
		return skipResult(msgCheckTools, msgToolsNotMac)
	}
	var missing []string
	for _, tool := range []string{"ioreg", "lsappinfo"} {
		if _, err := deps.LookPath(tool); err != nil {
			missing = append(missing, tool)
		}
	}
	if len(missing) > 0 {
		return failResult(msgCheckTools, fmt.Sprintf(msgToolsMissing, strings.Join(missing, ", ")), msgToolsFix)
	}
	return okResult(msgCheckTools, msgToolsOK)
}

// checkActivityWatch is independent of the other checks and is skipped, never
// failed, while the optional importer is disabled.
func checkActivityWatch(ctx context.Context, deps doctorDeps) checkResult {
	cfg, _, err := loadConfig(deps)
	if err != nil || !cfg.ActivityWatch.Enabled {
		return skipResult(msgCheckActivityWatch, msgAWDisabled)
	}
	aw := cfg.ActivityWatch
	client, err := deps.NewActivityWatch(aw.URL)
	if err != nil {
		return failResult(msgCheckActivityWatch, fmt.Sprintf(msgAWBadURL, err), msgAWBadURLFix)
	}
	buckets, err := client.Buckets(ctx)
	if err != nil {
		return failResult(msgCheckActivityWatch, fmt.Sprintf(msgAWUnreachable, aw.URL, err), msgAWUnreachableFix)
	}
	afk, window := activitywatch.SelectBuckets(buckets, aw.Hostname)
	if len(afk)+len(window) == 0 {
		return failResult(msgCheckActivityWatch, fmt.Sprintf(msgAWNoBuckets, aw.URL), msgAWNoBucketsFix)
	}
	return okResult(msgCheckActivityWatch, fmt.Sprintf(msgAWOK, aw.URL, len(afk)+len(window)))
}

// titleStatusStale is the age after which the title probe status no longer
// shows what the agent experiences now. The agent refreshes it every few
// minutes while it runs and is not paused.
const titleStatusStale = 15 * time.Minute

// checkTitleHint is skipped unless the title hint feature is enabled. When it
// is, it reports the outcome of the last title probe of the launchd agent from
// its status file. The check never reads a window title itself: the terminal
// running `doctor` may hold different permissions than the agent, and only the
// agent's experience counts. Without a status file the result is "not checked
// yet".
func checkTitleHint(_ context.Context, deps doctorDeps) checkResult {
	cfg, _, err := loadConfig(deps)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return skipResult(msgCheckTitleHint, msgTitleConfigBad)
	}
	if !cfg.TitleHint.Enabled {
		return skipResult(msgCheckTitleHint, msgTitleDisabled)
	}
	if deps.GOOS != "darwin" {
		return skipResult(msgCheckTitleHint, msgTitleNotMac)
	}
	statusPath, err := deps.TitleStatusPath()
	if err != nil {
		return skipResult(msgCheckTitleHint, msgTitleNotChecked)
	}
	status, err := title.ReadStatus(statusPath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return skipResult(msgCheckTitleHint, msgTitleNotChecked)
	case err != nil:
		return warnResult(msgCheckTitleHint, fmt.Sprintf(msgTitleStatusBad, err), msgTitleStatusBadFix)
	}
	age := deps.Now().Sub(status.At).Round(time.Second)
	if age > titleStatusStale && !trackingPaused(deps) {
		return warnResult(msgCheckTitleHint, fmt.Sprintf(msgTitleStale, age), msgTitleStaleFix)
	}
	switch status.State {
	case title.ProbeOK:
		return okResult(msgCheckTitleHint, fmt.Sprintf(msgTitleOK, age))
	case title.ProbeDenied:
		return failResult(msgCheckTitleHint, fmt.Sprintf(msgTitleDenied, age), msgTitleDeniedFix)
	default:
		return failResult(msgCheckTitleHint, fmt.Sprintf(msgTitleReadFail, age), msgTitleReadFix)
	}
}

// checkRepositories is independent of the other checks and is skipped while
// repo_paths is empty. Otherwise it inspects every configured path and names
// each unusable one with its reason and fix.
func checkRepositories(_ context.Context, deps doctorDeps) checkResult {
	cfg, path, err := loadConfig(deps)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return skipResult(msgCheckRepos, msgReposConfigBad)
	}
	repos := trackedRepos(cfg, deps.Home)
	if len(repos) == 0 {
		return skipResult(msgCheckRepos, msgReposNone)
	}
	var details, fixes []string
	for _, raw := range repos {
		expanded, err := config.ExpandRepoPath(raw, deps.Home)
		if err != nil {
			details = append(details, fmt.Sprintf("%s %s", raw, fmt.Sprintf(msgRepoBadPath, err)))
			fixes = append(fixes, raw+": "+msgReposFixBadPath)
			continue
		}
		if problem := inspectRepoPath(expanded); problem != nil {
			details = append(details, fmt.Sprintf("%s %s", raw, problem.Reason))
			fix := problem.Fix
			if strings.Contains(fix, "%s") {
				fix = fmt.Sprintf(fix, path)
			}
			fixes = append(fixes, raw+": "+fix)
		}
	}
	if len(details) == 0 {
		return okResult(msgCheckRepos, fmt.Sprintf(msgReposOK, len(repos)))
	}
	return failResult(msgCheckRepos,
		fmt.Sprintf(msgReposFail, len(details), len(repos), strings.Join(details, msgReposDetailJoiner)),
		strings.Join(fixes, msgReposDetailJoiner))
}
