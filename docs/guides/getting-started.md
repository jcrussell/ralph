# Getting started

Install → init → first iteration → reading what happened. Five minutes if `bd` is already set up.

## Prerequisites

- **Linux.** Isolation is `systemd-run --user --scope` only. macOS and Windows will error at `ralph run` startup. See [isolation](../reference/isolation.md).
- **`bd` on `$PATH`** with a populated queue. Tasks, decisions, and memories live there; ralph never duplicates them. See [bd integration](../concepts/bd-integration.md).
- **`claude` CLI** on `$PATH`. v1 shells out to it directly.
- **Go 1.22+** if you're installing from source.

## Install

```sh
go install github.com/jcrussell/ralph/cmd/ralph@latest
ralph version
```

## Init

From inside any git repo:

```sh
ralph init
```

That scaffolds `.ralph/` next to `.git/`:

```
.ralph/
├── config.toml         # see docs/reference/config.md
├── prompts/            # one *.md per state, plus optional _header.md/_footer.md
├── hooks/              # global + per-state executable scripts
└── state/              # runtime — gitignored
```

Re-running `init` is a no-op; pass `--force` to overwrite. Full flags in [`ralph init`](../reference/ralph_init.md).

## First iteration

Smoke-test prompts and routing without invoking the runner:

```sh
ralph run --once --dry-run
```

This renders the prompt for the chosen state, writes an iteration record, and exits — `claude` is never invoked. Inspect the rendered prompt at `.ralph/state/logs/iter-1-prompt.txt`.

Then take one real iteration:

```sh
ralph run --once
```

For a full autonomous session, drop `--once`. The loop terminates on `done{queue_empty}`, `done{iter_cap}`, or one of the `failed{*}` outcomes. Full flags in [`ralph run`](../reference/ralph_run.md).

Since a long run is unattended, get pinged when it stops: edit `.ralph/hooks/notify` (scaffolded as a no-op) to send a Slack/desktop alert — it fires on every terminal outcome with `RALPH_STATE`, `RALPH_REASON`, and `RALPH_COST_USD` in the env. See [Hooks](../concepts/hooks.md).

### Resuming and resetting

`fsm.json` is the resume point. If the prior run terminated (`done{*}` or `failed{*}`), a follow-up `ralph run` will refuse to silently no-op — it prints a notice to stderr and exits non-zero. To start over, pass `--fresh`:

```sh
ralph run --fresh
```

`--fresh` rewrites `fsm.json` to the start state but preserves `state/runs/` history. See [state machine — persistence](../concepts/state-machine.md#persistence).

## Reading what happened

Three views, three subcommands:

| What you want                          | Command                          |
|----------------------------------------|----------------------------------|
| Current FSM state + recent transitions | [`ralph status`](../reference/ralph_status.md)     |
| Chronological narrative                | [`ralph timeline`](../reference/ralph_timeline.md) |
| Drill into one iteration               | [`ralph trace <iter>`](../reference/ralph_trace.md) |

The raw artifacts ralph reads from are also fair game for `jq`:

- `.ralph/state/logs/summary.jsonl` — one record per iteration.
- `.ralph/state/logs/orchestrator.log` — slog-formatted runtime log.
- `.ralph/state/runs/<run-id>/manifest.json` + `transitions.jsonl` — one directory per `ralph run` invocation.

## Next steps

- [State machine](../concepts/state-machine.md) — the topology you've been watching scroll by.
- [Prompts](../concepts/prompts.md) — how each state's `*.md` is rendered, what template variables are exposed.
- [Hooks](../concepts/hooks.md) — slot side-effects into state lifecycle events.
- [Review mode](review-mode.md) — when you have a branch instead of a queue.
