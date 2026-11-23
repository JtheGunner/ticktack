# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- New setting `retention_days` (default `0` = off; maximum `3650`; negative values are rejected). Above zero, the agent deletes once a day the raw events of days older than that many days that are fully booked (every item confirmed and in the ledger with the same minutes; any doubt keeps the events). Overrides, ledger and sync history stay. A cleaned day is archived (migration `0010`, table `archived_days`) and frozen: it cannot be re-derived, so `review`, `today`, `explain`, `sync` and `auto` refuse it with a message instead of showing an empty day, and `sync` never reconciles its bookings away. Events for an archived day that arrive later (for example from a git re-scan) are ignored; the context the following day starts from is kept. Cleaning gives up re-deriving the day, see the README section Retention.
- `ticktack purge --expired` runs the cleanup by hand after the usual confirmation (`--yes` skips it); `--dry-run` prints the number of events and days without deleting. `ticktack purge` also empties the list of archived days.
- The work item text names the commits of the booked time. `ticktack sync` and `ticktack auto` read the subjects from the repository with the local `git` when they run (unique subjects in the order they were made, at most 5 and 200 characters each, then `+N more`; merge, `fixup!` and `squash!` commits are left out; control characters are removed). Without commits, or with an unreadable repository, the text stays the short summary. The `[ticktack:<key>]` marker stays at the end. Subjects are not stored on `events`; the booked text is kept in `sync_entries` for undo, as before. Turn it off with `description_from_commits = false` (default `true`).
- Migration `0009` adds `previous_text` to `sync_entries`, the text a work item had before an update or a deletion.
- `ticktack review` offers idle gaps (holes of at least `gap_threshold` between the first and the last presence of the day) as entries. Fill one with a ticket (`t`; `s` fills the part after a split time), mark it as a break (`b`) or discard it (`d`); time in a `personal` app is never offered. A filled gap is an `explicit` block with the reason `manuell gefüllt`, created only by that action; `ticktack auto` never fills gaps, and `ticktack explain` names the manual fill.
- Breaks are stored apart from discards, are never booked, and show as their own sum in `ticktack review` and `ticktack today`.
- `z` in `ticktack review` undoes the last action of the session (repeatable) and takes it out of the review log's correction count.
- Migration `0008` adds a `kind` column (`reassign`, `discard`, `break`, `fill`) to `overrides`; existing rows become `discard` (dropped time) or `reassign`.
- Optional `[app_classes]` table that maps the bundle ID of a foreground app to `work`, `neutral` or `personal` (empty by default; an unlisted app is `work`, so nothing changes). Time in a `neutral` app (chat, video call) keeps the ticket but is only `suggested`, with the reason `foreground app class neutral`, so it is confirmed in review and not booked by `ticktack auto`. Time in a `personal` app ends the presence span and is not counted; it does not show as unassigned. `ticktack explain` names the class and the app. A manual pin is not lowered. Only the bundle ID is used; no window title is stored.
- Optional alias `tt` for `ticktack`: `ticktack init` adds it to the managed shell hook block (`alias tt=ticktack`, fish: `alias tt ticktack`) unless `tt` already exists as a command on `PATH` or as an alias or function in the rc file, or `--skip-alias` is given. Rerunning `init` upgrades an older block in place; `ticktack uninstall` removes the alias with the block. `ticktack doctor` reports the state as an informational line that is not counted as a check. The release ships no `tt` binary or symlink.
- `ticktack uninstall` reverses `ticktack init`: after one confirmation (`--yes` skips it) it unloads and removes the launchd agent, removes the managed block from the shell startup files, deletes the YouTrack token from the OS keychain and deletes the log file and the title probe status file (`title-status.json`, with any temporary file of an interrupted write). Config file, database and binary stay.
- New setting `context_max_age` (default `"2h"`; `"0"` turns the age limit for repository and branch context off, a pin from an earlier day stays `suggested`).

### Changed

- The content hash in the ledger now decides: `ticktack sync` skips a booked item only when its minutes and its text are unchanged, and updates it when the text changed. `ticktack undo` restores the earlier text together with the earlier minutes, and books a deleted item again with the text it had; entries of earlier versions keep the old behaviour.
- Context no longer vouches for time without limit. Time under a repository and branch that were not confirmed for longer than `context_max_age` keeps its ticket but is only tier `suggested`, with the reason `context unconfirmed for <age>`: confirm it in `ticktack review`; `ticktack auto` does not book it. A shell prompt, checkout, commit or title hint on the same repository and branch confirms the context again. This also applies to the branch a day starts with from the day before.
- A pin (`ticktack switch`) is exempt from that limit on the day it was set. On the following days it still decides the ticket, but only as `suggested`, so a forgotten pin no longer books as `explicit` for days. This does not depend on `context_max_age` and also holds with `"0"`.
- `ticktack explain` names the age of the context and its last confirmation for such a block.
- The confirmation question of `ticktack purge` names everything it deletes.

### Fixed

- Work item text no longer takes commit subjects from commits of another ticket or from a branch without a ticket: a commit is used only when it was made during the item's time and its own ticket (from its branch) equals the item's ticket, so pinned, reassigned or folded time cannot leak subjects from an unrelated or private repository.
- Reading commit subjects, and every other `git` call, no longer lazily fetches missing objects from the promisor remote of a partial clone (`GIT_NO_LAZY_FETCH=1`, plus `GIT_ALLOW_PROTOCOL=none` and `GIT_TERMINAL_PROMPT=0`, so the guard does not depend on the git version); a missing commit is skipped, keeping network calls limited to YouTrack and ActivityWatch.
- Splitting an override block in `ticktack review` without a different ticket reported success and changed nothing; it is refused with a message.
- Ending the input (or pressing Ctrl-C) at the ticket question of a split no longer carries out the split; it is cancelled.
- Concurrent writers (agent, shell hook, CLI) no longer lose events or fail with `SQLITE_BUSY`. The database now uses WAL mode and waits up to 5 s for a lock, every transaction takes the write lock when it starts, and the check for applied migrations runs inside the migration transaction. The two events of a shell prompt and every scan or import pass are stored in one transaction, so a failure stores none of them instead of a part. The `-wal` and `-shm` side files are created with mode `0600`.
- The agent and the shell hook no longer fail with `database is locked` when they open a brand-new database at the same moment. `Open` retries for up to 5 s when SQLite reports a busy database while the first open sets it up.
- The marker search on an issue's work items and the tag lookup read every page of YouTrack's answer (`$top`/`$skip`) instead of only the first. Beyond the first page the search used to report "not found" and booked duplicates. Responses are read up to 8 MiB.
- The database and the agent log file are created with mode `0600` (an existing database is narrowed to `0600`); they used to follow the umask (`0644`) inside a `0700` directory.
- `ticktack version` of a `go install` build reports the module version or `dev-<revision>` instead of `dev`.
- The generated config now documents `repo_roots` and `[default_issue]`.
- `ticktack uninstall` now deletes everything the log writer owns: the log file, its backup (`.1`), the throttle state (`.throttle`) and leftover temporary files, not only `ticktack.log`.
- `ticktack uninstall` no longer treats every `launchctl print` error as "agent not loaded". Only launchctl's "service not found" result skips the unload; any other error is reported as a failure and the command exits non-zero (the plist is still removed).
- `ticktack purge` now empties every table of the local database. The cached YouTrack project list, the remembered standard tasks and the consent to create them used to survive, so auto mode could create a standard task without asking again.

- `ticktack sync` and `ticktack auto` no longer leave time booked on a ticket the day has no time for any more. A work item booked earlier is deleted when its block was reassigned, discarded or rounded to zero; `--dry-run` lists it as `dry-run-would-delete` and `ticktack undo` books it again. A booking whose item still exists but waits for a new confirmation is left as it is.
- `ticktack sync`, `ticktack auto` and `ticktack undo` no longer run at the same time, so a scheduled `auto` and a manual `sync` cannot create the same work item twice. They share a lock file; a run that finds it taken changes nothing, says that another booking run is active and exits with code `75`. The lock is released by the operating system when a run ends, also after a crash. `--dry-run` takes no lock.

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
