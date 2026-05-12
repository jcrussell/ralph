# bd integration

`bd` ([Beads](https://github.com/jcrussell/beads)) is ralph's only persistent store for *work*. Tasks, decisions, memories, and bead events all live in bd; ralph does not maintain a parallel store.

## Two stores, no overlap

- **Work-related** → `bd`: task status, decisions, memories, bead events. Queried with `bd list/show/memories/etc.`
- **Loop-related** → JSONL on disk under `.ralph/state/logs/`: iteration records, state transitions, run manifests, incidents. Queried with `jq` or the `ralph timeline`/`trace`/`report` subcommands.

No SQLite. No second index. No ralph subcommand wraps a bd surface — when you want bd, run `bd`.

## What the orchestrator calls

The orchestrator (`internal/bd/`) shells out to bd for:

| Call                                  | When                                                                |
|---------------------------------------|---------------------------------------------------------------------|
| `bd ready --json -l <label>`          | Predicate evaluation (`bd_ready_count`).                            |
| `bd list --status in_progress -l <label> --json` | Predicate evaluation (`bd_in_progress_count`).           |
| `bd defer <id>`                       | Auto-revert path; remove a stuck task from in-progress.            |
| `bd remember --key <topic> "..."`     | Auto-revert path; record the failure mode.                          |
| `bd create -t task -l "..." "..."`    | Used by `internal/review` to file `merge:<branch>` beads if/when the orchestrator opts to do so. (Currently the *agent* files the merge bead per prompt instruction; the orchestrator helper exists as a backstop.) |

Everything else — claiming, closing, exploring decisions, searching memories — is the *agent's* job, driven by the prompt's workflow. The orchestrator stays out of the agent's queue management.

## Why bd is first-class

Building a parallel store would either:

1. Replicate bd's surface — wasted code, two sources of truth, drift over time.
2. Stay a strict subset — then why not just use bd directly?

The decision: lean on bd, don't fight it. The user is going to run bd anyway. ralph's job is the loop, not the queue.

## What ralph contributes

- **The `review:<branch>` and `merge:<branch>` label conventions**. Review findings live under `review:<branch>`; merge requests under `merge:<branch>`. Either label is bd-native; ralph just suggests using them.
- **Bead-diff per iteration**. Each iteration record carries `bd_diff: {opened, closed, created, deferred}` derived by snapshotting `bd list --json` before and after the runner ran. Drives the narrative line and `ralph report`'s "Work done" section.

## What if I don't want bd?

You don't run ralph. v1 is bd-coupled and won't degrade gracefully. The plan explicitly defers "pluggable task DB" until a concrete need surfaces.
