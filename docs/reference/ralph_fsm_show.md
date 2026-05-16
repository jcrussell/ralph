## ralph fsm show

Pretty-print .ralph/state/fsm.json

### Synopsis

show reads .ralph/state/fsm.json — the orchestrator's persisted
state — and prints it as indented JSON. Use this when you want the
full record (every counter, every cumulative total); use 'ralph
status' instead when you just want the dashboard view.

--json is a no-op for show: the output is JSON either way. The
flag is kept for consistency with other commands that gate JSON
output.

```
ralph fsm show [flags]
```

### Examples

```
  # pretty-print fsm.json
  ralph fsm show

  # pipe to jq for ad-hoc queries
  ralph fsm show | jq '.cumulative_cost_usd, .iter'
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

