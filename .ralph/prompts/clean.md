# Clean state

The working tree is clean. Your job: drain the bd queue.

## Setup checks (fast)

- `git status` — should be clean.
- `bd ready` — there should be at least one task. If empty, exit immediately with stdout `nothing to do`.

## Work

Claim ONE task per session — or two closely-related ones if the second is a trivial follow-up. Scope discipline matters: a single tight commit is better than three sprawling ones.

Then follow the workflow in the footer.
