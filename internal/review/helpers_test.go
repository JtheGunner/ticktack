package review

import (
	"regexp"

	"github.com/JtheGunner/ticktack/internal/config"
)

func mustPattern(p string) *regexp.Regexp { return regexp.MustCompile(p) }

func ruleFor(repo, ticket string) config.Rule { return config.Rule{Repo: repo, Ticket: ticket} }
