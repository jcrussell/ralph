## ralph run

Run the ralph loop in normal mode

### Synopsis

run drives the FSM-orchestrated loop against the current repo's
bd queue. The loop acquires .ralph/state/pid.lock, opens the JSONL
summary writer and orchestrator log, restores fsm.json (or starts
fresh), then runs iterations until a terminal outcome — done{*} on
graceful exit or failed{*} on budget exhaustion / auth / runner-
terminal failure. The user-facing iteration narrative is written by
the loop itself; this command adds no chatter of its own.

Display: on an interactive terminal (both stdin and stderr are TTYs)
run shows a live UI — a metrics panel (iteration, FSM state, cost vs
budget, elapsed, last gate result) above a scrollable log pane fed by
the loop's output. Keys: up/down scroll, e or tab expand/collapse the
log pane, q or Ctrl-C quit (cancel the loop, then wait for it to
unwind). Off a TTY (CI, pipes, redirects) or with --no-tui, run streams
the one-line-per-iteration narrative instead; that path is byte-
identical to the pre-TUI behavior. Either way the artifact files under
.ralph/ (summary.jsonl, the orchestrator log) stay the durable data
sink — the TUI is a view, not a record.

Exit codes: 0 on done{*}; 1 on failed{*}; non-zero infrastructure
errors (lock contention, disk full, malformed config) print to stderr
and exit 1.

```
ralph run [flags]
```

### Examples

```
  # normal autonomous run, using all .ralph/config.toml defaults
  ralph run

  # one iteration only, useful for smoke-testing prompts and hooks
  ralph run --once

  # render prompts and route states but don't invoke the runner
  ralph run --once --dry-run

  # cap iterations at 5 for this invocation without editing config.toml
  ralph run --max-iterations=5

  # disable the live TUI and stream the iteration narrative instead
  ralph run --no-tui
```

### Options

```
      --dry-run              render prompts and route states without invoking the runner
      --fresh                reset fsm.json before starting (required after failed{*}; done{*} auto-resets)
  -h, --help                 help for run
      --label string         iteration label recorded in summary.jsonl
      --max-iterations int   override [loop] max_iterations (0 = config)
      --memory string        override [loop] memory_limit_bytes ('' = config)
      --no-tui               disable the live terminal UI; stream the one-line-per-iteration narrative instead (auto-disabled off a TTY)
      --once                 run one iteration then exit
      --skip-gate            skip the per-state gate hook
      --timeout int          override [loop] session_timeout_secs (0 = config)
      --wait-on-quota        on a runner quota cap, sleep until it resets and resume instead of exiting failed (overrides [loop] wait_on_quota)
```

### Options inherited from parent commands

```
      --log-file string     append log records to this file instead of stderr
      --log-format string   log record format (text|json); default text
      --log-level string    explicit log level (warn|info|debug); overrides -v
  -v, --verbose count       increase log verbosity (-v=info, -vv=debug)
```

### SEE ALSO

* [ralph](ralph.md)	 - FSM-driven autonomous-loop CLI

