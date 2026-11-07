# Backoff and failure-mode reference

When the runner returns, ralph classifies the session into a single `runner.Mode`, then maps that mode to a sleep duration (and sometimes a terminal FSM transition). This page is the source of truth for both decisions.

The two pure functions are `runner.Classify(*Session) Mode` and `backoff.Compute(Input, *BackoffConfig) time.Duration`. The loop calls them in that order each iteration.

## Failure-mode taxonomy

`runner.Mode` is one of:

| Mode               | Cause                                                                 | Terminal? | Backoff outcome |
|--------------------|-----------------------------------------------------------------------|-----------|-----------------|
| `ok`               | Exit 0 + valid JSON envelope.                                         | no        | `OKBackoff` (10s) |
| `auth`             | Stderr/envelope mentions `invalid api key`, `authentication`, `unauthorized`. | **yes** (`failed{auth}`) | `0` — loop exits |
| `budget`           | Stderr/envelope mentions `credit balance` or `insufficient credit`.   | **yes** (`failed{runner_terminal}`) | `0` — loop exits |
| `rate_limit`       | Stderr/envelope mentions `rate limit` or `too many requests`.         | no        | `Retry-After` if parsed, else exponential on `rate_limit_default` |
| `model_overloaded` | Stderr/envelope mentions `overloaded`.                                | no        | constant `rate_limit_default` |
| `oom`              | The scope ended `Result=oom-kill`, an unexplained `SIGKILL` (or exit 137 from a wrapper), or stderr mentions `out of memory` / `oom`. | no        | `oom_secs` |
| `timeout`          | The runner was killed by ralph's session timeout (`session_timeout_secs`). | no  | `timeout_secs` |
| `dead_session`     | Exit non-zero with no envelope, or envelope subtype `error_max_turns`. | no, but escalates | `unknown_secs` until threshold, then exponential |
| `unknown`          | Anything else with a non-zero exit and no signal.                     | no        | `unknown_secs` |

`Mode.Terminal()` is true only for `auth` and `budget`. `dead_session` becomes terminal once the loop has seen `dead_session_threshold` consecutive dead sessions (the loop owns the streak counter; `Classify` does not).

The terminal mapping into `fsm.Reason` happens in the loop (`classifyToReason` in `internal/loop`):

- `Mode.auth` → `fsm.ReasonAuth` → `failed{auth}`.
- `Mode.budget` → `fsm.ReasonRunnerTerminal` → `failed{runner_terminal}`. Note the distinction: `fsm.ReasonBudget` is reserved for **ralph's own** cost cap (`[budget] max_cost_usd`), not the runner's.
- `Mode.dead_session` once `streak >= dead_session_threshold` → `fsm.ReasonRunnerTerminal`.

### Diagnosing a session

Every iteration record (`summary.jsonl` row and `iter-NNNN-*.json`) that ran a session records how the runner process ended: `exit_code`, `signal` (e.g. `SIGKILL`, when a signal ended it), `oom_killed` (systemd recorded the runner's scope as `Result=oom-kill`: an OOM killer, the scope's cap or the host's, killed a process in it) and `killed_by_timeout`. Rows with any mode other than `ok` also carry `stderr_tail`, the last 1000 bytes of the runner's stderr. A `dead_session` narrative names the cause, e.g. `clean → clean: dead session (exit 1)`, and dead-streak and terminal-failure incidents repeat it. `ralph trace <iter>` shows the full record.

`oom_killed` doesn't say which limit ran out (the kernel counts any OOM kill in the victim's cgroup); the kernel log does: `Memory cgroup out of memory` for a cgroup cap, `Out of memory: Killed process` for the host (see `ralph doctor`'s memory headroom check).

### runner_terminal retries

By default `failed{runner_terminal}` ends the run. With `[loop] runner_terminal_retries = N` (and not `--once` / `--dry-run`), the loop instead absorbs a dead-session streak's `runner_terminal` up to N times in a row (a runner-side `budget` or `quota` failure, which also maps to `runner_terminal`, still ends the run: a cooldown won't refill credits):

1. The iteration routes as if the runner had not failed (to `clean`, `dirty`, or a terminal like `failed{budget}`, `done{deadline}` or `done{queue_empty}`, which then ends the run as usual) and is persisted with a narrative note such as `runner_terminal: retry 1/6 after 15m0s cooldown`.
2. The loop sleeps `runner_terminal_cooldown_secs` (default 900), cut short at `--until` or by SIGINT/SIGTERM, with a `sleeping (runner cooldown)` badge in the TUI and a warning in `orchestrator.log`.
3. The dead-session and failure streaks reset, and the next iteration starts over.

Cost, session wallclock and commit totals carry over, so `[budget]` still bounds the whole run. A healthy (`ok`) session resets the retry count. The count and the cooldown's end (`cooldown_until`) are kept in `fsm.json`, so restarting ralph neither refills the count nor skips the rest of a cooldown (a restart mid-cooldown sleeps out the remainder first); `--fresh` and the automatic reset after a `done{*}` do. `auth` is never retried. Once the retries are spent, the run ends `failed{runner_terminal}` (exit 22).

## Backoff math

`backoff.Compute` consumes `(Mode, Session, Streaks, RateLimitReset)` and returns a `time.Duration` capped at `MaxBackoff` (80 minutes). Per-mode logic:

| Mode               | Formula                                                                                                       |
|--------------------|---------------------------------------------------------------------------------------------------------------|
| `ok`               | constant `OKBackoff` (10s).                                                                                   |
| `auth`, `budget`   | `0` (terminal — caller exits).                                                                                |
| `rate_limit`       | `RateLimitReset` if non-zero (parsed via `backoff.ParseRateLimitReset`), else `base * 2^min(streak, 4)` where `base = rate_limit_default`. |
| `model_overloaded` | constant `rate_limit_default`.                                                                                |
| `oom`              | constant `oom_secs`.                                                                                          |
| `timeout`          | constant `timeout_secs`.                                                                                      |
| `dead_session`     | `unknown_secs` while `streak < dead_session_threshold`; `base * 2^min(streak - threshold, 4)` once exceeded.  |
| `unknown`          | `unknown_secs` if exit ≠ 0; otherwise `OKBackoff`.                                                            |

The exponential cap is `min(n, 4)` so `2^4 = 16` is the highest multiplier. With the default `rate_limit_default = 900s`, the raw exponential sequence is `900, 1800, 3600, 7200, 14400` seconds — and `MaxBackoff` (80 minutes = 4800s) clips the last two. Every mode's final return passes through this cap, so no single sleep exceeds 80 minutes regardless of streak length.

### Rate-limit reset parsing

Anthropic surfaces a hint like `resets 4am (UTC)` in 429 responses. `backoff.ParseRateLimitReset(stderr, now)` finds the first match of `(?i)resets\s+(\d{1,2})\s*(am|pm)\s*\(UTC\)` and returns the duration from `now` to that wall-clock instant, plus a 60-second safety buffer. Returns 0 when no hint is present so the caller falls back to exponential.

## Escalation rules

Two loop-level counters drive promotions that aren't visible from `Classify` or `Compute` alone:

- **Dirty-revert threshold** (`[backoff] dirty_revert_threshold`, default `3`): The FSM's `ConsecutiveDirty` increments on every `clean|dirty → dirty` transition and resets on `→ clean` or `→ revert`. When `ConsecutiveDirty >= threshold`, `fsm.SelectNextState` routes to `revert`. Set to `0` to disable.
- **Dead-session threshold** (`[backoff] dead_session_threshold`, default `3`): The loop tracks consecutive `Mode.dead_session` iterations. When the streak hits the threshold, `classifyToReason` returns `fsm.ReasonRunnerTerminal` so the next `SelectNextState` call routes to `failed{runner_terminal}`. The iteration that crosses the threshold also writes a `dead-streak` incident, unless terminal-failure takes priority.

## Per-iteration sleep

Backoff sleeps happen at the end of each non-terminal iteration. The loop uses an injected `Clock` so tests can drive iterations without waiting; production code sleeps on a `time.Timer` raced against `ctx.Done()` so a Ctrl-C during a long backoff returns immediately. Terminal outcomes skip the sleep — the loop exits.

The `[loop] sleep_between_secs` knob is NOT used by `backoff.Compute`. It's reserved as a baseline pause that could be folded into ralph's design later if the OK path needs to be slower than `OKBackoff`. Today, the OK path is fixed at 10 seconds for parity with the upstream Python loop.

## See also

- `internal/runner/classify.go` — the Mode taxonomy and `Classify` function.
- `internal/backoff/backoff.go` — `Compute`, `ParseRateLimitReset`, and the math constants (`MaxBackoff = 80m`, `OKBackoff = 10s`, `expCap = 4`).
- `docs/reference/config.md` — the `[backoff]`, `[budget]`, and `[loop]` knobs.
- `docs/concepts/state-machine.md` — how the FSM consumes terminal modes and the `consecutive_dirty` predicate.
