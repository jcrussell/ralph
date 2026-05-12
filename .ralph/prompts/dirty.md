# Dirty state

The working tree has uncommitted changes from a previous session. You have 15 minutes to decide what to do with them.

## Decision matrix

Run `git status` and `git diff` first. Then:

| Compiles? | Tests pass? | Action                                                        |
|-----------|-------------|---------------------------------------------------------------|
| yes       | yes         | Finish the work, commit, close the bead.                      |
| yes       | no          | Either fix the failing test (≤15 min) or `git checkout -- .`. |
| no        | n/a         | Either fix the compile error (≤15 min) or revert.             |
| obviously broken | n/a  | Revert immediately: `git checkout -- . && git clean -fd`.     |

**Time-box this.** If the decision is unclear after 15 minutes, revert and defer the in-progress bead with `bd defer <id>` + `bd remember --key avoid-stuck-<id> ...` explaining what went wrong.

Do not start *new* work in this session. Only resolve the dirty state.
