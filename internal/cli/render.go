package cli

import (
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/JtheGunner/ticktack/internal/ledger"
	"github.com/JtheGunner/ticktack/internal/render"
	"github.com/JtheGunner/ticktack/internal/resolver"
)

// noColorFlag is the name of the persistent root flag that disables colour.
const noColorFlag = "no-color"

// rendererFor builds the renderer for w from the command's --no-color flag,
// the real environment and the real terminal.
//
// Wiring rule: every interactive deps struct gets its Render from
// rendererFor(cmd, out) in its default...Deps(cmd) constructor. Use
// syncDeps.forCommand(cmd) only when the deps are built before the command
// exists.
func rendererFor(cmd *cobra.Command, w io.Writer) render.Renderer {
	return rendererWith(cmd, w, render.Options{Getenv: os.Getenv})
}

// rendererWith is rendererFor with injectable environment and terminal size.
func rendererWith(cmd *cobra.Command, w io.Writer, opts render.Options) render.Renderer {
	return render.New(w, applyNoColorFlag(cmd, opts))
}

// applyNoColorFlag turns opts.NoColor on when the command's --no-color flag is
// set. A command without the flag leaves opts unchanged.
func applyNoColorFlag(cmd *cobra.Command, opts render.Options) render.Options {
	if noColor, err := cmd.Flags().GetBool(noColorFlag); err == nil && noColor {
		opts.NoColor = true
	}
	return opts
}

// colorTier styles a confidence tier by trust: explicit and rule are green,
// learned is accent, suggested is a warning and unknown is dim. The tier name
// is always printed as text, so it stays readable without colour.
func colorTier(r render.Renderer, tier resolver.Tier) string {
	name := string(tier)
	switch tier {
	case resolver.TierExplicit, resolver.TierRule:
		return r.Colors.OK(name)
	case resolver.TierLearned:
		return r.Colors.Accent(name)
	case resolver.TierSuggested:
		return r.Colors.Warn(name)
	default:
		return r.Colors.Dim(name)
	}
}

// actionStatus maps a ledger action to the gutter status that shows it:
// booked items are OK, skipped items are skipped, failures fail, items that
// were not sent warn and dry-run plans point forward.
func actionStatus(action string) render.Status {
	switch ledger.Action(action) {
	case ledger.ActionCreated, ledger.ActionUpdated, ledger.ActionRecovered:
		return render.StatusOK
	case ledger.ActionSkipped:
		return render.StatusSkip
	case ledger.ActionFailed:
		return render.StatusFail
	case ledger.ActionNotSent:
		return render.StatusWarn
	case ledger.ActionWouldCreate, ledger.ActionWouldUpdate, ledger.ActionWouldRecover:
		return render.StatusGo
	default:
		return render.StatusInfo
	}
}

// colorAction styles a ledger action name by its status. The name is always
// printed as text, so it stays readable without colour.
func colorAction(r render.Renderer, action string) string {
	switch actionStatus(action) {
	case render.StatusOK:
		return r.Colors.OK(action)
	case render.StatusFail:
		return r.Colors.Fail(action)
	case render.StatusWarn:
		return r.Colors.Warn(action)
	case render.StatusGo:
		return r.Colors.Accent(action)
	case render.StatusSkip:
		return r.Colors.Dim(action)
	default:
		return action
	}
}
