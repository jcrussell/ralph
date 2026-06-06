## ralph hook run

Run a hook manually with the standard environment

### Synopsis

<path> is relative to .ralph/hooks/ or absolute. The resolved path
must live under <repo>/.ralph/hooks/. The hook's exit code propagates
to ralph's exit code; stdout and stderr are passed through.

RALPH_REPO, RALPH_STATE, RALPH_ITER, and the notify-hook summary vars
(RALPH_REASON, RALPH_COST_USD, RALPH_DURATION_SECS) are populated from
fsm.json by default; override RALPH_STATE and the transition states with
--state, --prev-state, --next-state, or any var with --env KEY=VALUE to
reproduce a specific iteration's environment.

```
ralph hook run <path> [flags]
```

### Examples

```
  # run the global pre-iteration hook with current fsm.json state
  ralph hook run pre-iteration

  # test the terminal notify hook with the last run's outcome
  ralph hook run notify

  # simulate the clean → dirty transition for the dirty/enter hook
  ralph hook run dirty/enter --state=dirty --prev-state=clean

  # inject extra env vars for an ad-hoc hook
  ralph hook run my-debug-hook --env=DEBUG=1 --env=DRY_RUN=1
```

### Options

```
      --env strings         extra KEY=VALUE pairs (repeatable)
  -h, --help                help for run
      --next-state string   value for RALPH_NEXT_STATE
      --prev-state string   value for RALPH_PREV_STATE
      --state string        value for RALPH_STATE (default: from fsm.json)
```

### Options inherited from parent commands

```
      --log-file string     append log records to this file instead of stderr
      --log-format string   log record format (text|json); default text
      --log-level string    explicit log level (warn|info|debug); overrides -v
  -v, --verbose count       increase log verbosity (-v=info, -vv=debug)
```

### SEE ALSO

* [ralph hook](ralph_hook.md)	 - Inspect and run ralph hooks

