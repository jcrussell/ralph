## ralph fsm show

Pretty-print .ralph/state/fsm.json

```
ralph fsm show [flags]
```

### Examples

```
  ralph fsm show
  ralph fsm show --json | jq .
```

### Options

```
  -h, --help   help for show
      --json   emit raw fsm.json
```

### Options inherited from parent commands

```
      --log-file string     append log records to this file instead of stderr
      --log-format string   log record format (text|json); default text
      --log-level string    explicit log level (warn|info|debug); overrides -v
  -v, --verbose count       increase log verbosity (-v=info, -vv=debug)
```

### SEE ALSO

* [ralph fsm](ralph_fsm.md)	 - Inspect the orchestrator state machine

