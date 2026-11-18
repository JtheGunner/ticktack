# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- `ticktack sync` and `ticktack auto` no longer leave time booked on a ticket the day has no time for any more. A work item booked earlier is deleted when its block was reassigned, discarded or rounded to zero; `--dry-run` lists it as `dry-run-would-delete` and `ticktack undo` books it again. A booking whose item still exists but waits for a new confirmation is left as it is.

## [0.3.5] - 2026-11-17

### Added

- `ticktack review` has a help screen: type `?` (or `help`) for every action with its placeholders, a description and an example, one command per line.

### Changed

- The action overview in `ticktack review` is now a compact key bar (key and one word per action) that points to `?`. Placeholders in usage messages read `<block>`, `<ticket>`, `<time>` and `<n>` instead of `N` and `K`.

## [0.3.4] - 2026-11-17

### Changed

- `ticktack review` shows its actions as a grouped overview (Assign, Confirm, Adjust, Other) with highlighted keys instead of one long line. It shows the same overview after an unknown command, and falls back to plain text without colour support.

## [0.3.3] - 2026-11-17

### Changed

- The standard task that ticktack creates for time without a ticket now carries the `manual` tag. The tag is created when it does not exist yet; standard tasks that already exist are left untouched.

## [0.3.2] - 2026-11-17

### Added

- `daily_cap_enabled = false` turns the daily cap off: no warning in `ticktack review`, and neither `ticktack sync` nor `ticktack auto` is blocked by it.

### Changed

- `ticktack review` prints the feedback of an action below the re-rendered table, next to the prompt, so there is no need to scroll up.
- Short blocks that cannot join a same-ticket neighbour now merge with their touching neighbours of the same kind into one block that needs a ticket, instead of a long list of one- and two-minute rows.

## [0.3.1] - 2026-11-16

### Fixed

- `ticktack sync --dry-run` no longer claims "No time to book" when the time waits for a standard task that does not exist yet. It names each project and the time that would be booked once the task is created.

## [0.3.0] - 2026-11-16

### Added

- `repo_roots`: scan every repository below a folder such as `~/Projects` without listing each one.
- Standard tasks: time without a ticket is booked on a catch-all issue of the project whose name matches the repository folder, or on the global `[default_issue]`. `ticktack sync` creates a missing task after one confirmation; `ticktack auto` creates it only for projects confirmed before.
- `ticktack doctor` checks `repo_roots` and reports the age of the cached project list.

## [0.2.0] - 2026-11-16

### Added

- Styled command output: headings, aligned tables and status lines with colours and glyphs, adapted to the terminal width.
- `--no-color` flag on every command. `NO_COLOR` and `TERM=dumb` also turn colours off, and output that is not a terminal never contains ANSI codes.
- ASCII fallback for glyphs and frames when the locale is not UTF-8 or `TERM=dumb`.
- Output is measured in terminal cells, so wide characters (CJK, emoji) in paths, branches and reasons no longer break the alignment of tables, boxes and wrapped text. Measurement does not depend on the locale.
- The summary boxes of `doctor` and `sync` have a green or red border that matches their result.

### Changed

- The layout of the interactive commands' output: `init`, `doctor`, `status`, `today`, `review`, `explain`, `map`, `scan`, `sync`, `auto`, `digest`, `import`, `undo`, `purge`, `pause`, `resume` and `switch`. Scripts that parse this text must be updated; `agent`, `hook` and `version` are unchanged.
- `ticktack review` moves the tier and state of a block below its row when the terminal is too narrow for all columns, instead of cutting them.
- `ticktack digest` prints the stored digest text, which is plain ASCII without colour by design so the stored text is stable.

## [0.1.1] - 2026-11-16

### Added

- Advisory docs-drift check on pull requests: annotations for README sections whose code changed (`.github/workflows/docs-drift.yml`, `.docdrift.yaml`). It never fails the build.
- `ticktack doctor` reports a read-only database file with a fix hint.

### Changed

- Ticket keys are matched by `ticket_pattern` exactly as written, case-sensitively, in `ticktack scan`, the shell hook, review and the resolver. With the default pattern `fix/utf-8-handling` or `feat/abc-12` no longer yield a ticket. Already stored events are not rewritten.
- The shell hook honours `ticket_pattern` (it read only the default before); the pattern is loaded from the config file at each prompt, which costs microseconds.
- Context precedence is now pin, then window-title hint, then git/shell context. A hint applies only while its app is the frontmost app; the title collector records the app's bundle ID on its events for this.
- A day counts towards the auto-mode trust gate only if its review session took place on that calendar day or the next one (configured time zone). Catch-up reviews with `ticktack review --day` stay valid for booking but do not count.
- `ticktack map` help and the README now state that learned rules match the branch exactly and the repository by path prefix; there are no globs.
- `y` (confirm all) in `ticktack review` is all or nothing: if storing fails, no item is confirmed instead of only the first few.
- README "Releasing" section updated to match the release workflow.

### Fixed

- A review action and its review log entry are now stored in one transaction, so an interrupted session can no longer lose the correction record.
- Splitting unassigned time without a ticket is rejected with a clear message instead of creating a still-unassigned entry that counted as a correction.
- `ticktack doctor` judges agent freshness by the interval the installed launchd agent runs with, not the 30 s default.

## [0.1.0] - 2026-11-16

First public release.

### Added

- Passive capture: a macOS presence agent (`ticktack agent`, run by launchd), and a shell hook that reports the repository and branch at each prompt.
- Work blocks derived from presence and context, with a discrete confidence tier per block (`explicit`, `rule`, `learned`, `suggested`, `unknown`).
- Ticket attribution from branch names, configured repository rules, learned rules, manual `ticktack switch` pins and suggestions from YouTrack issues assigned to you.
- Daily commands: `status`, `today`, `switch`, `pause`, `resume`, `purge`.
- Guided setup with `ticktack init` (config, keychain token, shell hook, launchd agent) and a health check with `ticktack doctor`.
- `ticktack review` to confirm, reassign, discard, split or mark blocks as breaks; `ticktack explain` shows the signal and rule behind a block; `ticktack map` manages learned rules.
- `ticktack sync` books confirmed time idempotently, one YouTrack work item per ticket, day and type; `ticktack undo` reverts the last booking run.
- Optional integrations, all off by default: window-title hints, ActivityWatch import (`ticktack import activitywatch`, loopback only) and auto mode with a trust gate (`ticktack auto`, `ticktack digest`).
- `ticktack scan [--since YYYY-MM-DD]` reads the git reflog of the repositories in `repo_paths` (absolute or `~/` paths, one repository root per entry) and stores repo, branch, ticket hint and timestamp as context events, so work without a shell prompt such as IDE git is captured. Scanning is idempotent and honours the pause flag.
- Optional periodic scan by the agent: `[scan] interval` (off by default, minimum `5m`). `ticktack doctor` gained a "git repositories" check that reports each missing, unreadable or non-repository path.
- Example configuration in `examples/config.toml`.
- Release packaging with GoReleaser and installation through a Homebrew tap or `go install`.
- GitHub Actions: a CI workflow (lint, tests on macOS and Linux, build, `go mod tidy` check) and a tag-triggered release workflow that runs GoReleaser.

### Fixed

- The agent now waits for its window-title and scan goroutines before closing the store on shutdown.
- `ticktack today` and `ticktack init` strip terminal control characters from ticket keys, paths and external error text before printing them.
- The git scan (`ticktack scan` and the agent scan) now honours the configured `ticket_pattern` instead of always using the default.
- The ActivityWatch URL check rejects IPv4-mapped IPv6 (`[::ffff:127.0.0.1]`) and zoned loopback addresses; numerically encoded and `localhost`-lookalike hosts stay rejected.

[Unreleased]: https://github.com/JtheGunner/ticktack/compare/v0.3.5...HEAD
[0.3.5]: https://github.com/JtheGunner/ticktack/compare/v0.3.4...v0.3.5
[0.3.4]: https://github.com/JtheGunner/ticktack/compare/v0.3.3...v0.3.4
[0.3.3]: https://github.com/JtheGunner/ticktack/compare/v0.3.2...v0.3.3
[0.3.2]: https://github.com/JtheGunner/ticktack/compare/v0.3.1...v0.3.2
[0.3.1]: https://github.com/JtheGunner/ticktack/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/JtheGunner/ticktack/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/JtheGunner/ticktack/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/JtheGunner/ticktack/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/JtheGunner/ticktack/releases/tag/v0.1.0
