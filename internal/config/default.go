package config

import (
	"fmt"
	"strconv"
	"strings"
)

// DefaultTOML renders a configuration file with every default spelled out in
// a comment. When youTrackURL is non-empty it is written as an active
// setting; otherwise the key is left commented out.
func DefaultTOML(youTrackURL string) string {
	var b strings.Builder
	b.WriteString("# ticktack configuration. Uncomment a line to change a default.\n")
	b.WriteString("# The YouTrack token is not stored here; it lives in the OS keychain.\n\n")
	b.WriteString("# Base URL of your YouTrack instance.\n")
	if youTrackURL != "" {
		fmt.Fprintf(&b, "youtrack_url = %s\n\n", strconv.Quote(youTrackURL))
	} else {
		b.WriteString("# youtrack_url = \"https://example.youtrack.cloud\"\n\n")
	}
	b.WriteString("# Presence gaps longer than this end a work block.\n")
	fmt.Fprintf(&b, "# gap_threshold = %q\n\n", DefaultGapThreshold.String())
	b.WriteString("# A presence sample with at least this much idle time is ignored; time after the last input is never booked.\n")
	fmt.Fprintf(&b, "# idle_threshold = %q\n\n", DefaultIdleThreshold.String())
	b.WriteString("# Blocks shorter than this join a same-ticket neighbour or become one block that needs a ticket.\n")
	fmt.Fprintf(&b, "# min_block_duration = %q\n\n", DefaultMinBlockDuration.String())
	b.WriteString("# Booked durations are rounded to a multiple of this.\n")
	fmt.Fprintf(&b, "# rounding = %q\n\n", DefaultRounding.String())
	b.WriteString("# Maximum time booked per day.\n")
	fmt.Fprintf(&b, "# daily_cap = %q\n\n", DefaultDailyCap.String())
	b.WriteString("# Set to false to turn the daily cap off: no warning, and sync is never blocked by it.\n")
	b.WriteString("# daily_cap_enabled = true\n\n")
	b.WriteString("# Regular expression that extracts a ticket id from a branch name.\n")
	fmt.Fprintf(&b, "# ticket_pattern = %s\n\n", strconv.Quote(DefaultTicketPattern))
	b.WriteString("# Repositories scanned for git history by \"ticktack scan\". Each entry is the root of one\n")
	b.WriteString("# repository (absolute path or starting with ~/); there is no recursive search.\n")
	b.WriteString("# repo_paths = [\"~/Projects/my-repo\"]\n\n")
	b.WriteString("# Optional periodic git scan by the agent (off by default). \"0\" turns it off;\n")
	b.WriteString("# otherwise the minimum is 5m. The agent also scans once at start when this is set.\n")
	b.WriteString("# [scan]\n# interval = \"0\"\n\n")
	b.WriteString("# Time zone for day boundaries (default: system zone).\n# time_zone = \"Europe/Zurich\"\n\n")
	b.WriteString("# Optional window-title hints (off by default). Needs the macOS Accessibility\n")
	b.WriteString("# permission; titles are read in memory and never stored. Maps an IDE project\n")
	b.WriteString("# name to the absolute path of its repository.\n")
	b.WriteString("# [titlehint]\n# enabled = true\n#\n# [titlehint.projects]\n# \"my-ide-project\" = \"/abs/path/to/repo\"\n\n")
	b.WriteString("# Fixed repository to ticket mappings.\n# [[rules]]\n# repo = \"/path/to/repo\"\n# ticket = \"ABC-1\"\n\n")
	b.WriteString("# Optional import from a local ActivityWatch server (loopback only, off by default).\n")
	b.WriteString("# [activitywatch]\n# enabled = false\n")
	fmt.Fprintf(&b, "# url = %s\n", strconv.Quote(DefaultActivityWatchURL))
	b.WriteString("# hostname = \"my-mac\"\n\n")
	b.WriteString("# Optional auto mode (off by default). Run \"ticktack auto\" by hand or from your own\n")
	b.WriteString("# scheduler; it books only explicit and rule blocks, and only after this many\n")
	b.WriteString("# consecutive reviewed days without a correction.\n")
	b.WriteString("# [auto]\n# enabled = false\n")
	fmt.Fprintf(&b, "# trusted_days = %d\n", DefaultAutoTrustedDays)
	return b.String()
}
