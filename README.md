<div align="center">

# ⏱️ ticktack

**Passive time tracking that ends in reviewed YouTrack work items.**
It watches when you are at the keyboard and which repository and branch you work on, then proposes the time sheet. You review it before anything is booked.

<code>🖥️ presence + 🌿 shell context (repo, branch)</code> &nbsp;→&nbsp; <code>🧮 work blocks</code> &nbsp;→&nbsp; <code>✅ review</code> &nbsp;→&nbsp; <code>📒 YouTrack</code>

![License](https://img.shields.io/badge/license-MIT-22c55e?style=flat-square)
![Go](https://img.shields.io/badge/go-1.27%2B-3776ab?style=flat-square&logo=go&logoColor=white)
![Platform](https://img.shields.io/badge/platform-macOS-0ea5e9?style=flat-square&logo=apple&logoColor=white)
![Privacy](https://img.shields.io/badge/telemetry-none-8b5cf6?style=flat-square)
![Status](https://img.shields.io/badge/status-pre--release-f59e0b?style=flat-square)

</div>

---

## 🎯 What it is

`ticktack` is a single-binary command line tool for people who book their time in [YouTrack](https://www.jetbrains.com/youtrack/) and forget to do it. It records small, privacy-preserving signals in the background and turns them into a daily proposal: "2h15 on ABC-12, 45m on ABC-31".

You stay in control. Every block is shown with the reason it was attributed to a ticket, you correct what is wrong, and only then does `ticktack sync` write the work items.

|    | Idea                    | What it means                                                                                  |
|:--:|-------------------------|------------------------------------------------------------------------------------------------|
| 🫥 | **Passive**             | No timers to start or stop. A launchd agent samples presence; a shell hook reports repo and branch. |
| 🔒 | **Private by design**   | No window titles, screenshots, keystrokes or file contents are stored. No telemetry.           |
| 🔍 | **Explainable**         | `ticktack explain` shows the signal and rule behind every block.                               |
| ♻️ | **Idempotent booking**  | One work item per ticket, day and type. Syncing twice updates instead of duplicating.          |
| ↩️ | **Reversible**          | `ticktack undo` reverts the last booking run.                                                  |

---

## 🧭 How it works

Two independent signals are combined. **Presence** says whether you were at the machine. **Context** says what you were working on (repository, branch, manual pin). Time to book is the intersection of both; context alone never produces a duration.

```text
  presence agent ──┐                                   ┌── overrides (your corrections)
  shell hook     ──┼─▶ events (append-only) ─▶ blocks ─┤
  manual pins    ──┘     presence ∩ context            └── rules ─▶ review ─▶ sync ─▶ YouTrack
                                                                              │
                                                                          ledger (idempotent)
```

Blocks are always re-derived from the stored events, your rules and your overrides. Corrections never rewrite events.

Each block carries a discrete confidence tier:

| Tier        | Meaning                                                                  |
|-------------|--------------------------------------------------------------------------|
| `explicit`  | The ticket is named outright: pinned with `ticktack switch`, or found in the branch name. |
| `rule`      | A repository rule from your config (`[[rules]]`).                        |
| `learned`   | A rule taught during review or managed with `ticktack map`.              |
| `suggested` | A proposal from your assigned, in-progress YouTrack issues. Needs your confirmation. |
| `unknown`   | No attribution. Never booked until you assign it.                        |

---

## 📦 Install

**Homebrew** (macOS):

```sh
brew install JtheGunner/tap/ticktack
```

**Go** (1.27 or newer):

```sh
go install github.com/JtheGunner/ticktack/cmd/ticktack@latest
```

Check the install with `ticktack version`.

`ticktack init` writes the resolved path of the running binary into the launchd agent definition. After moving or upgrading the binary, rerun `ticktack init` (it is safe to repeat) and run `ticktack doctor` to confirm the agent still points at it.

> [!NOTE]
> The capture agents (presence sampling, launchd) are macOS-only and rely on the system tools `ioreg` and `lsappinfo`. The review and sync commands are plain Go.

---

## 🚀 Quick start

```sh
# 1. Guided setup: config file, YouTrack token (keychain), shell hook, launchd agent
ticktack init --url https://your-company.youtrack.cloud

# 2. Verify everything; every problem names its fix
ticktack doctor

# 3. Work as usual. Look at what ticktack sees at any time
ticktack status
ticktack today

# 4. End of day: confirm, reassign, split or discard blocks
ticktack review

# 5. Book the confirmed items in YouTrack
ticktack sync

# Changed your mind?
ticktack undo
```

`ticktack init` explains each of its four steps and lets you skip any of them; rerunning it is safe. Create the token in your YouTrack profile as a permanent token. It is read with hidden input and stored in the OS keychain.

Inside `ticktack review` the prompt accepts: `c N` confirm, `t N TICKET` reassign, `u N K` take suggestion K, `r` remember the last reassignment as a rule, `d N` discard, `b N` mark as break, `s N HH:MM [TICKET]` split, `a` acknowledge the daily cap, `y` confirm all, `q` quit.

---

## ⚙️ Configuration

The config file is optional; every key has a default.

| Location       | Path                                                              |
|----------------|-------------------------------------------------------------------|
| 📄 Config      | `$XDG_CONFIG_HOME/ticktack/config.toml` (default `~/.config/ticktack/config.toml`) |
| 🗄️ Database    | `$XDG_DATA_HOME/ticktack/events.db` (default `~/.local/share/ticktack/events.db`)  |
| 📝 Log + pause flag | `$XDG_STATE_HOME/ticktack/` (default `~/.local/state/ticktack/`)  |

A complete, commented example lives in [`examples/config.toml`](examples/config.toml). Unknown keys are rejected so typos do not go unnoticed.

| Key                  | Default                | Meaning                                                          |
|----------------------|------------------------|------------------------------------------------------------------|
| `youtrack_url`       | none                   | Base URL of your YouTrack instance.                              |
| `gap_threshold`      | `"5m0s"`               | Presence gaps longer than this end a work block.                 |
| `min_block_duration` | `"10m0s"`              | Blocks shorter than this are dropped.                            |
| `rounding`           | `"15m0s"`              | Booked durations are rounded to a multiple of this.              |
| `daily_cap`          | `"10h0m0s"`            | Maximum time booked per day; above it, review must acknowledge. |
| `ticket_pattern`     | `[A-Z][A-Z0-9]+-\d+`   | Regular expression that extracts a ticket id from a branch name. |
| `repo_paths`         | none                   | Reserved, currently not used: accepted and validated, but nothing reads it yet. |
| `time_zone`          | system zone            | Time zone for day boundaries, for example `"Europe/Zurich"`.     |
| `[[rules]]`          | none                   | Fixed mappings with `repo` (path) and `ticket`. Repeatable.      |
| `[titlehint]`        | `enabled = false`      | Window-title hints; `[titlehint.projects]` maps IDE project name to repository path. |
| `[activitywatch]`    | `enabled = false`      | `url` (default `http://localhost:5600`, loopback only) and optional `hostname`. |
| `[auto]`             | `enabled = false`      | `trusted_days` (default `10`).                                   |

Durations use Go syntax such as `"90s"` or `"1h30m"`. The YouTrack token is never part of the config.

---

## 🛠️ Commands

|    | Command                         | What it does                                                                         |
|:--:|---------------------------------|--------------------------------------------------------------------------------------|
| ℹ️ | `ticktack version`              | Print the ticktack version.                                                          |
| 🧰 | `ticktack init`                 | Set up config, token, shell hook and launchd agent (`--yes`, `--url`, `--shell`, `--skip-config`, `--skip-token`, `--skip-hook`, `--skip-agent`, `--print-snippet SHELL`). |
| 🩺 | `ticktack doctor`               | Check the setup and name the fix for each problem.                                   |
| 🖥️ | `ticktack agent`                | Run the presence agent in the foreground (launchd runs it for you; `--interval`).    |
| 🐚 | `ticktack hook`                 | Internal: reports the working directory from the shell prompt. Hidden from help.     |
| 👀 | `ticktack status`               | Show what ticktack currently detects.                                                |
| 📋 | `ticktack today`                | List today's blocks and totals (`--day YYYY-MM-DD`).                                 |
| 📌 | `ticktack switch TICKET`        | Attribute time to a ticket from now on; `--clear` or `switch auto` returns to detection. |
| ⏸️ | `ticktack pause`                | Pause all capture.                                                                   |
| ▶️ | `ticktack resume`               | Resume capture after a pause.                                                        |
| 🗑️ | `ticktack purge`                | Delete all local data: events, overrides, ledger (`--yes` skips the prompt).         |
| ✅ | `ticktack review`               | Review the day's blocks and confirm what may be booked (`--day`).                    |
| 🔍 | `ticktack explain <N\|HH:MM>`   | Show the signal and rule behind a block (`--day`).                                   |
| 🗺️ | `ticktack map`                  | List and edit rules: `map list`, `map add --repo R [--branch B] TICKET`, `map edit ID`, `map rm ID`. |
| 📒 | `ticktack sync`                 | Book the confirmed time of a day in YouTrack (`--day`, `--dry-run`).                 |
| ↩️ | `ticktack undo`                 | Revert the last sync.                                                                |
| 🤖 | `ticktack auto`                 | Book the safe blocks of a day without review once trust is earned (`--day`, `--dry-run`). |
| 📰 | `ticktack digest`               | Show the digest of the last auto run.                                                |
| 🔌 | `ticktack import activitywatch` | Import window and AFK events from a local ActivityWatch server (`--since YYYY-MM-DD`). |

Run `ticktack <command> --help` for details.

---

## 🧩 Optional integrations

All of these are **off by default** and can be enabled independently.

| Integration         | How to enable                                                       | Notes                                                                 |
|---------------------|---------------------------------------------------------------------|-----------------------------------------------------------------------|
| 🐚 Shell hook       | Keep the hook step in `ticktack init` (zsh, bash, fish).            | The source of repository and branch context: at each prompt it records the repository root and branch. Without it only manual pins (`ticktack switch`), window-title hints or the ActivityWatch import provide context. Managed block in your rc file. |
| 🪟 Window-title hints | `[titlehint] enabled = true` plus a project-to-repository map.    | Needs the macOS Accessibility permission. Titles are parsed in memory for a project hint and never stored. |
| 🔌 ActivityWatch import | `[activitywatch] enabled = true`, then `ticktack import activitywatch`. | Talks only to a loopback server.                                |
| 🤖 Auto mode        | `[auto] enabled = true`, then `ticktack auto`.                      | Guarded by the trust gate below.                                      |

> [!IMPORTANT]
> **The trust gate.** Auto mode unlocks only after the last `trusted_days` (default 10) consecutive days were reviewed **without a single correction**. A later correction in `ticktack review` locks it again. Even when unlocked it books only `explicit` and `rule` blocks, skips any item that contains lower-tier time, and does not book a day above the daily cap. There is no background scheduler: run `ticktack auto` yourself or from your own cron or launchd job. Each run prints a digest (`ticktack digest` shows it again) and `ticktack undo` reverts it.

---

## 🔐 Privacy

- Never stored: window titles, screenshots, keystrokes, file contents. Titles may be parsed in memory for a project hint only.
- The only network call is the YouTrack sync. The optional ActivityWatch import talks to a loopback address only. There is no telemetry.
- The YouTrack token lives in the OS keychain, never in the config, environment files or logs.
- Nothing is booked without your review, unless you enabled auto mode and its trust gate is met.
- `ticktack pause` stops all capture; `ticktack purge` deletes all local data.

---

## 🧑‍💻 Development

```sh
make build    # CGO_ENABLED=0 go build -> bin/ticktack (VERSION=x.y.z sets the version)
make test     # CGO_ENABLED=0 go test ./...
make lint     # golangci-lint run ./...
```

| Path              | Role                                                             |
|-------------------|------------------------------------------------------------------|
| `cmd/ticktack/`   | Entrypoint.                                                      |
| `internal/cli/`   | Command tree and all user-facing strings.                        |
| `launchd/`        | Renders the launchd job definition of the presence agent.        |
| `internal/collector/` | Event sources: presence, window-title, ActivityWatch.   |
| `internal/resolver/`, `internal/sessionizer/` | Pure functions that attribute tickets and build blocks. |
| `internal/store/`, `internal/ledger/` | SQLite storage (pure Go, no CGO) and the booking ledger. |
| `internal/sink/`, `internal/youtrack/` | The booking target and its YouTrack client.      |
| `internal/config/`, `internal/paths/`, `internal/token/` | Config loading, file locations, keychain access. |

Tests are table-driven and the YouTrack client is tested against an `httptest` server; no test talks to a real YouTrack instance.

### Continuous integration

[`.github/workflows/ci.yml`](.github/workflows/ci.yml) runs on every pull request and on pushes to `main`. It needs no secrets and never talks to YouTrack; the `e2e` build-tag test is not run.

| Job name               | Check                                                  |
|------------------------|--------------------------------------------------------|
| `lint`                 | `golangci-lint` (version pinned in the workflow)       |
| `test (macos-latest)`  | `make test` (`CGO_ENABLED=0 go test ./...`) on macOS   |
| `test (ubuntu-latest)` | the same tests on Linux, to catch portability issues   |
| `build`                | `make build` on macOS                                  |
| `tidy`                 | `go mod tidy -diff` fails when `go.mod`/`go.sum` drift |

Register these five job names as required status checks when protecting `main`.

### Releasing

For the maintainer. Releases are built by [GoReleaser](https://goreleaser.com) from [`.goreleaser.yaml`](.goreleaser.yaml).

1. Move the `Unreleased` entries in `CHANGELOG.md` under a new version heading with the date and commit.
2. Tag the commit: `git tag vX.Y.Z && git push origin vX.Y.Z`.

Pushing the tag starts [`.github/workflows/release.yml`](.github/workflows/release.yml), which runs `goreleaser release --clean`. It needs the repository secret `HOMEBREW_TAP_GITHUB_TOKEN`: a token with write access to `JtheGunner/homebrew-tap`. The workflow's own `GITHUB_TOKEN` publishes the GitHub release.

To release from your machine instead, export a GitHub token as `GITHUB_TOKEN` and the tap token as `HOMEBREW_TAP_GITHUB_TOKEN`, then run `goreleaser release --clean`.

The release publishes darwin arm64 and amd64 archives with checksums and updates the Homebrew formula.

---

## 🤝 Contributing

Contributions are welcome.

- **Report an issue:** open one on [GitHub](https://github.com/JtheGunner/ticktack/issues) with the output of `ticktack version` and `ticktack doctor`. Remove anything private first.
- **Propose a change:** open an issue to discuss it, then a pull request from a branch.
- **Run locally:** `git clone https://github.com/JtheGunner/ticktack.git`, then `make test` and `make lint`. Run `gofmt`, `go vet`, `golangci-lint run` and `go test ./...` before opening a pull request; CI runs the same checks (see [Continuous integration](#continuous-integration)).
- **Conventions:** code, comments and commit messages in English; commits follow Conventional Commits (`feat:`, `fix:`, `docs:`, ...). Changes must keep the privacy guarantees above.

---

## 📝 Changelog

Release notes live in [`CHANGELOG.md`](CHANGELOG.md).

---

## 📄 License

Released under the **MIT License**. See [`LICENSE`](LICENSE).

<div align="center">
<sub>Track less. Book what is true.</sub>
</div>
