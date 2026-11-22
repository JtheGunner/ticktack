package cli

import (
	"github.com/JtheGunner/ticktack/internal/commitsubject"
	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/ledger"
)

// describer returns the describer that puts commit subjects into the booked
// text, or nil when description_from_commits is off. The subjects are read
// from the repository by deps.CommitReader, the local git by default.
func (d syncDeps) describer(cfg config.Config) ledger.Describer {
	if !cfg.DescriptionFromCommits {
		return nil
	}
	reader := d.CommitReader
	if reader == nil {
		reader = commitsubject.GitReader{}
	}
	return commitsubject.Describer{Reader: reader}
}
