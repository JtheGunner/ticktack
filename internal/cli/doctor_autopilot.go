package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

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
	return fmt.Sprintf(msgAutopilotLastRun, formatAge(deps.Now().Sub(digest.CreatedAt)))
}
