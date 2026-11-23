<div align="center">

# ⏱️ ticktack

**Passive time tracking that ends in reviewed YouTrack work items.**
It watches when you are at the keyboard and which repository and branch you work on, then proposes the time sheet. You review it before anything is booked.

<code>🖥️ presence + 🌿 shell context (repo, branch)</code> &nbsp;→&nbsp; <code>🧮 work blocks</code> &nbsp;→&nbsp; <code>✅ review</code> &nbsp;→&nbsp; <code>📒 YouTrack</code>

![License](https://img.shields.io/badge/license-MIT-22c55e?style=flat-square)
![Go](https://img.shields.io/badge/go-1.27.3%2B-3776ab?style=flat-square&logo=go&logoColor=white)
![Platform](https://img.shields.io/badge/platform-macOS-0ea5e9?style=flat-square&logo=apple&logoColor=white)
![Privacy](https://img.shields.io/badge/telemetry-none-8b5cf6?style=flat-square)
[![Release](https://img.shields.io/github/v/release/JtheGunner/ticktack?style=flat-square&color=22c55e&label=release)](https://github.com/JtheGunner/ticktack/releases)

</div>

---

## 🎯 What it is

`ticktack` is a single-binary command line tool for people who book their time in [YouTrack](https://www.jetbrains.com/youtrack/) and forget to do it. It records small, privacy-preserving signals in the background and turns them into a daily proposal: "2h15 on ABC-12, 45m on ABC-31".

You stay in control. Every block is shown with the reason it was attributed to a ticket, you correct what is wrong, and only then does `ticktack sync` write the work items.

|    | Idea                    | What it means                                                                                  |
|:--:|-------------------------|------------------------------------------------------------------------------------------------|
| 🫥 | **Passive**             | No timers to start or stop. A launchd agent samples presence; a shell hook reports repo and branch; `ticktack scan` fills gaps from the git reflog. |
| 🔒 | **Private by design**   | No window titles, screenshots, keystrokes or file contents are stored. No telemetry.           |
| 🔍 | **Explainable**         | `ticktack explain` shows the signal and rule behind every block.                               |
| ♻️ | **Idempotent booking**  | One work item per ticket, day and type. Syncing twice updates instead of duplicating.          |
| ↩️ | **Reversible**          | `ticktack undo` reverts the last booking run.                                                  |

---

## 🧭 How it works

Two independent signals are combined. **Presence** says whether you were at the machine. **Context** says what you were working on (repository, branch, manual pin). Time to book is the intersection of both; context alone never produces a duration.

```text
  presence agent ──┐                                   ┌── overrides (your corrections)
  shell hook     ──┤
  git reflog scan──┼─▶ events (append-only) ─▶ blocks ─┤
  manual pins    ──┘     presence ∩ context            └── rules ─▶ review ─▶ sync ─▶ YouTrack
                                                                              │
                                                                          ledger (idempotent)
```

Blocks are always re-derived from the stored events, your rules and your overrides. Corrections never rewrite events.

When several context signals disagree, the strongest one wins:

1. **Pin** — `ticktack switch <TICKET>` holds until you clear it (`--clear` or `switch auto`).
2. **Window-title hint** — the optional title collector's ticket or project, but only while the hinted app is the frontmost app. While another app is in front, or after you leave it, the hint is ignored.
3. **Git and shell context** — the branch, repository rules and learned rules.

A ticket key in a branch name is matched by `ticket_pattern` exactly as written, so the default `[A-Z][A-Z0-9]+-\d+` reads `feat/ABC-12-x` as `ABC-12` but `fix/utf-8-handling` or `feat/abc-12` as no ticket. Scan, the shell hook, review and `switch` all use that one pattern. Tickets you type (`ticktack switch`, reassigning in review) are upper-cased before they are checked against the pattern, so a custom pattern with lower-case letters cannot be entered that way.

Each block carries a discrete confidence tier:

| Tier        | Meaning                                                                  |
|-------------|--------------------------------------------------------------------------|
| `explicit`  | The ticket is named outright: pinned with `ticktack switch`, or found in the branch name. |
| `rule`      | A repository rule from your config (`[[rules]]`).                        |
| `learned`   | A rule taught during review or managed with `ticktack map`.              |
| `suggested` | A proposal that needs your confirmation: context that was not confirmed for longer than `context_max_age`, or one of your assigned, in-progress YouTrack issues. |
| `unknown`   | No attribution. Never booked until you assign it.                        |

Context loses certainty with age. A branch seen at 08:00 does not vouch for the whole day: once it has gone unconfirmed for `context_max_age` (default two hours), the time keeps its ticket but drops to `suggested` with the reason `context unconfirmed for <age>`, so you check it in review and auto mode does not book it. Any context event on the same repository and branch confirms it again: a shell prompt, a checkout, a commit, a title hint. A pin is exempt on the day you set it; on the following days it still applies, but only as `suggested`. That holds whatever `context_max_age` is: `"0"` turns off the age limit for repository and branch context, not the day rule of a pin. `ticktack explain` names the age and the last confirmation.

---

## 📦 Install

**Homebrew** (macOS):

```sh
brew install JtheGunner/tap/ticktack
```

**Go** (1.27.3 or newer):

```sh
go install github.com/JtheGunner/ticktack/cmd/ticktack@latest
```

Check the install with `ticktack version`. A `go install` build reports the module version, or `dev-<revision>` when built from a checkout.

`ticktack init` writes two things into the launchd agent definition:

- **The path of the binary, in a form that survives upgrades.** A Homebrew install is referenced through its `opt` link (`<prefix>/opt/ticktack/bin/ticktack`), not the versioned Cellar directory. Any other install is referenced through the path it was started from, without following symlinks.
- **The directories in use at that moment** (`XDG_CONFIG_HOME`, `XDG_DATA_HOME`, `XDG_STATE_HOME`, or their defaults). launchd does not inherit your shell environment, so this keeps the agent on the same config file, database and pause flag as the CLI.

Rerun `ticktack init` (it is safe to repeat) after moving the binary or changing one of those variables. `ticktack doctor` checks that the program named in the agent definition exists and is the binary you are running, and that the agent stored a presence sample recently. It does not compare the directories in the agent definition with your current environment.

> [!NOTE]
> The capture agents (presence sampling, launchd) are macOS-only and rely on the system tools `ioreg` and `lsappinfo`. The review and sync commands are plain Go.

### Uninstall

`ticktack uninstall` reverses `ticktack init`. It lists what it is about to remove, asks once (`--yes` skips the question) and then:

| | Removed | Where |
|:--|:--|:--|
| 🖥️ | launchd agent, unloaded first | `~/Library/LaunchAgents/dev.ticktack.agent.plist` |
| 🐚 | Managed shell hook block; the rest of the file stays | `$ZDOTDIR/.zshrc` or `~/.zshrc`, `~/.bashrc`, `~/.bash_profile`, `~/.bash_login`, `~/.profile`, `~/.config/fish/config.fish` |
| 🔑 | YouTrack token | OS keychain |
| 📜 | Log file with its backup (`.1`), throttle state (`.throttle`) and temporary files | `~/.local/state/ticktack/ticktack.log` (or under `XDG_STATE_HOME`) |
| 🔎 | Title probe status file (`title-status.json`) and temporary files of an interrupted write | `~/.local/state/ticktack/` (or under `XDG_STATE_HOME`) |

The config file, the database and the binary stay. A step that fails is reported, the other steps still run, and the command exits non-zero; running it again is safe. If launchd cannot tell whether the agent is loaded, that counts as a failure too (the plist is still removed, so the agent does not start at the next login). Shells that are already open keep the hook until you restart them.

To leave nothing behind:

```sh
ticktack uninstall   # agent, shell hook, token, log
ticktack purge       # every row of the local database
```

Then remove the binary (`brew uninstall ticktack`, or delete it from your Go bin directory) and, if you like, the directories `~/.config/ticktack`, `~/.local/share/ticktack` and `~/.local/state/ticktack`. Nothing in YouTrack is touched: work items that were booked stay booked.

---

## ⌨️ The `tt` alias

The binary is called `ticktack`. `tt` is an optional shell alias for it, not a second binary: the release archive and the Homebrew formula install no `tt` file, so they cannot collide with another package or with a tool of yours.

`ticktack init` adds `alias tt=ticktack` (fish: `alias tt ticktack`) to the same managed block as the shell hook. Skip it with `--skip-alias`. Running `init` again keeps the block in sync: an older block without the alias gains it, `--skip-alias` takes it out, and the file never changes when nothing else did.

`init` does not set the alias when `tt` already exists, and says so. It looks for:

- a command named `tt` on your `PATH`;
- an `alias tt` or a function `tt` in the shell startup file `init` edits, outside the managed block.

An alias or function defined anywhere else cannot be seen, because `init` reads that one file and never runs your shell: a plugin, a file your rc sources, or something set only in the current session. If that applies, your own definition and the managed one both exist and the one loaded last wins; remove the alias line from the block or run `init --skip-alias`.

`ticktack doctor` lists the state as an informational line (`alias tt`): set, not set, or skipped because `tt` already exists. It is never counted as a check and never changes the exit status. `ticktack uninstall` removes the alias with the rest of the block.

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

`ticktack sync` keeps YouTrack in step with the day. The commit subjects in a work item come only from commits whose own branch carries the item's ticket; time moved to a ticket by a pin or a reassignment does not pull in commits from other branches or repositories. A work item booked earlier is deleted when its block was later reassigned, discarded or rounded to zero; `--dry-run` shows the deletion first and `ticktack undo` books the item again with the text it had. A booked item is left alone only when both its minutes and its text are unchanged; a new commit that changes the text updates the item, and `ticktack undo` restores the earlier minutes together with the earlier text. A booking whose item still exists but waits for a new confirmation is left untouched. A booked work item that was deleted in YouTrack is booked again by the next sync and reported as `repaired`.

> [!NOTE]
> Only one booking run is active at a time. `ticktack sync`, `ticktack auto` and `ticktack undo` share a lock file (`booking.lock` next to the database), so a scheduled `auto` and a manual `sync` cannot book the same work item twice. A run that finds the lock taken does not wait: it changes nothing, prints `another ticktack booking run (sync, auto or undo) is active` and exits with code `75`. Run it again once the other one has finished. The operating system releases the lock when a run ends, also after an abort or a crash, so there is never a stale lock to remove. `--dry-run` takes no lock.

> [!NOTE]
> Ctrl-C (SIGINT) or SIGTERM stops `sync`, `auto` and `undo` in an orderly way: the work item currently being sent is finished, including its ledger entry, and no further item is started. The items not started are reported as `not-sent`, and the command exits with code `130`; running it again continues. If a real failure happened as well (for example a rejected token during `undo`), it is printed next to the interruption notice and the exit code is `1`, so `130` always means a clean interruption; `75` still takes precedence. A second signal while it finishes ends the process at once. `ticktack init` also stops on the first signal, including at the hidden token prompt, and exits with `130` without storing anything that was being typed. Work on one item is bounded to two minutes, whether or not a signal arrived.

`ticktack init` explains each of its four steps and lets you skip any of them; rerunning it is safe. Create the token in your YouTrack profile as a permanent token. It is read with hidden input and stored in the OS keychain.

Inside `ticktack review` the prompt accepts:

| | Action | Effect |
|:--|:--|:--|
| ✅ | `c N` | Confirm block N. Idle gaps and breaks cannot be confirmed. |
| ✅ | `y` | Confirm all blocks (all or nothing: if storing fails, none of them is confirmed). |
| 🎫 | `t N TICKET` | Assign block N to a ticket. On an idle gap this fills the gap. |
| 💡 | `u N K` | Take suggestion K for block N. |
| 🧠 | `r` | Remember the last reassignment as a rule. |
| ✂️ | `s N HH:MM [TICKET]` | Split block N. Without a ticket the second part keeps the ticket; on an override block (reassigned or filled) that does nothing, so it is refused. Without an answer to the ticket question (end of input, Ctrl-C) nothing is split. |
| ☕ | `b N` | Mark block N or an idle gap as a break: not booked, shown as its own sum. |
| 🗑️ | `d N` | Discard block N, or dismiss an idle gap. |
| ↩️ | `z` | Undo the last action of this session. Repeat to go further back. |
| ⚠️ | `a` | Acknowledge the daily cap. |
| 🚪 | `q` | Quit. |

**Idle gaps.** A hole of at least `gap_threshold` between two stretches of presence on the same day (a meeting without keyboard, say) shows up as an `idle gap` entry. Time before the first and after the last presence of the day is never offered, and neither is time in a `personal` app (see `[app_classes]`): it is not counted and a fill never covers it. You can fill the gap with a ticket (`t`; `s` fills only the part after the split time), mark it as a break (`b`) or discard it (`d`). A filled gap becomes an `explicit` block with the reason `manuell gefüllt`; it is the only way time without presence enters a day, and only through your action in review, so no duration is derived from context and `ticktack auto` never fills a gap. A gap that was filled, marked or discarded is not offered again. `ticktack explain` names a manual fill.

**Breaks.** A break is stored separately from a discard, is never booked and is shown as its own sum (`Breaks: …`) in `review` and `today`, apart from the booked totals.

**Undo.** `z` removes the override your session created last, together with what it added to the review log; with nothing left to undo it says so and changes nothing. Overrides of earlier sessions are not touched, and a learned rule written with `r` stays (remove it with `ticktack map rm`).

---

## ⚙️ Configuration

The config file is optional; every key has a default.

| Location       | Path                                                              |
|----------------|-------------------------------------------------------------------|
| 📄 Config      | `$XDG_CONFIG_HOME/ticktack/config.toml` (default `~/.config/ticktack/config.toml`) |
| 🗄️ Database    | `$XDG_DATA_HOME/ticktack/events.db` (default `~/.local/share/ticktack/events.db`)  |
| 📝 Log + pause state | `$XDG_STATE_HOME/ticktack/` (default `~/.local/state/ticktack/`)  |

The log file `ticktack.log` stays bounded: above 1 MiB it is rotated into a single `ticktack.log.1`, and an identical message is written at most once every 10 minutes.

A complete, commented example lives in [`examples/config.toml`](examples/config.toml). Unknown keys are rejected so typos do not go unnoticed.

| Key                  | Default                | Meaning                                                          |
|----------------------|------------------------|------------------------------------------------------------------|
| `youtrack_url`       | none                   | Base URL of your YouTrack instance. Must use `https`; plain `http` is accepted only for `localhost`, `127.0.0.0/8` and `::1`. |
| `gap_threshold`      | `"5m0s"`               | Presence gaps longer than this end a work block.                 |
| `idle_threshold`     | `"3m0s"`               | A presence sample with at least this much idle time is ignored; time after the last input is never booked. |
| `min_block_duration` | `"10m0s"`              | Shorter blocks join a same-ticket neighbour, fold into the ticket they sit between (only when the short block itself is `explicit` or `rule`; the result never has a higher tier than either side), or become one block that needs a ticket. |
| `context_max_age`    | `"2h0m0s"`             | Context not confirmed for this long is only `suggested`. `"0"` turns this limit off. A pin does not depend on it: it is `suggested` from the day after it was set, also with `"0"`. |
| `rounding`           | `"15m0s"`              | Booked durations are rounded to a multiple of this. Must be whole minutes; `"1m"` books exact minutes. |
| `daily_cap`          | `"10h0m0s"`            | Maximum time booked per day; above it, review must acknowledge. |
| `daily_cap_enabled`  | `true`                 | `false` turns the daily cap off: no warning, and neither `sync` nor `auto` is blocked by it. |
| `description_from_commits` | `true`           | The work item text names the commits of the booked time: `ticktack sync` reads their subjects from the repository with the local `git` when it runs, with every git transport refused, so a partial clone never fetches from its remote on any git version (a commit that is not in the local object store is skipped). A commit counts for an item only when it was made during that ticket's time and its own branch names that same ticket; commits on a branch without a ticket, or on another ticket's branch, are never used, even when the time was pinned, reassigned or folded into the item, and even when a rule maps that repository to the ticket (unique subjects in the order they were made, at most 5 and 200 characters each, then `+N more`, or `+N or more` when an item has over 100 commits, of which only the first 100 are read; merge commits and `fixup!`/`squash!` commits are left out). Without commits the text stays `ABC-1 — 3 blocks`. When a commit cannot be read, an item currently booked with commit subjects keeps its last booked text until the next sync reads every commit; an item that never had commit subjects gets the summary. `false` always keeps that summary. The `[ticktack:<key>]` marker stays at the end of the text. |
| `ticket_pattern`     | `[A-Z][A-Z0-9]+-\d+`   | Regular expression that extracts a ticket id from a branch name. |
| `repo_paths`         | none                   | Repositories read by `ticktack scan` and the agent's periodic scan. Each entry is the root of one repository, as an absolute path or starting with `~/`; subdirectories are not searched. |
| `repo_roots`         | none                   | Folders whose direct subfolders are scanned like `repo_paths` entries, for example `["~/Projects", "~/StudioProjects"]`. A subfolder counts when it contains `.git`. |
| `time_zone`          | system zone            | Time zone for day boundaries, for example `"Europe/Zurich"`.     |
| `retention_days`     | `0` (off)              | Delete the raw events of a day this many days after it, once the day is fully booked. `0` keeps all events; negative values and values above `3650` are rejected. Read [Retention](#retention) before you turn it on. |
| `[[rules]]`          | none                   | Fixed mappings with `repo` (absolute path or starting with `~/`) and `ticket` (must match `ticket_pattern`). Repeatable. |
| `[titlehint]`        | `enabled = false`      | Window-title hints; `[titlehint.projects]` maps IDE project name to repository path. |
| `[app_classes]`      | empty                  | Maps a macOS bundle ID of the foreground app to a class, for example `"us.zoom.xos" = "neutral"`. `work` (the default for an unlisted app) keeps the ticket; `neutral` keeps it but only as `suggested`, with the reason `foreground app class neutral`; `personal` ends the time, which is not counted and does not show as unassigned. ticktack ships no app list. Only the bundle ID is used, never a window title. Bundle IDs are matched exactly and case-sensitively, as macOS reports them (surrounding spaces are ignored); listing one app twice, also with different case, is an error. |
| `[activitywatch]`    | `enabled = false`      | `url` (default `http://localhost:5600`, loopback only) and optional `hostname`. |
| `[scan]`             | `interval = "15m"`     | Periodic git scan by the agent, once `repo_paths` or `repo_roots` is set. `"0"` is off; otherwise at least `"5m"`. |
| `[default_issue]`    | none                   | `project` (YouTrack project key for time without a ticket, for example `"GENERAL"`) and `summary` (required, for example `"Meetings / Admin"`). Turns on standard tasks (see below). |
| `[auto]`             | `enabled = false`      | `trusted_days` (default `10`).                                   |

Durations use Go syntax such as `"90s"` or `"1h30m"`. A duration of zero is rejected, except for `scan.interval` and `context_max_age`, where `"0"` means off; to turn the daily cap off, set `daily_cap_enabled = false`. Top-level keys such as `time_zone` must come before the first table (`[scan]`, `[[rules]]`, ...). Repository paths in `[[rules]]` and `[titlehint.projects]` are normalised when the config is loaded: a trailing slash is dropped and symlinks are resolved. The YouTrack token is never part of the config.

### Retention

By default ticktack keeps every raw event. With `retention_days = N` (N from 1 to 3650) the agent deletes, once a day, the raw events of a day that is older than N days **and fully booked**: re-deriving the day gives items that are all confirmed in review and booked in the ledger with the same minutes, and nothing is left unconfirmed, unassigned or stale. Any doubt keeps the events. A day older than N days that is not fully booked keeps its events until it is.

> [!WARNING]
> A cleaned day can no longer be re-derived. It is **archived and frozen**: `review`, `today`, `explain`, `sync` and `auto` refuse it with a message, because a day without events would otherwise look like a day without time and `sync` would delete the work items it booked. Overrides, the ledger, the sync history and the bookings in YouTrack stay untouched, and `undo` still reverts the last sync from the sync history. Events that arrive later for an archived day (for example from a re-scan of the git reflog) are ignored.

- The newest repository, branch and pin context before the end of a cleaned day stays, because the following day starts from it. It is not counted as deleted.
- Values from 1 to 6 are allowed but leave little time to review and correct a week. Keep `retention_days` well above the time you need to review a day, because a day that is not fully booked in time simply keeps its events until it is.
- **Retention and auto mode.** An archived day cannot be re-derived, so the trust gate judges it from what stays: its confirmations and its ledger rows. It counts towards the streak only when it has confirmations, every one was made on the day or the next, and they match the ledger rows exactly (same issue, type and minutes, nothing on either side alone). A day cleaned by retention was fully confirmed and booked, so this is the same bar as for a live day and a small `retention_days` cannot keep auto mode locked. An archived day that fails this check is skipped: it never counts and never ends the streak. A correction (reassign, discard, break, split, rule change) ends the streak at its day as before.
- A day is kept when the cleanup cannot be sure: a day above the daily cap is cleaned only after the cap was acknowledged for exactly that total, and day boundaries are calendar days in `time_zone`, so the 23 and 25 hour days of a daylight-saving change are never cleaned early.
- `ticktack purge --expired --dry-run` prints how many events and days the cleanup would delete and changes nothing. `ticktack purge --expired` runs it by hand after the same confirmation as `purge` (`--yes` skips it) and states plainly that the days cannot be re-derived afterwards.
- The agent reads the configuration anew before every run, so a changed `retention_days` takes effect at the next run (at most 24 hours later) without a restart. It cleans at start and then every 24 hours, does nothing while `retention_days` is 0 or the config cannot be loaded, skips the run while a sync or auto run holds the booking lock, and logs failures without stopping. `ticktack doctor` shows one `retention` line: off, or the number of days, and in both cases the number of archived days.

**Rule matching.** Rules (`[[rules]]` and learned rules) are deliberately simple. A repository matches that path and everything below it, on a path boundary (`/work/app` covers `/work/app/sub`, not `/work/application`). A learned rule's branch must equal the branch name exactly: there are no wildcards or globs, so `feature/*` only matches a branch literally named that. Add one rule per branch, or leave the branch out to cover the whole repository. `ticktack map` lists and edits learned rules.

---

## 🗂️ Standard tasks

YouTrack work items always belong to an issue. Time without a ticket therefore goes to a **standard task**: one catch-all issue per project plus one global fallback. Set `[default_issue]` to turn this on; add `repo_roots` to also get one task per project.

Resolution order for a block:

1. an explicit switch or an override
2. a ticket id carried by the event, then one in the branch name
3. a `[[rules]]` entry, then a learned rule
4. the standard task of the project that matches the repository's folder name
5. the global standard task from `[default_issue]`

- **Folder to project.** The folder under a `repo_roots` entry is compared with the YouTrack project **names**. Case, `-`, `_` and spaces are ignored. The project is used only when exactly one matches; otherwise the global task applies. Matching runs locally against a cached project list, so no folder name leaves the machine. `ticktack review` refreshes the list; `ticktack doctor` shows its age. Run `ticktack review` after creating or renaming a YouTrack project.
- **Creating the task.** A standard task is found by the marker `[ticktack:default-issue]` in its description, and only when you reported it. If it is missing, `ticktack sync` asks once for all projects at once: `Create standard task in: A, B, C — continue? [y/N]`. Until the task exists, review shows it as `<PROJECT>:default` and nothing is booked on it.
- **Auto mode.** `ticktack auto` creates a missing task only for a project you confirmed before. Other time stays unbooked and the digest says why.
- **Booking.** Standard task time follows the usual rules: reviewed first, booked once per issue, day and type.

> [!NOTE]
> The only data sent to YouTrack is the same as before: the project list is read and the standard task is created in your own projects. Folder names are never sent.

---

## 🛠️ Commands

|    | Command                         | What it does                                                                         |
|:--:|---------------------------------|--------------------------------------------------------------------------------------|
| ℹ️ | `ticktack version`              | Print the ticktack version.                                                          |
| 🧰 | `ticktack init`                 | Set up config, token, shell hook and launchd agent (`--yes`, `--url`, `--shell`, `--skip-config`, `--skip-token`, `--skip-hook`, `--skip-agent`, `--skip-alias`, `--print-snippet SHELL`). |
| 🩺 | `ticktack doctor`               | Check the setup and name the fix for each problem.                                   |
| 🖥️ | `ticktack agent`                | Run the presence agent in the foreground (launchd runs it for you; `--interval`).    |
| 🐚 | `ticktack hook`                 | Internal: reports the working directory from the shell prompt. Hidden from help.     |
| 👀 | `ticktack status`               | Show what ticktack currently detects.                                                |
| 📋 | `ticktack today`                | List today's blocks and totals (`--day YYYY-MM-DD`).                                 |
| 📌 | `ticktack switch TICKET`        | Attribute time to a ticket from now on; `--clear` or `switch auto` returns to detection. |
| ⏸️ | `ticktack pause`                | Pause all capture. Time inside a pause is never stored, also not by a later scan or import. |
| ▶️ | `ticktack resume`               | Resume capture after a pause.                                                        |
| 🗑️ | `ticktack purge`                | Empty the local database: events, overrides, ledger and sync history, review state, learned rules, digests, the cached project list, standard tasks, the consent to create them and the list of archived days (`--yes` skips the prompt). Config, token, agent and shell hook stay. |
| 🧽 | `ticktack purge --expired`      | Delete only the raw events of fully booked days older than `retention_days` (`--dry-run` prints the number of events and days without deleting; `--yes` skips the prompt). The days can no longer be re-derived. See [Retention](#retention). |
| 🧹 | `ticktack uninstall`            | Remove the launchd agent, the shell hook block, the keychain token and the log file (`--yes` skips the prompt). See [Uninstall](#uninstall). |
| ✅ | `ticktack review`               | Review the day's blocks and confirm what may be booked (`--day`).                    |
| 🔍 | `ticktack explain <N\|HH:MM>`   | Show the signal and rule behind a block (`--day`).                                   |
| 🗺️ | `ticktack map`                  | List and edit rules: `map list`, `map add --repo R [--branch B] TICKET`, `map edit ID`, `map rm ID`. |
| 📒 | `ticktack sync`                 | Book the confirmed time of a day in YouTrack (`--day`, `--dry-run`).                 |
| ↩️ | `ticktack undo`                 | Revert the last sync.                                                                |
| 🤖 | `ticktack auto`                 | Book the safe blocks of a day without review once trust is earned (`--day`, `--dry-run`). |
| 📰 | `ticktack digest`               | Show the digest of the last auto run (stored as plain ASCII text without colour, so it stays stable). |
| 🔭 | `ticktack scan`                 | Read the git reflog of the repositories in `repo_paths` and store repo and branch context (`--since YYYY-MM-DD`, default: the start of the day 7 days ago). |
| 🔌 | `ticktack import activitywatch` | Import window and AFK events from a local ActivityWatch server (`--since YYYY-MM-DD`). |

Run `ticktack <command> --help` for details.

Every command accepts the global flag `--no-color`.

---

## 🎨 Output and colours

The interactive commands (`init`, `doctor`, `status`, `today`, `review`, `explain`, `map`, `scan`, `sync`, `auto`, `digest`, `import`, `undo`, `purge`, `uninstall`, `pause`, `resume`, `switch`) print structured output: headings, aligned tables and status lines whose glyph tells success, warning and failure apart even without colour. This is `ticktack today` as plain text:

```text
  Blocks for 2026-03-02

  #  Time         Ticket        Tier      Reason
  1  08:00-08:40  ABC-7         explicit  ticket ABC-7 found in branch "ABC...
  2  09:30-09:50  (unassigned)  unknown   no ticket in branch and no rule f...

  Totals
  Ticket        Time  Raw
  (unassigned)   15m  21m
  ABC-7          45m  41m

  Total  1h
  !  OVER DAILY CAP (30m0s)
```

| Situation                                   | Behaviour                                                          |
|---------------------------------------------|--------------------------------------------------------------------|
| 🖥️ Output is a terminal                     | Colours are on.                                                    |
| 🔀 Output is piped or redirected            | No colour and no ANSI escape codes; the text is stable for scripts. |
| 🚫 `--no-color`, `NO_COLOR` non-empty, `TERM=dumb` | Colours are off.                                                   |
| 🔤 Locale is not UTF-8 or unset, or `TERM=dumb` | ASCII glyphs and frames (`+`, `!`, `x`, `i`) instead of Unicode symbols. |
| 📏 Width                                    | Follows the terminal, capped at 100 columns; 78 when the output is not a terminal. Long reasons in `review` and `digest` are never cut: they wrap onto indented lines under their row. On a narrow terminal `review` also moves tier and state below the row. Text is measured in terminal cells, so wide characters stay aligned. |

`ticktack agent`, `ticktack hook` and `ticktack version` keep their plain output: the agent writes a log and the hook runs at every shell prompt.

---

## 🧩 Optional integrations

All of these are **off by default** and can be enabled independently.

| Integration         | How to enable                                                       | Notes                                                                 |
|---------------------|---------------------------------------------------------------------|-----------------------------------------------------------------------|
| 🐚 Shell hook       | Keep the hook step in `ticktack init` (zsh, bash, fish).            | The source of repository and branch context: at each prompt it records the repository root and branch. Without it, context comes from `ticktack scan`, manual pins (`ticktack switch`), window-title hints or the ActivityWatch import. Managed block in your rc file. The same block holds the optional `tt` alias, see [The `tt` alias](#the-tt-alias). |
| 🔭 Git reflog scan  | List repositories in `repo_paths`, then run `ticktack scan`. The agent then scans at start and every 15 minutes; change this with `[scan] interval` (for example `"30m"`) or turn it off with `"0"`. | Works for repositories used without a shell prompt, such as IDE git, and fills gaps afterwards. Reads only the HEAD reflog (read-only `git`, no hooks, no writes); stores repo, branch, ticket hint from the branch name and timestamp, never commit messages or file contents. Scanning again changes nothing; the agent skips scans while capture is paused. `ticktack doctor` checks each path. |
| 🪟 Window-title hints | `[titlehint] enabled = true` plus a project-to-repository map.    | Needs the macOS Accessibility permission. Titles are parsed in memory for a project hint and never stored. |
| 🔌 ActivityWatch import | `[activitywatch] enabled = true`, then `ticktack import activitywatch`. | Talks only to a loopback server.                                |
| 🤖 Auto mode        | `[auto] enabled = true`, then `ticktack auto`.                      | Guarded by the trust gate below.                                      |
| 🛫 Autopilot        | `[auto] enabled = true` and `mode = "autopilot"` (planned, see below). | Opt-in alternative to the trust gate; the agent books completed days itself. |

> [!IMPORTANT]
> **The trust gate.** Auto mode unlocks only after the last `trusted_days` (default 10) consecutive reviewed days were confirmed **without a single correction**. A later correction locks it again. Even when unlocked it books only `explicit` and `rule` blocks, skips any item that contains lower-tier time, and does not book a day above the daily cap. There is no background scheduler: run `ticktack auto` yourself or from your own cron or launchd job. Each run prints a digest (`ticktack digest` shows it again) and `ticktack undo` reverts it.

> [!NOTE]
> **Autopilot (planned, TICKT-97).** If you would rather not review every day, you can opt in to `mode = "autopilot"` (the default is `"gated"`, the behaviour described above). The agent then confirms and books completed days itself, without the trust gate and without an external scheduler. It never touches today's open day, honours pauses, books only `explicit` and `rule` blocks, leaves any item with lower-tier time for `ticktack review`, and does not overwrite a day you confirmed or booked by hand. Each run writes a digest and `ticktack undo` reverts it. Autopilot is never on by default. This mode is not implemented yet: until it ships, only the gated mode above exists.

What the trust gate counts:

| | Case | Effect on the streak |
|:--|:--|:--|
| ✅ | Every item with a ticket is confirmed with the minutes it has now, on that calendar day or the next one (in your configured time zone). | The day counts. |
| ⏭️ | An item is unconfirmed, or its minutes changed after you confirmed it. | The day does not count until you confirm it again in time. |
| ⏭️ | The day was confirmed later with `ticktack review --day`. | Valid for booking, but the day does not count. |
| ⏭️ | Unassigned time or an idle gap stays open, or you give it a ticket, split it, mark it as a break or discard it. | No correction: the day still counts once its items are confirmed. |
| ⏭️ | `ticktack switch`. | No correction: it is information given in advance. |
| ❌ | Time that already had a ticket is reassigned, discarded, marked as a break or split. | Correction: the streak ends at that day. |
| ❌ | `ticktack map add`, `edit` or `rm`. | Correction on the day of the change: the streak ends there. |

Days without a review and days booked by auto mode are skipped: they neither count nor end the streak.

---

## 🔐 Privacy

- Never stored: window titles, screenshots, keystrokes, file contents. Titles may be parsed in memory for a project hint only.
- `ticktack scan` runs the local `git` binary read-only on your configured repositories and keeps only repo, branch, ticket hint and timestamp.
- Commit subjects are never stored with events. With `description_from_commits` (on by default), `ticktack sync` and `ticktack auto` read the subjects of the commits made during the booked time from the repository, with the local `git` and no network, and send them to YouTrack as the work item text. The booked text, and the text it replaced, is kept in the local sync history (`sync_entries`) so `ticktack undo` can restore it; that is the only place a subject is stored, and `ticktack purge` empties it. Set `description_from_commits = false` to book the short summary instead.
- Network calls go only to the configured YouTrack instance (`sync`, suggestions in `review`, `doctor`, the token check in `init`) and, if enabled, to ActivityWatch on loopback. No telemetry.
- The database, the log file and their directories are readable by your user only (files `0600`, directories `0700`). The database runs in WAL mode, so `events.db-wal` and `events.db-shm` appear next to it while it is in use; they hold the same data, carry the same `0600` mode and are removed with the database file when you delete the data directory. `ticktack purge` empties the tables and leaves no events behind in them. The bundled SQLite (3.54.1 in `modernc.org/sqlite` v1.62.0) contains the fix for the WAL-reset bug (3.51.3, backports 3.44.6 and 3.50.7); a test fails if a driver update falls below it.
- The YouTrack token lives in the OS keychain, never in the config, environment files or logs.
- Nothing is booked without your review, unless you enabled auto mode: either gated by the trust gate (default) or, once available, as an explicit opt-in autopilot.
- Raw events are kept until you delete them, unless you set `retention_days`: then the agent deletes the events of fully booked days older than that many days (see [Retention](#retention)). Those days can no longer be re-derived.
- `ticktack pause` stops all capture until `ticktack resume`. The paused time stays out for good: `ticktack scan`, the agent's periodic scan and `ticktack import activitywatch` drop every event whose timestamp lies inside a pause, also when they run after the resume. A pause that is still running counts until now. A gap in presence (for example from an ActivityWatch import) that reaches into a pause is cut at the pause, so its paused part is not stored either. If the pause marker file is damaged, `ticktack resume` still ends the pause and tells you; the pause then starts at the file's modification time, so time before that may not be protected.
- `ticktack purge` empties every table of the local database, including the consent that lets auto mode create a standard task. Afterwards auto mode asks again.
- `ticktack uninstall` removes the launchd agent, the shell hook, the keychain token and the log file.

> [!NOTE]
> To leave paused time out later, ticktack keeps the start and end of every pause in `pause-intervals` in the state directory. The file holds timestamps only. `ticktack purge` does not delete it, so a scan after a purge cannot read paused time back from the git reflog. Events stored before a pause began are not removed by it.

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
| `internal/render/` | Colours, glyphs, tables and layout shared by the commands.      |
| `launchd/`        | Renders the launchd job definition of the presence agent.        |
| `internal/collector/` | Event sources: presence, window-title, ActivityWatch.   |
| `internal/resolver/`, `internal/sessionizer/` | Pure functions that attribute tickets and build blocks. |
| `internal/store/`, `internal/ledger/` | SQLite storage (pure Go, no CGO) and the booking ledger. |
| `internal/sink/`, `internal/youtrack/` | The booking target and its YouTrack client.      |
| `internal/config/`, `internal/paths/`, `internal/token/` | Config loading, file locations, keychain access. |
| `internal/event/`, `internal/localday/`, `internal/ticketid/` | The event model, local calendar-day boundaries and ticket-id matching. `localday` and the shared interfaces use `cloud.google.com/go/civil.Date`: the dependency stays because only that subpackage is compiled and a custom date type would add risk at date boundaries. |
| `internal/review/`, `internal/suggest/`, `internal/explain/`, `internal/auto/` | Review flow, ticket suggestions, block explanations and auto mode. |
| `internal/retention/` | The retention cleanup: decides which days are fully booked and archives them. |
| `internal/reporoots/`, `internal/shellhook/` | Repository discovery under `repo_roots` and the shell-hook snippets. |
| `internal/logfile/`, `internal/runlock/`, `internal/loopback/` | Rotating log, single-run lock and the loopback-only address check. |

Tests are table-driven and the YouTrack client is tested against an `httptest` server; no test talks to a real YouTrack instance.

### Continuous integration

[`.github/workflows/ci.yml`](.github/workflows/ci.yml) runs on every pull request and on pushes to `main`. It needs no secrets and never talks to YouTrack; the `e2e` build-tag test is not run.

| Job name               | Check                                                  |
|------------------------|--------------------------------------------------------|
| `lint`                 | `golangci-lint` (version pinned in the workflow) on Linux |
| `lint-macos`           | the same lint on macOS, the only run that analyses the darwin-only files (advisory, not required) |
| `test (macos-latest)`  | `make test` (`CGO_ENABLED=0 go test ./...`), then `go test -race ./...` on macOS |
| `test (ubuntu-latest)` | the same tests and race run on Linux, to catch portability issues |
| `build`                | `make build` on macOS                                  |
| `tidy`                 | `go mod tidy -diff` fails when `go.mod`/`go.sum` drift |

Register `lint`, `test (macos-latest)`, `test (ubuntu-latest)`, `build` and `tidy` as required status checks when protecting `main`. `make test-race` runs the race tests locally.

[`.github/workflows/docs-drift.yml`](.github/workflows/docs-drift.yml) also runs on every pull request. It uses [doc-drift-checker](https://github.com/JtheGunner/doc-drift-checker) to flag README sections whose code changed while the section did not, and reports them as annotations. It never fails the build, so its `docs-drift` job is advisory and not a required check. Configuration lives in [`.docdrift.yaml`](.docdrift.yaml). To run it locally against `main`:

```sh
go install github.com/JtheGunner/doc-drift-checker/cmd/doc-drift-checker@v0.2.3
doc-drift-checker check --base origin/main
```

### Releasing

For the maintainer. Releases are built by [GoReleaser](https://goreleaser.com) from [`.goreleaser.yaml`](.goreleaser.yaml).

1. Move the `Unreleased` entries in `CHANGELOG.md` under a new version heading with the date and commit.
2. Tag the commit: `git tag vX.Y.Z && git push origin vX.Y.Z`.

Pushing a tag that matches `v*` starts [`.github/workflows/release.yml`](.github/workflows/release.yml). The workflow runs only in the `JtheGunner/ticktack` repository. It checks out the full history, sets up the Go version from `go.mod` without a build cache and runs `goreleaser release --clean`. Only the release job holds the `contents: write` permission; the workflow is read-only otherwise.

GoReleaser first runs `go mod tidy` and `go test ./...` (the `before` hooks), so a failing test stops the release. It then builds the binaries, publishes the GitHub release and pushes the Homebrew formula. The workflow needs one repository secret, `HOMEBREW_TAP_GITHUB_TOKEN`: a token with write access to `JtheGunner/homebrew-tap`. The workflow's own `GITHUB_TOKEN` publishes the GitHub release.

The release contains darwin arm64 and amd64 archives (`tar.gz` with `LICENSE`, `README.md`, `CHANGELOG.md` and `examples/config.toml`) and `checksums.txt`. GoReleaser does not generate release notes; they are maintained by hand in `CHANGELOG.md`.

To release from your machine instead, check out the tagged commit with a clean working tree, export a GitHub token as `GITHUB_TOKEN` and the tap token as `HOMEBREW_TAP_GITHUB_TOKEN`, then run `goreleaser release --clean`. `goreleaser release --snapshot --clean` is a dry run that publishes nothing.

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
