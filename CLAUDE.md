# ticktack — project instructions

Deltas to the global rules in `~/.claude/rules/`. Passive time tracker (Go CLI) that turns git/shell/presence signals into reviewed YouTrack work items.

## Where things live

- Design and plan: `.planning/design.md`, `.planning/plan.md`. **Gitignored — never commit them, never copy them to `docs/` or `docs/superpowers/`.** Read the design before changing architecture.
- Work is organised as YouTrack tickets derived from `.planning/plan.md` (one ticket per `T##` section); use the `youtrack-task` skill to pick them up.

## Architecture invariants

- **Presence and context are separate signals.** Time to book = presence ∩ context. Never infer duration from context events alone.
- **Events are append-only.** Blocks are always re-derived from events + rules + overrides. User corrections go into the overrides table, never into events.
- **Resolver and sessionizer are pure functions** (no I/O, no clock reads; time is passed in). Everything else sits behind interfaces: `Collector` (event sources) and `TimeSink` (booking target).
- **Booking is idempotent:** one work item per (issue, date, type), tracked in the ledger; update instead of create; item text carries a `[ticktack:<key>]` marker.
- Confidence is a discrete tier (`explicit`, `rule`, `learned`, `suggested`, `unknown`), not a number.

## Privacy invariants (anti-goals — a change that breaks one is a bug)

- Never persist window titles, screenshots, keystrokes, or file contents. Titles may be parsed in memory for a project hint only.
- Network calls go only to the configured YouTrack instance (`sync`, suggestions in `review`, `doctor`, the token check in `init`) and, if enabled, to ActivityWatch on loopback. No telemetry.
- The YouTrack token lives in the OS keychain, never in config, env files, logs, or tests.
- Nothing is booked without review unless the user enabled auto mode. Auto mode has two forms: `gated` (default; unlocks only when the trust gate is met) and `autopilot` (explicit opt-in via `[auto] mode = "autopilot"`; no trust gate, the agent books completed days itself). Both book only `explicit` and `rule` blocks (presence ∩ context time only), skip any item with lower-tier time, never touch today's open day, honour pauses, fail closed on any doubt (book nothing rather than something partial), never overwrite a day the user confirmed or booked by hand, and stay idempotent and reversible (`ticktack undo`, digest). Autopilot must never be the default and never switch itself on.

## Go conventions

- Go, single binary, pure-Go SQLite driver (no CGO in the core). macOS-only code behind build tags and the `Collector` interface.
- Layout: `cmd/ticktack/` (entrypoint), `internal/<package>/` (one responsibility each).
- Tests: table-driven; replay fixtures in `testdata/`; the YouTrack client is tested against an `httptest` server. No test talks to a real YouTrack instance except the explicitly tagged e2e test (`//go:build e2e`).
- Run before handing off: `gofmt`, `go vet`, `golangci-lint run`, `go test ./...`.
- CLI output is English for now; keep user-facing strings in one place so a German variant can be added later.
