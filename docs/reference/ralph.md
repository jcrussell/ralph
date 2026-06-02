## ralph

FSM-driven autonomous-loop CLI

### Synopsis

ralph runs an AI coding agent in a loop, routed by a built-in
state machine. Each iteration ends in clean, dirty, revert, or one
of the terminal done{*}/failed{*} states; the next prompt is chosen
from the resulting state.

See docs/concepts/ralph-fsm.md for the state diagram and
docs/concepts/configuration.md for the .ralph/config.toml schema.

### Examples

```
  # one-time setup
  ralph doctor && ralph init

  # autonomous run against the bd queue
  ralph run

  # review an open PR
  ralph review --pr=123

  # mid-run observability
  ralph status
  ralph logs --tail
```

### Options

```
  -h, --help                help for ralph
      --log-file string     append log records to this file instead of stderr
      --log-format string   log record format (text|json); default text
      --log-level string    explicit log level (warn|info|debug); overrides -v
  -v, --verbose count       increase log verbosity (-v=info, -vv=debug)
      --version             version for ralph
```

### SEE ALSO

* [ralph doctor](ralph_doctor.md)	 - Check ralph's runtime environment
* [ralph explore](ralph_explore.md)	 - Browse runs, incidents, and iteration artifacts under .ralph/state
* [ralph fsm](ralph_fsm.md)	 - Inspect the orchestrator state machine
* [ralph gc](ralph_gc.md)	 - Prune old runs, iteration artifacts, and incidents from .ralph/state
* [ralph hook](ralph_hook.md)	 - Inspect and run ralph hooks
* [ralph init](ralph_init.md)	 - Scaffold .ralph/ in the current repo
* [ralph logs](ralph_logs.md)	 - Stream the per-iteration log
* [ralph prompt](ralph_prompt.md)	 - Inspect ralph prompts
* [ralph report](ralph_report.md)	 - Markdown summary of orchestrator activity
* [ralph review](ralph_review.md)	 - Run the ralph loop in review mode against a branch
* [ralph run](ralph_run.md)	 - Run the ralph loop in normal mode
* [ralph status](ralph_status.md)	 - Show ralph's current FSM state, counters, and recent transitions
* [ralph timeline](ralph_timeline.md)	 - Chronological state transitions with narrative
* [ralph trace](ralph_trace.md)	 - Show every captured artifact for a single iteration
* [ralph version](ralph_version.md)	 - Print ralph version, commit, and build info

