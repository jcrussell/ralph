# Prompts

Each state has a markdown prompt at `.ralph/prompts/<state>.md`. Rendered with Go `text/template`. Optional `_header.md` and `_footer.md` wrap every state's prompt — the final order is **`_header → <state> → _footer`**.

## Template variables

| Variable             | Meaning                                                                           |
|----------------------|-----------------------------------------------------------------------------------|
| `.Iter`              | Current iteration number (1-indexed).                                             |
| `.State`             | Current state name.                                                               |
| `.PrevState`         | Previous state (empty on first iteration).                                        |
| `.GitDirty`          | bool — true if working tree has uncommitted tracked changes.                      |
| `.GitHead`           | Short SHA of HEAD.                                                                |
| `.RepoRoot`          | Absolute path to the repo root.                                                   |
| `.LastIter`          | Map of the previous iteration's summary record (state, narrative, commits, …).   |
| `.GateResult`        | Last gate-hook outcome: `passed`, `failed`, or `not-run`.                         |
| `.Review.Branch`     | Branch under review (review states only).                                         |
| `.Review.Base`       | Base branch for the review.                                                       |
| `.Review.OpenFindings` | Integer count of open `review:<branch>` findings.                               |

## The `include` helper

```
{{include "prompts/_shared/safety-rules.md"}}
```

Reads the file relative to the repo root and substitutes its rendered contents. Lets you keep one source of truth for snippets reused across prompts.

## Composition order

For state `clean`:

```
.ralph/prompts/_header.md            ← prepended (optional)
.ralph/prompts/clean.md              ← state body
.ralph/prompts/_footer.md            ← appended (optional)
```

`_header.md` and `_footer.md` are themselves templated with the same variables.

## Conventions

- **Header**: introduce the project, set the agent's mental model for the repo, declare invariants (paths, module name, language). Keep under 200 words.
- **State body**: the iteration-specific instruction. What to look at, what decision to make. The agent already knows the workflow — keep this lean.
- **Footer**: the workflow steps (`bd memories` → `bd ready` → claim → implement → close) plus safety rules. This is where the 12-step workflow lives.

## Rendering for debugging

```
ralph prompt show clean         # render with current FSM state vars
ralph prompt show review        # uses .Review.* vars from fsm.json
```

Prints the final composed text — same string that would be sent to the runner.

## Capture

Every iteration's *fully rendered* prompt is saved at `.ralph/state/logs/iter-NNNN-<ts>-prompt.txt`. Lets you reconstruct what the agent was actually asked even after templates evolve.
