# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

First public release.

### Added

- Passive capture: a macOS presence agent (`ticktack agent`, run by launchd), a git reflog scanner and a shell hook that reports the working directory.
- Work blocks derived from presence and context, with a discrete confidence tier per block (`explicit`, `rule`, `learned`, `suggested`, `unknown`).
- Ticket attribution from branch names, configured repository rules, learned rules, manual `ticktack switch` pins and suggestions from YouTrack issues assigned to you.
- Daily commands: `status`, `today`, `switch`, `pause`, `resume`, `purge`.
- Guided setup with `ticktack init` (config, keychain token, shell hook, launchd agent) and a health check with `ticktack doctor`.
- `ticktack review` to confirm, reassign, discard, split or mark blocks as breaks; `ticktack explain` shows the signal and rule behind a block; `ticktack map` manages learned rules.
- `ticktack sync` books confirmed time idempotently, one YouTrack work item per ticket, day and type; `ticktack undo` reverts the last booking run.
- Optional integrations, all off by default: window-title hints, ActivityWatch import (`ticktack import activitywatch`, loopback only) and auto mode with a trust gate (`ticktack auto`, `ticktack digest`).
- Example configuration in `examples/config.toml`.
- Release packaging with GoReleaser and installation through a Homebrew tap or `go install`.

[Unreleased]: https://github.com/JtheGunner/ticktack/commits/main
