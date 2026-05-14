# Review mode

Use `ralph review` when the work isn't "drain a queue" but "drive a branch to mergeable." The same FSM, with `review_mode=true` and one suppressed transition.

If you haven't run `ralph` against a regular queue yet, read [getting started](getting-started.md) first.

## What changes vs. normal mode

| Aspect              | `ralph run`                                  | `ralph review`                                       |
|---------------------|----------------------------------------------|------------------------------------------------------|
| FSM                 | `clean ↔ dirty ↔ revert`, terminal on done   | `review` loop, terminal on done; **`revert` suppressed** |
| Queue scope         | `bd ready` (unscoped)                        | `bd ready -l review:<branch>`                        |
| Termination         | bd queue empty + tree clean → `done{queue_empty}` | `review:<branch>` queue empty + tree clean → `done{queue_empty}` |
| Prompt              | per-state `clean.md` / `dirty.md`            | single `review.md` — the agent self-routes per iteration |

`revert` is suppressed because the work under review is the *whole point*; auto-`git checkout -- .` would silently destroy it. See [state machine — routing](../concepts/state-machine.md#routing) for the eight-step decision tree and why review takes priority.

The `review` state owns its own micro-routing inside the prompt. The agent decides per iteration: ingest the diff, claim and fix one finding, address a gate failure, or file the `merge:<branch>` bead. See `.ralph/prompts/review.md` for the rubric.

## Branch and base

`ralph review` resolves two refs:

- **Branch**: `--branch` wins; else the current checkout (`HEAD`). Detached HEAD without `--branch` is an error.
- **Base**: `--base` wins; else `[review] base_branch` from `.ralph/config.toml`; else `main`. Reviewing a branch against itself is rejected.

`--pr N` is sugar for `gh pr checkout N` *before* resolution — handy when reviewing a PR you haven't checked out locally. `gh` is not a hard dependency; if you don't have it, check out the branch yourself and pass `--branch`.

## Run it

Smoke first — render the review prompt, no runner:

```sh
ralph review --once --dry-run --base=main
```

Drive the full review:

```sh
ralph review --base=main
```

Or, from a freshly-pulled PR:

```sh
ralph review --pr=123
```

Cap iterations for this invocation:

```sh
ralph review --base=main --max-rounds=10
```

If the prior review terminated, the loop refuses to silently no-op. Reset and re-enter:

```sh
ralph review --fresh --base=main
```

Full flags in [`ralph review`](../reference/ralph_review.md).

## What ralph writes

Same artifacts as normal mode (see [getting started — reading what happened](getting-started.md#reading-what-happened)), plus:

- Every iteration's `summary.jsonl` record carries `label="review:<branch>"` (unless you set `--label` or `--no-label`). Filter the timeline by branch:

  ```sh
  ralph timeline --label=review:feat/foo
  ```

- The agent files `merge:<branch>` beads via `bd create` as findings drop off the queue. Check them with:

  ```sh
  bd list -l review:feat/foo
  bd list -l merge:feat/foo
  ```

## Hooks specific to review

The same hook lifecycle applies; the `review` per-state hook directory is the slot:

```
.ralph/hooks/states/review/
├── enter   # runs once when transitioning into review
├── gate    # runs after each review iteration (sets last_gate_result)
└── exit    # runs once when transitioning out
```

Use `gate` to enforce branch-specific checks (lint, type, targeted tests). Cross-link: [hooks](../concepts/hooks.md) for env vars and exit-code semantics.

## When to stop

Review mode terminates on `done{queue_empty}` once `bd ready -l review:<branch>` is empty *and* the tree is clean. If the agent files `merge:<branch>` but leaves uncommitted noise, the FSM routes back through `dirty` until either resolved or `max_iterations` hits — then `done{iter_cap}`. `failed{*}` outcomes (auth, budget, runner_terminal) fire the global failure hook just like in normal mode.
