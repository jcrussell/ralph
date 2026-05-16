## ralph timeline

Chronological state transitions with narrative

### Synopsis

Reads .ralph/state/runs/<latest>/transitions.jsonl and prints one
line per FSM transition, joined with the narrative from summary.jsonl
on iter number.

Use 'ralph timeline' when you want the chronological FSM view; use
'ralph logs' when you want only the iteration narrative; use 'ralph
status' for the single-screen dashboard. --since takes a Go duration
(1h, 30m) or an RFC3339 timestamp; --state and --reason filter rows.

```
ralph timeline [flags]
```

### Examples

```
  # all transitions for the latest run
  ralph timeline

  # only the last hour
  ralph timeline --since=1h

  # only failed-state transitions
  ralph timeline --state=failed

  # raw JSONL for scripting
  ralph timeline --json | jq 'select(.to=="dirty")'
```

### Options

```
  -h, --help            help for timeline
      --json            emit raw transition JSONL
      --reason string   filter rows whose reason matches this string
      --since string    duration (e.g. 1h) or RFC3339 timestamp; empty = all
      --state string    filter rows whose from or to matches this state
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

