## ralph hook run

Run a hook manually with the standard environment

### Synopsis

<path> is relative to .ralph/hooks/ or absolute. The resolved path
must live under <repo>/.ralph/hooks/. The hook's exit code propagates
to ralph's exit code; stdout and stderr are passed through.

```
ralph hook run <path> [flags]
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
      --log-level string   explicit log level (warn|info|debug); overrides -v
  -v, --verbose count      increase log verbosity (-v=info, -vv=debug)
```

### SEE ALSO

* [ralph hook](ralph_hook.md)	 - Inspect and run ralph hooks

