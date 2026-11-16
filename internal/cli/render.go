package cli

import (
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/JtheGunner/ticktack/internal/render"
	"github.com/JtheGunner/ticktack/internal/resolver"
)

// noColorFlag is the name of the persistent root flag that disables colour.
const noColorFlag = "no-color"

// rendererFor builds the renderer for w from the command's --no-color flag,
// the real environment and the real terminal.
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
