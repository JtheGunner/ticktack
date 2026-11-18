package review

import (
	"context"
	"strings"
	"time"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/resolver"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
	"github.com/JtheGunner/ticktack/internal/store"
)

// EventReader reads events together with the context carried into a range.
type EventReader interface {
	Events(ctx context.Context, from, to time.Time) ([]event.Event, error)
	CarriedContext(ctx context.Context, before time.Time, src store.CarrySources) ([]event.Event, error)
}

// DayInput is everything one derivation of a time range was made from, and
// its result.
type DayInput struct {
	// Events are the events of the range, led by the context carried into it
	// (see EventsWithCarriedContext).
	Events    []event.Event
	Overrides []event.Override
	Rules     resolver.Rules
	// Blocks is the derivation of Events, Overrides and Rules.
	Blocks []sessionizer.Block
}

// EventsWithCarriedContext returns the events in [from, to) preceded by the
// context that was in effect when the range began: the last manual pin and
// the last automatic (git, shell or scan) context event before from. It is
// the single place that decides how context crosses the start of a range, so
// status, today, explain, review, sync and auto see the same starting state.
//
// Presence and context stay separate signals. Only context events are carried;
// presence and gap events are limited to [from, to). The carried events keep
// their original timestamps and enter sessionizer.Derive as ordinary leading
// events, so they create no time of their own: a block attributed to carried
// context starts at the first presence inside the range, never before from.
//
// A manual event that cleared the pin is not carried: no pin is in effect
// then, and the automatic context applies. A window-title hint bound to an
// app is not carried either, because it only applies while that app is
// frontmost; the git or shell context beneath it is.
// Callers must therefore start the range at the day they report on: status
// reads the current day rather than a longer window, in which a hint of an
// earlier day would still apply once its app is frontmost again.
//
// Carried context has no age limit here: a branch or pin from any earlier
// day keeps its normal tier (a pin stays explicit). A separate change
// (TICKT-69) adds an age limit after which carried context is only tier
// suggested, which then also demotes a pin carried from an earlier day. That
// limit can be applied because the timestamps are passed through unchanged:
// the age of a carried event is the distance between its At and the presence
// it is attributed to. This loader must keep passing them through unchanged.
func EventsWithCarriedContext(ctx context.Context, r EventReader, from, to time.Time) ([]event.Event, error) {
	carried, err := r.CarriedContext(ctx, from, store.CarrySources{
		Manual:  sessionizer.ManualSource,
		AppHint: sessionizer.TitleSource,
	})
	if err != nil {
		return nil, err
	}
	events, err := r.Events(ctx, from, to)
	if err != nil {
		return nil, err
	}
	out := make([]event.Event, 0, len(carried)+len(events))
	for _, e := range carried {
		if e.Source == sessionizer.ManualSource && strings.TrimSpace(e.Ticket) == "" {
			continue
		}
		out = append(out, e)
	}
	return append(out, events...), nil
}

// LoadDayInput loads the events (with the carried context), overrides and
// rules for [from, to) and derives the blocks. It is the loader shared by
// LoadDay and the today and explain commands.
func LoadDayInput(ctx context.Context, r Reader, cfg config.Config, from, to time.Time) (DayInput, error) {
	events, err := EventsWithCarriedContext(ctx, r, from, to)
	if err != nil {
		return DayInput{}, err
	}
	overrides, err := r.Overrides(ctx, from, to)
	if err != nil {
		return DayInput{}, err
	}
	rules, err := LoadRules(ctx, r, cfg)
	if err != nil {
		return DayInput{}, err
	}
	blocks := sessionizer.Derive(events, overrides, sessionizer.ConfigFrom(cfg), rules)
	return DayInput{Events: events, Overrides: overrides, Rules: rules, Blocks: blocks}, nil
}
