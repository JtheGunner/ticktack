package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/render"
	"github.com/JtheGunner/ticktack/internal/review"
	"github.com/JtheGunner/ticktack/internal/store"
)

func newMapCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "map",
		Short: msgMapMapShort,
		Long:  msgMapMapLong,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMapList(cmd.Context(), defaultDailyDeps(cmd))
		},
	}
	cmd.AddCommand(newMapListCommand(), newMapAddCommand(), newMapEditCommand(), newMapRmCommand())
	return cmd
}

func newMapListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: msgMapListShort,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMapList(cmd.Context(), defaultDailyDeps(cmd))
		},
	}
}

func newMapAddCommand() *cobra.Command {
	var repo, branch string
	cmd := &cobra.Command{
		Use:   "add --repo R [--branch B] TICKET",
		Short: msgMapAddShort,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMapAdd(cmd.Context(), defaultDailyDeps(cmd), repo, branch, args[0])
		},
	}
	cmd.Flags().StringVar(&repo, "repo", "", msgMapAddRepoFlag)
	cmd.Flags().StringVar(&branch, "branch", "", msgMapAddBranchFlag)
	return cmd
}

func newMapEditCommand() *cobra.Command {
	var repo, branch, ticket string
	cmd := &cobra.Command{
		Use:   "edit ID [--repo R] [--branch B] [--ticket T]",
		Short: msgMapEditShort,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			changed := cmd.Flags().Changed
			return runMapEdit(cmd.Context(), defaultDailyDeps(cmd), args[0], mapChanges{
				repo: ptrIf(changed("repo"), repo), branch: ptrIf(changed("branch"), branch), ticket: ptrIf(changed("ticket"), ticket),
			})
		},
	}
	cmd.Flags().StringVar(&repo, "repo", "", msgMapEditRepoFlag)
	cmd.Flags().StringVar(&branch, "branch", "", msgMapEditBranchFlag)
	cmd.Flags().StringVar(&ticket, "ticket", "", msgMapEditTicketFlag)
	return cmd
}

func newMapRmCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "rm ID",
		Short: msgMapRmShort,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMapRemove(cmd.Context(), defaultDailyDeps(cmd), args[0])
		},
	}
}

func ptrIf(set bool, v string) *string {
	if !set {
		return nil
	}
	return &v
}

// mapChanges holds the fields `map edit` was asked to change; nil is unchanged.
type mapChanges struct{ repo, branch, ticket *string }

func runMapList(ctx context.Context, deps dailyDeps) error {
	cfg, err := deps.LoadConfig()
	if err != nil {
		return err
	}
	path := msgMapUnknownPath
	if deps.ConfigPath != nil {
		if p, perr := deps.ConfigPath(); perr == nil {
			path = p
		}
	} else if p, perr := config.DefaultPath(); perr == nil {
		path = p
	}
	st, err := deps.OpenStore()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	learned, err := st.LearnedRules(ctx)
	if err != nil {
		return err
	}
	r := deps.Render
	lines := []string{r.Heading(msgMapConfigHeading), r.Gutter(render.StatusInfo, fmt.Sprintf(msgMapConfigNote, SanitizeForTerminal(path))), ""}
	if len(cfg.Rules) == 0 {
		lines = append(lines, r.Gutter(render.StatusInfo, msgMapNoConfigRules))
	} else {
		rows := make([][]string, 0, len(cfg.Rules))
		for _, rule := range cfg.Rules {
			rows = append(rows, []string{SanitizeForTerminal(rule.Repo), SanitizeForTerminal(rule.Ticket)})
		}
		lines = append(lines, r.Table([]render.Column{{Header: msgColRepo, Flex: true}, {Header: msgColTicket}}, rows)...)
	}
	lines = append(lines, "", r.Heading(msgMapLearnedHeading), r.Gutter(render.StatusInfo, msgMapLearnedNote), "")
	if len(learned) == 0 {
		lines = append(lines, r.Gutter(render.StatusInfo, msgMapNoLearned))
	} else {
		rows := make([][]string, 0, len(learned))
		for _, rule := range learned {
			rows = append(rows, []string{
				"#" + strconv.FormatInt(rule.ID, 10),
				SanitizeForTerminal(anyIfEmpty(rule.Repo)),
				SanitizeForTerminal(anyIfEmpty(rule.BranchPattern)),
				SanitizeForTerminal(rule.Ticket),
				rule.CreatedAt.In(cfg.Location).Format(dayLayout),
			})
		}
		lines = append(lines, r.Table([]render.Column{
			{Header: msgColID, Right: true},
			{Header: msgColRepo, Flex: true},
			{Header: msgColBranch},
			{Header: msgColTicket},
			{Header: msgColCreated},
		}, rows)...)
	}
	_, err = fmt.Fprintln(deps.Out, strings.Join(lines, "\n"))
	return err
}

// validateRule checks the user-supplied fields before they reach the store.
// repo and branch are trimmed; the repo must be absolute only when
// checkRepo is set, so a stored rule with an older relative repo can still
// have its ticket edited.
func validateRule(cfg config.Config, repo, branch, ticket string, checkRepo bool) (string, error) {
	repo, branch = strings.TrimSpace(repo), strings.TrimSpace(branch)
	if repo == "" && branch == "" {
		return "", errors.New(msgMapEmptyRule)
	}
	if checkRepo && repo != "" && !filepath.IsAbs(repo) {
		return "", fmt.Errorf(msgMapRelativeRepo, SanitizeForTerminal(repo))
	}
	return review.NormalizeTicket(cfg, ticket)
}

func runMapAdd(ctx context.Context, deps dailyDeps, repo, branch, ticket string) error {
	cfg, err := deps.LoadConfig()
	if err != nil {
		return err
	}
	repo, branch = strings.TrimSpace(repo), strings.TrimSpace(branch)
	normalized, err := validateRule(cfg, repo, branch, ticket, true)
	if err != nil {
		return err
	}
	st, err := deps.OpenStore()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	var rule store.LearnedRule
	err = changeRule(ctx, st, deps, store.RuleAdded, func(tx store.Tx) error {
		var err error
		rule, err = tx.AddLearnedRule(ctx, store.LearnedRule{Repo: repo, BranchPattern: branch, Ticket: normalized, CreatedAt: deps.Now()})
		return err
	})
	if err != nil {
		return err
	}
	printRuleChange(deps, msgMapAdded, rule)
	return nil
}

func runMapEdit(ctx context.Context, deps dailyDeps, idArg string, changes mapChanges) error {
	id, err := parseRuleID(idArg)
	if err != nil {
		return err
	}
	if changes.repo == nil && changes.branch == nil && changes.ticket == nil {
		return errors.New(msgMapNothingToEdit)
	}
	cfg, err := deps.LoadConfig()
	if err != nil {
		return err
	}
	st, err := deps.OpenStore()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	rule, err := findLearnedRule(ctx, st, id)
	if err != nil {
		return err
	}
	if changes.repo != nil {
		rule.Repo = strings.TrimSpace(*changes.repo)
	}
	if changes.branch != nil {
		rule.BranchPattern = strings.TrimSpace(*changes.branch)
	}
	if changes.ticket != nil {
		rule.Ticket = *changes.ticket
	}
	if rule.Ticket, err = validateRule(cfg, rule.Repo, rule.BranchPattern, rule.Ticket, changes.repo != nil); err != nil {
		return err
	}
	err = changeRule(ctx, st, deps, store.RuleEdited, func(tx store.Tx) error { return tx.UpdateLearnedRule(ctx, rule) })
	if err != nil {
		return mapStoreError(err, id)
	}
	printRuleChange(deps, msgMapEdited, rule)
	return nil
}

func runMapRemove(ctx context.Context, deps dailyDeps, idArg string) error {
	id, err := parseRuleID(idArg)
	if err != nil {
		return err
	}
	st, err := deps.OpenStore()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	err = changeRule(ctx, st, deps, store.RuleRemoved, func(tx store.Tx) error { return tx.DeleteLearnedRule(ctx, id) })
	if err != nil {
		return mapStoreError(err, id)
	}
	_, _ = fmt.Fprintln(deps.Out, deps.Render.Gutter(render.StatusOK, fmt.Sprintf(msgMapRemoved, id)))
	_, _ = fmt.Fprintln(deps.Out, deps.Render.Gutter(render.StatusInfo, msgMapRederive))
	return nil
}

// changeRule runs a rule write and records it as a rule change in one
// transaction. The trust gate of auto mode counts every recorded change as a
// correction on its calendar day, so the two must never drift apart.
func changeRule(ctx context.Context, st dailyStore, deps dailyDeps, action store.RuleChangeAction, write func(tx store.Tx) error) error {
	return st.InTx(ctx, func(tx store.Tx) error {
		if err := write(tx); err != nil {
			return err
		}
		return tx.RecordRuleChange(ctx, store.RuleChange{ChangedAt: deps.Now(), Action: action})
	})
}

func findLearnedRule(ctx context.Context, st dailyStore, id int64) (store.LearnedRule, error) {
	rules, err := st.LearnedRules(ctx)
	if err != nil {
		return store.LearnedRule{}, err
	}
	for _, r := range rules {
		if r.ID == id {
			return r, nil
		}
	}
	return store.LearnedRule{}, msgMapUnknownID(id)
}

func mapStoreError(err error, id int64) error {
	if errors.Is(err, store.ErrNoSuchLearnedRule) {
		return msgMapUnknownID(id)
	}
	return err
}

func parseRuleID(arg string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimPrefix(strings.TrimSpace(arg), "#"), 10, 64)
	if err != nil || id < 1 {
		return 0, msgMapBadID(SanitizeForTerminal(arg))
	}
	return id, nil
}

func printRuleChange(deps dailyDeps, format string, r store.LearnedRule) {
	text := fmt.Sprintf(format, r.ID, SanitizeForTerminal(anyIfEmpty(r.Repo)),
		SanitizeForTerminal(anyIfEmpty(r.BranchPattern)), SanitizeForTerminal(r.Ticket))
	_, _ = fmt.Fprintln(deps.Out, deps.Render.Gutter(render.StatusOK, text))
	_, _ = fmt.Fprintln(deps.Out, deps.Render.Gutter(render.StatusInfo, msgMapRederive))
}
