# bd integration

`bd` ([Beads](https://github.com/jcrussell/beads)) is ralph's only persistent store for *work*. Tasks, decisions, memories, and bead events all live in bd; ralph does not maintain a parallel store.

## Two stores, no overlap

- **Work-related** → `bd`: task status, decisions, memories, bead events. Queried with `bd list/show/memories/etc.`
- **Loop-related** → JSONL on disk under `.ralph/state/logs/`: iteration records, state transitions, run manifests, incidents. Queried with `jq` or the `ralph timeline`/`trace`/`report` subcommands.

No SQLite. No second index. No ralph subcommand wraps a bd surface — when you want bd, run `bd`.

## What the orchestrator calls

`internal/bd/bd.go` is a thin typed wrapper over the `bd` binary. Every method shells out — no reimplementation of bd semantics. The surface is intentionally small:

| `bd.Client` method  | Underlying call                                  | When the orchestrator uses it |
|---------------------|--------------------------------------------------|-------------------------------|
| `Ready(ctx, label)` | `bd ready --json [-l <label>]`                   | `BDReadyCount` predicate; unscoped + `review:<branch>` flavors. |
| `List(ctx, status, label)` | `bd list --json -n 0 --status <s> [-l <l>]` | `BDInProgressCount` predicate; `Snapshot` for bead-diff. |
| `Create(ctx, opts)` | `bd create -t <type> ...`                        | Backstop for filing `merge:<branch>` beads when the agent fails to file one itself. |
| `Close(ctx, ids...)` | `bd close <id>...`                              | Reserved — present so the orchestrator can close beads it created. |
| `Defer(ctx, id, when, reason)` | `bd defer <id> --until ... --reason ...` | Auto-revert path: defer the bead that triggered the streak. |
| `Remember(ctx, key, body)` | `bd remember --key <k> <body>`            | Auto-revert path: record the failure mode for future search. |
| `Snapshot(ctx)`     | `bd list --json --all -n 0`                      | Pre/post-iteration snapshot for bead-diff. |

```mermaid
sequenceDiagram
  participant Loop as orchestrator loop
  participant Pred as fsm predicates
  participant BD as bd.Client
  participant CLI as bd binary
  Loop->>Pred: SelectNextState(ctx, in)
  Pred->>BD: Ready(ctx, "")
  BD->>CLI: bd ready --json
  CLI-->>BD: []Issue
  BD-->>Pred: count
  Pred->>BD: List(ctx, "in_progress", "")
  BD->>CLI: bd list --json --status=in_progress -n 0
  CLI-->>BD: []Issue
  BD-->>Pred: count
  Pred-->>Loop: Outcome
```

Everything else — claiming, exploring decisions, searching memories from the *agent* — is the agent's job, driven by the prompt's workflow. The orchestrator stays out of the agent's queue management.

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
