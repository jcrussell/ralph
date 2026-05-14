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
| `oom`              | Exit code 137, or stderr mentions `out of memory` / `oom`.            | no        | `oom_secs` |
| `timeout`          | The runner was killed by ralph's session timeout (`session_timeout_secs`). | no  | `timeout_secs` |
| `dead_session`     | Exit non-zero with no envelope, or envelope subtype `error_max_turns`. | no, but escalates | `unknown_secs` until threshold, then exponential |
| `unknown`          | Anything else with a non-zero exit and no signal.                     | no        | `unknown_secs` |

`Mode.Terminal()` is true only for `auth` and `budget`. `dead_session` becomes terminal once the loop has seen `dead_session_threshold` consecutive dead sessions (the loop owns the streak counter; `Classify` does not).

The terminal mapping into `fsm.Reason` happens in the loop (`classifyToReason` in `internal/loop`):

- `Mode.auth` → `fsm.ReasonAuth` → `failed{auth}`.
- `Mode.budget` → `fsm.ReasonRunnerTerminal` → `failed{runner_terminal}`. Note the distinction: `fsm.ReasonBudget` is reserved for **ralph's own** cost cap (`[budget] max_cost_usd`), not the runner's.
- `Mode.dead_session` once `streak >= dead_session_threshold` → `fsm.ReasonRunnerTerminal`.

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

Backoff sleeps happen at the end of each non-terminal iteration. The loop uses an injected `Clock` so tests can drive iterations without waiting; production code uses `time.Sleep`. Terminal outcomes skip the sleep — the loop exits.

The `[loop] sleep_between_secs` knob is NOT used by `backoff.Compute`. It's reserved as a baseline pause that could be folded into ralph's design later if the OK path needs to be slower than `OKBackoff`. Today, the OK path is fixed at 10 seconds for parity with the upstream Python loop.

## See also

- `internal/runner/classify.go` — the Mode taxonomy and `Classify` function.
- `internal/backoff/backoff.go` — `Compute`, `ParseRateLimitReset`, and the math constants (`MaxBackoff = 80m`, `OKBackoff = 10s`, `expCap = 4`).
- `docs/reference/config.md` — the `[backoff]`, `[budget]`, and `[loop]` knobs.
- `docs/concepts/state-machine.md` — how the FSM consumes terminal modes and the `consecutive_dirty` predicate.
