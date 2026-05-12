# State machine

The FSM is defined in `internal/fsm/`. Topology is fixed in the binary; `.ralph/` customizes prompts and hooks per state, never the topology.

## States

| State            | Kind     | What it means                                                                              |
|------------------|----------|--------------------------------------------------------------------------------------------|
| `start`          | initial  | First iteration; immediately calls `selectNextState`.                                       |
| `clean`          | loop     | Working tree clean, draining `bd ready`.                                                    |
| `dirty`          | loop     | Working tree dirty; assess & finish or accumulate streak.                                   |
| `revert`         | one-shot | Auto-revert triggered (3 consecutive dirty iterations); `git checkout -- .` + `bd defer`. Suppressed in review mode. |
| `review`         | loop     | Review-mode loop; agent decides ingest / fix / address-gate / file-merge per iteration.    |
| `done{Reason}`   | terminal | Exit 0. Reasons: `QueueEmpty` (nothing left to do), `IterCap` (max iterations reached).    |
| `failed{Reason}` | terminal | Exit 1. Reasons: `Budget`, `Auth`, `RunnerTerminal`. Runs `hooks/failure`.                 |

There is no `idle` state. A state with no prompt and no hook is just a function call wearing a hat — routing is centralized in `selectNextState`.

## Routing

After every iteration, `selectNextState(ctx)` evaluates predicates in order and returns the next state. Pseudocode (real source: `internal/fsm/select.go`):

```
if terminal_runner_failure                                              -> failed{<mode>}
if budget_exhausted                                                     -> failed{Budget}
if caps_exceeded                                                        -> done{IterCap}

if review_mode_on:
    if review_queue_empty && git_clean                                  -> done{QueueEmpty}
    return review

if dirty_streak_exceeded                                                -> revert
if git_dirty                                                            -> dirty
if bd_ready_count(unscoped) == 0 && bd_in_progress_count == 0 && git_clean
                                                                        -> done{QueueEmpty}
return clean
```

Notes on ordering:

- **Failures first.** Terminal runner failure and budget exhaustion always win.
- **Caps before review/run.** A graceful exit on `max_iterations` takes precedence over re-entering a loop state.
- **`done` requires `git_clean`.** Prevents exiting with uncommitted work.
- **Review uses label-scoped queue.** Otherwise an unrelated empty global queue would terminate review mid-stream.

## Predicates

All predicates are pure Go functions in `internal/fsm/predicates.go`:

- `git_clean(ctx)` / `git_dirty(ctx)` — working-tree status, excluding untracked.
- `bd_ready_count(ctx, label)` — `len(bd ready -l <label> --json)`. Empty label = unscoped.
- `bd_in_progress_count(ctx, label)` — `len(bd list --status in_progress -l <label> --json)`.
- `review_queue_empty(ctx)` — `bd_ready_count(ctx, "review:"+branch) == 0 && bd_in_progress_count(ctx, "review:"+branch) == 0`.
- `dirty_streak_exceeded(ctx)` — `fsm.consecutive_dirty >= [backoff] dirty_revert_threshold`.
- `caps_exceeded(ctx)` — `fsm.iter >= [loop] max_iterations`.
- `budget_exhausted(ctx)` — cost or wallclock cap exceeded.
- `terminal_runner_failure(ctx)` — runner reports `auth_error` or claude-side `budget_exhausted`.
- `review_mode_on(ctx)` — `fsm.review_mode` flag, set by `ralph review`.

## Counters

In `internal/fsm/counters.go`:

- `iter` increments once per loop iteration.
- `consecutive_dirty` increments on `* → dirty`, resets on `* → clean` or `* → revert`.

## Persistence

`state/fsm.json` records: current state + reason, counters, review fields, cumulative cost/wallclock. Survives orchestrator restarts. `ralph run` resumes from it unless `--fresh` is passed.

## The review state carries its own routing logic

The single `review` state covers ingest, fix, gate-fail, and merge-filing through one prompt — the agent decides which based on `{{.Review.OpenFindings}}`, `{{.GateResult}}`, and the diff. The FSM does not break review into sub-states because per-iteration context lets the agent route itself, and a single state stays grep-able.

See [.ralph/prompts/review.md](../../.ralph/prompts/review.md) for the routing the agent performs.
