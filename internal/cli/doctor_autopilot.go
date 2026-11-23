package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strconv"
	"strings"

	"github.com/JtheGunner/ticktack/internal/auto"
	"github.com/JtheGunner/ticktack/internal/store"
)

// digestReader is the part of the store the autopilot check reads.
type digestReader interface {
	LastDigest(ctx context.Context) (store.Digest, bool, error)
}

// checkAutopilot states whether the autopilot is off, gated or active, and
// when the last digest was written. It is informational and never fails: a
// configuration that cannot be read is skipped, because the config check
// reports that.
func checkAutopilot(ctx context.Context, deps doctorDeps) checkResult {
	cfg, _, err := loadConfig(deps)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return skipResult(msgCheckAutopilot, msgAutopilotConfigBad)
	}
	switch {
	case !cfg.Auto.Enabled:
		return skipResult(msgCheckAutopilot, msgAutopilotOff)
	case !cfg.Auto.Autopilot():
		return infoResult(msgCheckAutopilot, msgAutopilotGated)
	}
	return infoResult(msgCheckAutopilot, fmt.Sprintf(msgAutopilotActive, auto.LookbackDays(cfg.Auto))+"; "+lastDigestAge(ctx, deps))
}

// lastDigestAge describes when the last auto digest was written, which is the
// last time a run booked, changed or failed something. A database that cannot
// be read says nothing: the database check reports that.
func lastDigestAge(ctx context.Context, deps doctorDeps) string {
	dbPath, err := deps.DBPath()
	if err != nil {
		return msgAutopilotNoRun
	}
	st, err := deps.OpenStore(dbPath)
	if err != nil {
		return msgAutopilotNoRun
	}
	defer func() { _ = st.Close() }()
	reader, ok := st.(digestReader)
	if !ok {
		return msgAutopilotNoRun
	}
	digest, found, err := reader.LastDigest(ctx)
	if err != nil || !found {
		return msgAutopilotNoRun
	}
	age := formatAge(deps.Now().Sub(digest.CreatedAt))
	failed, notSent, readable := digestFailureCounts(digest.Text)
	switch {
	case !readable:
		// Never healthy on a doubt: a digest whose totals cannot be read is shown
		// as such.
		return fmt.Sprintf(msgAutopilotLastUnreadable, age)
	case failed > 0 || notSent > 0:
		return fmt.Sprintf(msgAutopilotLastFail, failed+notSent, failed, notSent, age)
	}
	return fmt.Sprintf(msgAutopilotLastRun, age)
}

// digestTotalsLine matches the whole totals line that renderDigest writes, from
// the same format string, alone on its line after the one-glyph gutter of the
// stored digest. Item texts of the digest come from the YouTrack server; they
// are sanitised to one line inside a table row, so none of them can open a
// line of its own, and a long one that the table wraps is still not the last
// line: the real totals line follows all entries and the caller takes the last
// match.
var digestTotalsLine = regexp.MustCompile(`(?m)^[ \t]*\S[ \t]+` +
	strings.NewReplacer("%d", `(\d+)`, "%s", `.+?`).Replace(regexp.QuoteMeta(msgDigestTotalsFormat)) + "$")

// Positions of the failed and not-sent counts among the %d verbs of the format.
const (
	totalsFailedGroup  = 7
	totalsNotSentGroup = 8
)

// digestFailureCounts reads the failed and not-sent counts from the last totals
// line of a stored digest text. It reports readable false when there is no
// totals line or a count does not fit an int, so a doubt is never taken for a
// run without failures.
func digestFailureCounts(text string) (failed, notSent int, readable bool) {
	all := digestTotalsLine.FindAllStringSubmatch(text, -1)
	if len(all) == 0 {
		return 0, 0, false
	}
	m := all[len(all)-1]
	failed, err := strconv.Atoi(m[totalsFailedGroup])
	if err != nil {
		return 0, 0, false
	}
	notSent, err = strconv.Atoi(m[totalsNotSentGroup])
	if err != nil {
		return 0, 0, false
	}
	return failed, notSent, true
}
