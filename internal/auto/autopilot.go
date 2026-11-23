package auto

import (
	"cloud.google.com/go/civil"

	"github.com/JtheGunner/ticktack/internal/config"
)

// LookbackDays returns the configured number of completed days the autopilot
// looks back. A value outside the accepted range (an unset configuration)
// falls back to the default, so a missing value never widens the window.
func LookbackDays(cfg config.Auto) int {
	if cfg.AutopilotLookbackDays < config.MinAutopilotLookbackDays || cfg.AutopilotLookbackDays > config.MaxAutopilotLookbackDays {
		return config.DefaultAutopilotLookbackDays
	}
	return cfg.AutopilotLookbackDays
}

// AutopilotDays returns the days the autopilot considers: the lookback days
// before today, oldest first. Today is never among them, because an open day
// is not finished yet.
func AutopilotDays(today civil.Date, lookback int) []civil.Date {
	days := make([]civil.Date, 0, lookback)
	for back := lookback; back >= 1; back-- {
		days = append(days, today.AddDays(-back))
	}
	return days
}

// Completed reports whether day lies before today and so is finished.
func Completed(day, today civil.Date) bool {
	return day.Before(today)
}
