## Workflow

1. `bd memories` — read the persistent learnings. They auto-inject; do not skip.
2. `bd ready` — find unblocked tasks. Pick the highest-priority one (lowest P-number).
3. `bd update <id> --claim` — mark it in-progress before starting work.
4. `bd show <id>` — read the full description.
5. If the task references a byob decision (label like `factory`, `iostreams`, `command-shape`), run `bd list --type decision -l <label>` then `bd show <decision-id>` for the design contract.
6. Implement. Match existing idioms — three-part command shape (Options + NewCmdXxx + xxxRun), factory injection, IOStreams for I/O, FlagErrorf for flag errors, context propagation.
7. `make build && make test && make vet` — must pass before commit.
8. `git add <files> && git commit -m "..."` — small, focused commits referencing the bead ID in the message body.
9. `bd close <id> --reason "..."` — close with a one-line summary of what landed.
10. `bd remember --key <topic> ...` — save any non-obvious finding, especially root causes of bugs you fixed, surprising constraints, and invariants future work must respect. Context dies between sessions; memories are the only bridge.
11. `bd create -t task "..."` for any new work uncovered.
12. End by writing one paragraph to `.ralph/state/session.md` describing what you did, what you learned, and what's next. This is the human's morning briefing.

## Safety rules

- Never `git push`. Never `--no-verify`. Never `--force` anything destructive.
- Do not modify `.beads/` directly — go through `bd` commands.
- Do not delete `.ralph/state/` or anything in it.
- If the gate fails, do not bypass it — fix the regression or revert.
- If you find yourself in a loop, `bd remember` the trap and `bd defer` the task.
