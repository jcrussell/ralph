# Configuration reference

Ralph reads three configuration layers, later wins:

1. Built-in defaults (`internal/config.Defaults()`).
2. User config at `$XDG_CONFIG_HOME/ralph/config.toml` (defaults to `~/.config/ralph/config.toml`).
3. Repo config at `<repo>/.ralph/config.toml`.

Any key you omit falls through to the layer below; an empty file = use all defaults. A malformed TOML file is a startup error.

The repo-level file is scaffolded by `ralph init` with every section present and every key commented out, ready to uncomment.

## `[loop]`

| Key                      | Type    | Default | Meaning |
|--------------------------|---------|---------|---------|
| `max_iterations`         | int     | `30`    | Hard cap on iterations. `0` = unlimited (not recommended without a budget cap). When `fsm.Iter >= max_iterations`, the FSM routes to `done{iter_cap}`. |
| `session_timeout_secs`   | int     | `3600`  | Per-iteration runner timeout. ralph kills the runner when exceeded; the iteration is classified as `timeout`. |
| `memory_limit_bytes`     | string  | `"7G"`  | systemd-run memory cap for the runner cgroup. Suffixes `K`/`M`/`G`/`T` use 1024 multipliers; a trailing `B` is accepted. Empty or `"0"` disables the cap. |
| `sleep_between_secs`     | int     | `5`     | Sleep between iterations. Backoff modes (rate-limit, OOM, timeout) override this with their own durations. |

## `[runner]`

| Key       | Type     | Default                                                | Meaning |
|-----------|----------|--------------------------------------------------------|---------|
| `command` | string   | `"claude"`                                             | Binary to run. Looked up on PATH if not absolute. |
| `args`    | []string | `["--dangerously-skip-permissions", "--output-format=json"]` | Args prepended to every invocation. The JSON-output flag is load-bearing — classifier reads `total_cost_usd`, `subtype`, `api_error_status` from the parsed envelope. |
| `model`   | string   | `"opus"`                                               | When set, appended as `--model <model>` after `args`, so explicit `args` take precedence. Empty omits the flag entirely. |

## `[gate]`

| Key            | Type   | Default          | Meaning |
|----------------|--------|------------------|---------|
| `timeout_secs` | int    | `600`            | Per-invocation timeout for `states/<state>/gate` hooks. |
| `soft_fail`    | bool   | `true`           | When `true`, a non-zero gate exit is recorded in `fsm.LastGateResult` but the FSM keeps routing normally. When `false`, gate failure becomes a runner-terminal signal. |
| `run_when`     | string | `"commits-only"` | When to invoke the gate. `"every"` (every iteration), `"commits-only"` (only when the iteration produced a commit), `"never"` (skip). |

## `[backoff]`

Per-mode sleep between iterations, in seconds. The classifier's `Mode` selects which value applies.

| Key                      | Type | Default | Meaning |
|--------------------------|------|---------|---------|
| `dirty_revert_threshold` | int  | `3`     | Auto-revert when `fsm.ConsecutiveDirty >= threshold`. `0` disables. |
| `dead_session_threshold` | int  | `3`     | Promote `ModeDeadSession` to `failed{runner_terminal}` after this many consecutive dead-session iterations. |
| `unknown_secs`           | int  | `30`    | Sleep after `ModeUnknown`. |
| `oom_secs`               | int  | `120`   | Sleep after `ModeOOM`. |
| `timeout_secs`           | int  | `60`    | Sleep after `ModeTimeout`. |
| `rate_limit_default`     | int  | `900`   | Sleep after `ModeRateLimit` when no `Retry-After` was given. |

## `[budget]`

Hard caps that force `failed{budget}`. `0` = unlimited.

| Key                  | Type    | Default | Meaning |
|----------------------|---------|---------|---------|
| `max_cost_usd`       | float64 | `0`     | Cumulative cost across all iterations of the run. |
| `max_wallclock_secs` | int     | `0`     | Cumulative wallclock across all iterations of the run. |

## `[review]`

| Key           | Type   | Default  | Meaning |
|---------------|--------|----------|---------|
| `base_branch` | string | `"main"` | Base branch for review-mode merge-filing flows. Used by `internal/git.ResolveBase` when nothing else is specified. |

## See also

- `internal/config/config.go` — the Go struct definitions and the layered load.
- `pkg/cmd/initcmd/defaults/config.toml` — the scaffolded template (kept in sync with `Defaults()` by `TestShippedConfigTomlLoadsToDefaults`).
