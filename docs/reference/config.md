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
| `max_iterations`         | int     | `30`    | Hard cap on iterations. `0` = unlimited (not recommended without a budget cap). When `fsm.Iter >= max_iterations`, the FSM routes to `done{iter_cap}`. CLI overrides (`run --max-iterations`, `run --unlimited`, `review --max-rounds`) apply to one invocation only; the effective value is recorded on the run manifest so `ralph status` reports the cap the loop is actually running under rather than the one on disk. |
| `session_timeout_secs`   | int     | `3600`  | Per-iteration runner timeout. ralph kills the runner when exceeded; the iteration is classified as `timeout`. |
| `memory_limit_bytes`     | string  | `"7G"`  | systemd-run memory cap for the runner cgroup. Suffixes `K`/`M`/`G`/`T` use 1024 multipliers; a trailing `B` is accepted. Empty or `"0"` disables the cap. Leave at least 1G under the host's RAM (and any `MemoryMax` on your user slice); `ralph doctor` warns when it doesn't. |
| `sleep_between_secs`     | int     | `5`     | Sleep between iterations. Backoff modes (rate-limit, OOM, timeout) override this with their own durations. |
| `max_noop_iters`         | int     | `10`    | Exit `done{idle}` after this many consecutive no-op iterations (runner OK, zero commits, no bd diff, state unchanged). Guards against an endless spin when `bd ready` keeps reporting items the agent can't action. `0` disables the cap. |
| `wait_on_quota`          | bool    | `false` | When `true`, a runner quota cap (the resettable 5-hour/weekly/monthly window, mode `quota`) waits (see `quota_wait_secs`) and resumes the same state instead of exiting `failed{runner_terminal}`. Default `false` keeps quota terminal (fail fast). The `--wait-on-quota` flag enables it for one invocation. |
| `quota_wait_secs`        | int     | `1800`  | Ceiling of the exponential backoff between quota retries when `wait_on_quota` is set and the error carries no usable reset hint (itself capped at the backoff `MaxBackoff` of 80 min). Waits start at 5 min and double per consecutive quota wait up to this ceiling; any non-quota iteration resets the streak. When the envelope surfaces a `resets 10:30pm (UTC)` instant still ahead, the loop sleeps until then instead (capped at `MaxQuotaWait`, 6 h), ignoring this interval. A hint naming an instant up to 6 h in the past (the runner repeating a reset that has just gone by) is stale and takes the backoff, rather than being read as the next day's. The sleep is interruptible by SIGINT/SIGTERM. While waiting, the live `ralph run` header shows a `sleeping (quota)` badge counting down to the resume. |
| `wait_for_beads`         | bool    | `false` | When `true`, `done{idle}` and `done{queue_empty}` park instead of exiting: the loop polls `bd ready` every `bead_poll_secs` and resumes when the set of ready issues changes. The `--wait-for-beads` flag enables it for one invocation. |
| `bead_poll_secs`         | int     | `60`    | Poll interval while parked on `wait_for_beads`. |
| `max_bead_wait_secs`     | int     | `0`     | Bound on one park; `0` parks indefinitely (or until `--until`). |
| `runner_terminal_retries` | int    | `0`     | How many times in a row a run that would end `failed{runner_terminal}` on a dead-session streak instead cools down and carries on (runner `budget`/`quota` failures are not retried). Reset by a healthy session. `0` keeps it terminal. See [backoff](backoff.md#runner_terminal-retries). |
| `runner_terminal_cooldown_secs` | int | `900` | Sleep before each such retry. |

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

## `[ui]`

Presentation knobs for the live `ralph run` TUI. They affect only what the
operator sees, never orchestration.

| Key                    | Type   | Default   | Meaning |
|------------------------|--------|-----------|---------|
| `log_scrollback_lines` | int    | `50000`   | Max lines retained in the live log pane's in-memory scrollback ring. Must be `> 0`. The pane keeps the most recent lines up to this cap, evicting oldest-first. |
| `log_scrollback_bytes` | string | `"16M"`   | Byte cap on the same ring, parsed like `memory_limit_bytes` (suffixes `K`/`M`/`G`/`T`, 1024 multipliers; trailing `B` accepted). Must resolve to `> 0`. The pane retains lines up to **both** caps, so whichever is hit first bounds scrollback. |

Raising these lets a long run page further back at the cost of more memory; the
ring is bounded by whichever cap is reached first, so memory stays predictable.

## Live TUI (`ralph run`)

The interactive terminal UI's **activation** is not a config key — there is no
`config.toml` toggle to turn it on or off. It is gated entirely on the terminal
and one flag (below), so activation stays out of the config layers above. Its
log-pane scrollback caps *are* configurable via the `[ui]` section above.

**Activation.** `ralph run` shows the live UI automatically when **both** stdin
and stderr are TTYs. Off a TTY — CI, pipes, output redirects — it falls back to
the synchronous one-line-per-iteration narrative. There is no way to force the
UI on when stderr is not a terminal.

**`--no-tui`.** Pass `--no-tui` to disable the UI for one invocation even on a
TTY; run then streams the narrative instead. This flag is the only switch — the
off-TTY fallback and the `--no-tui` path are the same code path, and it is
byte-identical to the pre-TUI behavior.

**Layout.** Three stacked blocks sit above a scrollable log pane, each delimited
by a horizontal rule:

1. **Header** — `ralph <version>  ·  <cwd>`, plus a colored terminal-state badge
   once the run finishes (or a `sleeping (quota)` countdown badge while waiting).
2. **Cumulative block** — whole-run totals: iteration N/max, cumulative cost vs
   budget cap, elapsed and wallclock vs cap, the run-total commit count, and the
   ready-queue depth (`N ready`). These live on the persisted FSM (or the run
   clock), so they hold steady across a quota wait rather than reading as a reset.
3. **Session block** — the current/last iteration: FSM state, last gate result,
   this-iteration runner mode/cost/duration/commits, a consecutive-dirty warning,
   any bead deltas, and the one-line narrative. The per-iteration cost/commits
   here legitimately show `0` during a quota wait (the surviving totals are in the
   cumulative block). This block is hidden before the first iteration completes.

The log pane follows the tail unless you scroll up; its scrollback depth is
bounded by the `[ui]` caps above. On a window too small to split, the layout
degrades to the metric blocks clipped to fit. Color honors `NO_COLOR` and the
stderr TTY.

**Keys.**

| Key             | Action |
|-----------------|--------|
| `↑` / `↓`       | Scroll the log pane. |
| `e` or `Tab`    | Expand / collapse the log pane (hides both metric blocks for full-height logs). |
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
