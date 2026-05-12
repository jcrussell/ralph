# ralph fsm — the methodology

## The loop is the easy part

Looping an AI coding agent on a prompt — the "ralph engineering" pattern (Geoffrey Huntley) — is a one-page shell script. What makes the pattern *useful in practice* is everything around the loop:

- Picking a different prompt depending on git state, queue state, and recent history.
- Detecting and recovering when the agent leaves uncommitted noise behind.
- Classifying failures (rate limit vs. budget exhaustion vs. OOM vs. timeout) and reacting differently.
- Persisting learnings somewhere durable, because the agent's context dies between sessions.
- Stopping cleanly — for the right reason.

Each of those is a routing decision the orchestrator has to make. Together they form a state machine. The original Python orchestrator (`run_optimization_loop.py` in `~/repos/scratch/angr`) has all of this — but the state machine is implicit, tangled into 1078 lines of conditional logic specific to one project.

`ralph` extracts the FSM into a first-class, typed abstraction and the per-project configuration into `.ralph/`.

## Why FSM, not just hooks

The first instinct is "just make everything a hook." But hooks alone can't express the *transitions between* modes. You end up reinventing state-machine semantics inside shell scripts, with implicit sentinels (`__done__` strings, exit code conventions) and no help from the type system.

A typed FSM with named states, named transitions, and named predicates:

- gives every routing decision a single grep-able home,
- makes the topology testable in pure Go,
- forces the design to be explicit about what's stable (the topology) vs. what's customizable (prompts and side-effects within a state).

## Why closed-core

For a personal tool, opening the FSM to user-declared states and transitions is overhead with no payoff — you'll never edit `config.toml` to add a new state when you can just edit the prompt. So states and transitions are baked into the Go binary. `.ralph/` customizes *what happens within* states (which prompt, which gate, which side-effects via hooks); it does not customize *which states exist*.

If a future use case needs a different topology, the answer is "add it to the binary" or "fork ralph," not "expose a DSL."

## Why bd

Tasks, decisions, and memories all need durable storage that survives the orchestrator. `bd` (Beads) already does this well — embedded Dolt, label-scoped queries, first-class dependencies, a `memories` table. Building a parallel store inside ralph would either replicate bd or stay a strict subset of it. So ralph treats bd as first-class: the orchestrator calls `bd ready`, `bd close`, `bd defer`, `bd remember` directly, and the only thing ralph stores on disk is *loop history* (JSONL) — never task state.

No ralph subcommand wraps a bd surface. If you want to inspect the queue, run `bd ready` — `ralph status` exists for the *loop* dashboard, not the queue.

## Why Linux-only

Memory isolation matters. The original Python orchestrator OOM-kills itself on an 8GB machine without cgroup limits on the child claude process. `systemd-run --user --scope` with `MemoryMax` + `MemorySwapMax=0` is the only way to cap a subprocess reliably and detect OOM events without root. Mac and Windows alternatives are best-effort at best. Pick one platform, do it right.

## What "running ralph" looks like

```
$ ralph run --max-iterations 30
[start → clean]   iter 1   claimed ralph-7k2, 3 commits, gate green
[clean → clean]   iter 2   claimed ralph-9qj, 2 commits, gate green
[clean → dirty]   iter 3   uncommitted changes left behind
[dirty → clean]   iter 4   resolved + 1 commit
[clean → __done__/QueueEmpty]
```

That's the story. `ralph timeline` reconstructs it from `state/logs/summary.jsonl`. `ralph trace <iter>` drills into any single hop. `ralph report --since=8h` summarizes overnight work as a one-page markdown brief.

See also:
- [state-machine.md](state-machine.md) — the actual topology.
- [hooks.md](hooks.md) — how to slot side-effects into state lifecycle events.
- [bd-integration.md](bd-integration.md) — what ralph calls on bd, and what it deliberately doesn't.
