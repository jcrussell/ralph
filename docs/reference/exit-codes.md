# Exit codes

`ralph run` and `ralph review` exit with a code that names how the loop
ended, so a supervisor (a systemd unit, a CI job, a shell loop) can act on
the outcome without parsing output. The table is a stable contract: codes
are only ever added, never renumbered. Source: `fsm.Outcome.ExitCode`
(`internal/fsm/states.go`) and `pkg/cmd/loopexit`.

| Code | Meaning | Typical supervisor action |
|------|---------|---------------------------|
| 0    | `done{queue_empty}`: no ready work. Also `--once` and `--dry-run`, which stop on a non-terminal state. | stop |
| 1    | Infrastructure error: lock held by another ralph, disk full, malformed config, a hook or bd failure. Prints `error: ...`. | restart after a delay (the run resumes with its totals) |
| 2    | Usage error (bad flag, unknown command). | stop; fix the invocation |
| 3    | Refused to resume: `fsm.json` is in `failed{*}` and `--fresh` was not passed. | stop; inspect, then `--fresh` |
| 10   | `done{idle}`: `max_noop_iters` iterations in a row did nothing. | stop |
| 11   | `done{iter_cap}`: hit `max_iterations`. | stop (a restart begins a fresh run, zeroing the totals `[budget]` counts) |
| 12   | `done{deadline}`: the `--until` time passed (including one already past at start). | stop |
| 20   | `failed{budget}`: ralph's own `[budget]` cap (cost or session wallclock). | stop |
| 21   | `failed{auth}`: the runner is not logged in. | stop; log the runner in |
| 22   | `failed{runner_terminal}`: the runner failed terminally (a dead-session streak, a runner-side credit or quota error). | stop, or cool down and restart with `--fresh` |
| 130  | Interrupted by SIGINT. | — |
| 143  | Interrupted by SIGTERM (e.g. `systemctl stop`). ralph unwinds the current iteration first. | — |

## The closing line

Whenever the loop reaches an outcome (every row above except 1-3 and the
signals), the last thing ralph writes to stderr is one line naming it:

```
ralph: run ended: failed{runner_terminal} (exit 22)
```

Under `--log-format json` it is one JSON object instead, shaped like a slog
record:

```json
{"time":"2026-11-07T21:04:05.123Z","level":"INFO","msg":"run ended","state":"failed","reason":"runner_terminal","exit_code":22,"iter":41,"cost_usd":12.5,"run_id":"20261107T190000Z"}
```

`iter` and `cost_usd` are the run's totals from `fsm.json`; `reason` is
empty for a non-terminal stop.

## Reading the outcome later

`ralph status --json state,reason` always includes `reason` (empty when the
fsm is not in a terminal state). For a bare value in shell, use a template:
`ralph status --json state,reason --template '{{.reason}}'` (`--jq` prints JSON
values, so strings come out quoted). The run manifest's `exit_outcome` and
the last `summary.jsonl` row carry the outcome too.
