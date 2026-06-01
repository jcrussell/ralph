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
| `wait_on_quota`          | bool    | `false` | When `true`, a runner quota cap (the resettable 5-hour/weekly/monthly window, mode `quota`) sleeps `quota_wait_secs` and resumes the same state instead of exiting `failed{runner_terminal}`. Default `false` keeps quota terminal (fail fast). The `--wait-on-quota` flag enables it for one invocation. |
| `quota_wait_secs`        | int     | `1800`  | Bounded sleep between quota retries when `wait_on_quota` is set. The upstream reset instant isn't in the error envelope, so this is a fixed poll interval (capped at the backoff `MaxBackoff` of 80 min). The sleep is interruptible by SIGINT/SIGTERM. |

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

## Live TUI (`ralph run`)

The interactive terminal UI is not a config key — it has no `[tui]` section and
no `config.toml` toggle. It is gated entirely on the terminal and one flag, so
it stays out of the config layers above.

**Activation.** `ralph run` shows the live UI automatically when **both** stdin
and stderr are TTYs. Off a TTY — CI, pipes, output redirects — it falls back to
the synchronous one-line-per-iteration narrative. There is no way to force the
UI on when stderr is not a terminal.

**`--no-tui`.** Pass `--no-tui` to disable the UI for one invocation even on a
TTY; run then streams the narrative instead. This flag is the only switch — the
off-TTY fallback and the `--no-tui` path are the same code path, and it is
byte-identical to the pre-TUI behavior.

**Layout.** A metrics panel (iteration N/max, FSM state, cumulative cost vs
budget cap, elapsed vs wallclock cap, last gate result, consecutive-dirty
count, last-iteration cost/duration/commits) sits above a scrollable log pane
fed by the loop's interleaved output. The pane follows the tail unless you
scroll up. On a window too small to split, the layout degrades to a clipped
metrics block. Color honors `NO_COLOR` and the stderr TTY.

**Keys.**

| Key             | Action |
|-----------------|--------|
| `↑` / `↓`       | Scroll the log pane. |
| `e` or `Tab`    | Expand / collapse the log pane (hides the metrics panel for full-height logs). |
| `q` or `Ctrl-C` | Quit: cancel the loop, wait for the current iteration to unwind, then exit. |

**Files are the durable data sink.** The TUI is a view, not a record. The
metrics and log lines it shows are also written to the artifact files under
`.ralph/` (`summary.jsonl`, the orchestrator log) regardless of whether the UI
is active. Nothing is lost by running `--no-tui` or off a TTY; inspect the
files (or `ralph logs` / `ralph status` / `ralph timeline`) for the durable
history.

## See also

- `internal/config/config.go` — the Go struct definitions and the layered load.
- `pkg/cmd/initcmd/defaults/config.toml` — the scaffolded template (kept in sync with `Defaults()` by `TestShippedConfigTomlLoadsToDefaults`).
