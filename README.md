# ralph

An FSM-driven autonomous-loop CLI for running an AI coding agent on a task queue, hour after hour, without losing the plot.

```
ralph init                          # scaffold .ralph/ in any repo
ralph run                           # drain the bd queue, FSM-routed
ralph review --branch feature-x     # iterate review findings until clean
ralph status                        # current state, recent transitions, cost
ralph timeline --since=8h           # narrative of what happened overnight
```

## What it is

The loop pattern (Geoffrey Huntley's "ralph engineering") works, but the interesting part isn't the loop — it's the **state machine** the orchestrator runs around the agent: `clean ↔ dirty ↔ revert`, with `review` as a separate mode for branch review. `ralph` extracts that machine into a typed Go FSM and the per-repo configuration into a `.ralph/` directory analogous to `.git/`.

Prompts and hooks customize behavior *within* states. The state topology is fixed in the binary; only the prompts and the work pulled from `bd` change between repos.

## Concepts

- **[ralph fsm](docs/concepts/ralph-fsm.md)** — the methodology and why an FSM matters.
- **[state machine](docs/concepts/state-machine.md)** — the FSM topology, predicates, routing.
- **[hooks](docs/concepts/hooks.md)** — git-style executable scripts that slot into state lifecycle events.
- **[prompts](docs/concepts/prompts.md)** — templated markdown per state, with optional `_header.md` and `_footer.md`.
- **[bd integration](docs/concepts/bd-integration.md)** — why `bd` is first-class and how the orchestrator uses it.

## Reference

- **[CLI](docs/reference/cli.md)** — every subcommand and flag.
- **[Config](docs/reference/config.md)** — `.ralph/config.toml` fields.
- **[Isolation](docs/reference/isolation.md)** — `systemd-run --user --scope` and OOM detection (Linux-only).
- **[Backoff](docs/reference/backoff.md)** — failure-mode classification and the wait math.

## Guides

- **[Getting started](docs/guides/getting-started.md)** — install → init → first run → reading logs.
- **[Review mode](docs/guides/review-mode.md)** — end-to-end branch review.

## Constraints

- **Linux-only.** Isolation uses `systemd-run --user --scope` for memory caps and OOM detection. `ralph run` errors out on other OSes.
- **Requires `bd`.** Tasks, decisions, and memories live in [Beads](https://github.com/jcrussell/beads). No SQLite, no second store.
- **Requires `claude`.** v1 shells out to the Claude Code CLI. Other runners are deferred until the abstraction earns its keep.
