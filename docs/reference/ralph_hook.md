## ralph hook

Inspect and run ralph hooks

### Synopsis

Hooks are git-style executable scripts under .ralph/hooks/ that
ralph invokes at well-known points (pre-iteration, post-iteration,
failure, and per-state enter/exit/gate). The hook subcommand is for
running them manually with the documented environment so you can
debug them outside the loop.

### Options

```
  -h, --help   help for hook
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
* [ralph hook run](ralph_hook_run.md)	 - Run a hook manually with the standard environment

