# Review state

You are reviewing branch `{{.Review.Branch}}` against base `{{.Review.Base}}`. Findings are tracked as bd tasks labeled `review:{{.Review.Branch}}`.

Current state of the world:
- Open findings: `{{.Review.OpenFindings}}`
- Last gate result: `{{.GateResult}}`
- Working tree: {{if .GitDirty}}**dirty**{{else}}clean{{end}}

## Decide what to do this iteration

Pick exactly one of:

1. **Ingest** — if no `review:{{.Review.Branch}}` findings exist *yet at the current HEAD*, read `git diff {{.Review.Base}}..HEAD` and file ONE finding per real issue you spot with `bd create -t task -l review:{{.Review.Branch}} "..."`. Do not file findings for style nits — only for things that should block merge. Then exit.

2. **Fix** — if open findings exist, claim the highest-priority one with `bd update <id> --claim`, fix it, commit on this branch, close with `bd close <id> --reason "..."`. Then exit.

3. **Address gate failure** — if no findings remain but `{{.GateResult}}` is `failed`, file ONE finding describing the gate output as a new `review:{{.Review.Branch}}` task. Then exit.

4. **Merge** — if no findings remain, the gate is `passed`, and the working tree is clean, file the merge request: `bd create -t task -l "merge:{{.Review.Branch}}" --deps <closed-finding-ids> "Merge {{.Review.Branch}} into {{.Review.Base}}"`. Include a one-paragraph summary of the closed findings in `--description`. Then exit. The orchestrator will detect the empty review queue and stop.

Do not interleave these — one decision per iteration.
