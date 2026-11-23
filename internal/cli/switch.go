package cli

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/render"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
)

// autoKeyword is the positional alternative to --clear.
const autoKeyword = "auto"

func newSwitchCommand() *cobra.Command {
	var clearPin bool
	cmd := &cobra.Command{
		Use:   "switch TICKET",
		Short: msgSwitchShort,
		Long:  msgSwitchLong,
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSwitch(cmd.Context(), defaultDailyDeps(cmd), args, clearPin)
		},
	}
	cmd.Flags().BoolVar(&clearPin, "clear", false, msgSwitchClearFlag)
	return cmd
}

// runSwitch appends a manual context event: a ticket pins the attribution, an
// empty ticket clears the pin.
func runSwitch(ctx context.Context, deps dailyDeps, args []string, clearFlag bool) error {
	ticket, clearPin, err := parseSwitchTarget(args, clearFlag)
	if err != nil {
		return err
	}
	if !clearPin {
		cfg, err := deps.LoadConfig()
		if err != nil {
			return err
		}
		pattern := cfg.TicketPattern.String()
		if !fullMatch(pattern, ticket) {
			return msgInvalidTicket(args[0], pattern)
		}
	}

	st, err := deps.OpenStore()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	e := event.Event{At: deps.Now(), Source: sessionizer.ManualSource, Kind: event.KindContext, Ticket: ticket}
	if err := st.Append(ctx, e); err != nil {
		return err
	}
	if clearPin {
		_, err = fmt.Fprintln(deps.Out, deps.Render.Gutter(render.StatusInfo, msgSwitchCleared))
		return err
	}
	_, err = fmt.Fprintln(deps.Out, deps.Render.Gutter(render.StatusOK, msgSwitched(SanitizeForTerminal(ticket))))
	return err
}

// parseSwitchTarget returns the normalised (upper-case) ticket to switch to,
// or clearing=true when the pin is to be removed.
func parseSwitchTarget(args []string, clearFlag bool) (ticket string, clearing bool, err error) {
	switch {
	case len(args) == 0 && !clearFlag:
		return "", false, errors.New(msgSwitchNeedsTicket)
	case len(args) == 1 && clearFlag:
		return "", false, errors.New(msgSwitchClearWithTicket)
	case clearFlag:
		return "", true, nil
	case strings.EqualFold(strings.TrimSpace(args[0]), autoKeyword):
		return "", true, nil
	}
	return strings.ToUpper(strings.TrimSpace(args[0])), false, nil
}

// fullMatch reports whether the whole text matches the pattern.
func fullMatch(pattern, text string) bool {
	re, err := regexp.Compile("^(?:" + pattern + ")$")
	return err == nil && re.MatchString(text)
}
