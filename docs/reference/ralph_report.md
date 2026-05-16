## ralph report

Markdown summary of orchestrator activity

### Synopsis

report renders a human-readable markdown summary over a time
window: bd issues closed/created/reopened/deferred, commits, recent
incidents, FSM state distribution, and aggregate cost / wallclock /
iteration count. Inputs are summary.jsonl, run manifests under
.ralph/state/runs/, incident files under .ralph/state/incidents/,
and 'git log'.

Use this for end-of-day or end-of-week briefings — pipe to your
favorite markdown viewer or commit it to a journal. --since takes
a Go duration (24h, 7d → use 168h) or an RFC3339 timestamp.

```
ralph report [flags]
```

### Examples

```
  # last 24h (default)
  ralph report

  # last week
  ralph report --since=168h

  # since a specific timestamp
  ralph report --since=2026-05-12T00:00:00Z

  # commit a daily report to a journal
  ralph report > journal/$(date -I).md
```

### Options

```
  -h, --help           help for report
      --since string   duration (e.g. 24h) or RFC3339 timestamp (default "24h")
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

