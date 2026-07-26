## ralph logs

Stream the per-iteration log

### Synopsis

Reads .ralph/state/logs/summary.jsonl.
Default: one narrative line per iteration.
--iter N selects a single record (pretty JSON).
--tail follows appends until interrupted.
--json emits raw JSONL.
--jq filters each record with a built-in jq engine (no external jq
needed), one compact JSON result per line so it stays tail-friendly.
Because the stream is unbounded (--tail), --jq runs per record rather
than over one buffered array like 'ralph status --jq' / 'timeline --jq'.

Use 'ralph logs' when you want the per-iteration narrative stream;
use 'ralph timeline' when you want the FSM transition view joined
with narratives; use 'ralph trace <iter>' when you want every
captured artifact (prompt, stdout, stderr) for one iteration.

```
ralph logs [flags]
```

### Examples

```
  # one narrative line per iteration (default)
  ralph logs

  # follow a live run
  ralph logs --tail

  # pretty-print iter 42 in full
  ralph logs --iter=42

  # raw JSONL for scripting
  ralph logs --json

  # built-in jq: only dirty records (works live with --tail)
  ralph logs --jq 'select(.state=="dirty")'
```

### Options

```
  -h, --help        help for logs
      --iter int    only print the record with this iter
      --jq string   filter each record with a jq expression (compact JSON per line)
      --json        emit raw JSONL
      --tail        follow new records as they're appended
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

