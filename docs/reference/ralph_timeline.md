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

--json with a comma-separated field list emits the filtered transitions as
a JSON array; --jq filters that array with a built-in jq engine (no external
jq needed) and --template formats it with a Go template.

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

  # JSON array of transitions
  ralph timeline --json iter,ts,from,to,reason

  # built-in jq: only transitions into dirty
  ralph timeline --json to,iter --jq '.[] | select(.to=="dirty")'
```

### Options

```
  -h, --help              help for timeline
      --jq string         filter --json output with a jq expression
      --json string       output JSON with the given comma-separated fields (available: ts,iter,from,to,reason,runner_mode,gate_result,cost_usd_delta)
      --reason string     filter rows whose reason matches this string
      --since string      duration (e.g. 1h) or RFC3339 timestamp; empty = all
      --state string      filter rows whose from or to matches this state
      --template string   format --json output with a Go template
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

