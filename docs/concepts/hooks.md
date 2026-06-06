# Hooks

Hooks are executable scripts under `.ralph/hooks/`. Any language; just `chmod +x`. Ralph invokes them with `cwd = repo root` and a stable env.

## Layout

```
.ralph/hooks/
├── pre-iteration       # global: runs before every iteration
├── post-iteration      # global: runs after every iteration
├── failure             # global: runs when FSM enters `failed`
├── notify              # global: runs on every terminal outcome (done or failed)
└── states/
    ├── clean/{enter,exit,gate}
    ├── dirty/{enter,exit,gate}
    ├── revert/{enter,exit,gate}
    └── review/{enter,exit,gate}
```

Names are single verbs (`enter`, `exit`, `gate`) — not `on_enter`/`on_exit`.

## Lifecycle per iteration

```mermaid
flowchart TD
  A[pre-iteration] --> B{state changed?}
  B -->|yes| C["states/&lt;state&gt;/enter"]
  B -->|no| D[render prompt + run runner]
  C --> D
  D --> E["states/&lt;state&gt;/gate"]
  E --> F[write iteration record]
  F --> G[post-iteration]
  G --> H[SelectNextState]
  H --> I{state changing?}
  I -->|yes| J["states/&lt;prev&gt;/exit"]
  I -->|no| A
  J --> A
```

`pre-iteration` runs first so it can short-circuit the tick (non-zero exit → skip the runner). `enter` runs after, but only on entry into a new state.

## Hook contracts

| Hook                            | Input              | Exit semantics                                                                          |
|---------------------------------|--------------------|-----------------------------------------------------------------------------------------|
| `pre-iteration`                 | env only           | Non-zero → skip iteration; log stderr.                                                  |
| `post-iteration`                | iteration JSON on stdin | Exit ignored.                                                                       |
| `failure`                       | env: `RALPH_FAILURE_MODE`, `RALPH_FAILURE_REASON` | Exit ignored. React to OOM / rate-limit / terminal.   |
| `notify`                        | env: `RALPH_REPO`, `RALPH_ITER`, `RALPH_STATE`, `RALPH_REASON`, `RALPH_COST_USD`, `RALPH_DURATION_SECS` | Exit ignored. Fires on every terminal outcome (after `failure` for a failed run) — reach an unattended operator (Slack/desktop/email). |
| `states/<state>/enter`          | env only           | Non-zero → log warning, continue.                                                       |
| `states/<state>/exit`           | env: `RALPH_NEXT_STATE` | Non-zero → log warning, continue.                                                  |
| `states/<state>/gate`           | env only           | Exit 0 = pass, non-zero = fail. Result surfaces in `{{.GateResult}}` for next prompt. Whether failure routes to `failed` depends on `[gate] soft_fail` — see [config reference](../reference/config.md). |

Missing hooks are fine — every slot has a no-op default.

## Environment

Always set:

- `RALPH_REPO` — absolute path to the repo root.
- `RALPH_ITER` — current iteration number (1-indexed).
- `RALPH_STATE` — current state name.
- `RALPH_PREV_STATE` — previous state name (empty on first iteration).
- `RALPH_PROMPT_FILE` — path to the rendered prompt file for this iteration.
- `RALPH_ITER_JSON` — path to the per-iteration JSON record once written.

Conditional:

- `RALPH_NEXT_STATE` — exit hooks only.
- `RALPH_FAILURE_MODE` / `RALPH_FAILURE_REASON` — failure hook only.
- `RALPH_REASON` — terminal reason for the `notify` hook (`queue_empty`, `iter_cap`, `idle`, `budget`, `auth`, `runner_terminal`).
- `RALPH_COST_USD` / `RALPH_DURATION_SECS` — `notify` hook only: the run's cumulative cost and runner runtime in seconds, totaled across the whole run (omitted when zero).
- `RALPH_REVIEW_BRANCH` / `RALPH_REVIEW_BASE` — review states only.

## What hooks should and shouldn't do

**Should**: run tests (`gate`), notify Slack on transitions (`post-iteration`), bump a marker file (`enter`), sync external comments into bd findings (`enter` for `review`).

**Shouldn't**: alter FSM state, write to `state/fsm.json` directly, parse the prompt and try to re-route. The FSM is closed-core; routing is the orchestrator's job.

## Testing a hook manually

```
ralph hook run .ralph/hooks/states/clean/gate
```

Sets the env vars from the current `state/fsm.json` and runs the hook with the same `cwd` the orchestrator uses. Output goes to your terminal.
