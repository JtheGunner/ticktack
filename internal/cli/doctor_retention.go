package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/JtheGunner/ticktack/internal/store"
)

// archivedDayLister is the part of the store the retention check reads.
type archivedDayLister interface {
	ArchivedDays(ctx context.Context) ([]store.ArchivedDay, error)
}

// checkRetention states whether the retention cleanup is on and how many days
// it has archived so far. It is informational and never fails: an off setting
// is shown as skipped (optional), and a database that cannot be read is
// skipped too, because the database check reports that problem.
func checkRetention(ctx context.Context, deps doctorDeps) checkResult {
	cfg, _, err := loadConfig(deps)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return skipResult(msgCheckRetention, msgRetentionConfigBad)
	}
	dbPath, err := deps.DBPath()
	if err != nil {
		return skipResult(msgCheckRetention, fmt.Sprintf(msgRetentionDBFail, err))
	}
	st, err := deps.OpenStore(dbPath)
	if err != nil {
		return skipResult(msgCheckRetention, fmt.Sprintf(msgRetentionDBFail, err))
	}
	defer func() { _ = st.Close() }()
	archived := 0
	if lister, ok := st.(archivedDayLister); ok {
		days, err := lister.ArchivedDays(ctx)
		if err != nil {
			return skipResult(msgCheckRetention, fmt.Sprintf(msgRetentionDBFail, err))
		}
		archived = len(days)
	}
	if cfg.RetentionDays <= 0 {
		return skipResult(msgCheckRetention, fmt.Sprintf(msgRetentionOff, archived))
	}
	return okResult(msgCheckRetention, fmt.Sprintf(msgRetentionOn, cfg.RetentionDays, archived))
}
