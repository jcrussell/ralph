## ralph report

Activity summary (markdown or JSON) over a time window

### Synopsis

report aggregates orchestrator activity over a time window: bd issues
closed/created/reopened/deferred, commits, recent incidents, FSM state
distribution, and aggregate cost / wallclock / iteration count. Inputs are
summary.jsonl, run manifests under .ralph/state/runs/, incident files under
.ralph/state/incidents/, and 'git log'.

Default output is markdown — pipe to a markdown viewer or commit it to a
journal. --section=work_done,commits,... limits the markdown to specific
sections. --json takes a comma-separated list of sections (the flag help
lists them) and emits them as one JSON object (sections are keys), which
--jq / --template post-process with a built-in engine (no external jq
needed). --since takes a Go duration (24h, 7d → 168h) or an RFC3339 timestamp.

```
ralph report [flags]
```

### Examples

```
  # last 24h markdown (default)
  ralph report

  # only the commits and incidents sections
  ralph report --section=commits,incidents

  # JSON for specific sections
  ralph report --json work_done,commits,cost

  # how many commits has ralph made in the last week?
  ralph report --since=168h --json commits --jq '.commits | length'

  # just the incidents, as JSON
  ralph report --json incidents --jq '.incidents'

  # commit a daily report to a journal
  ralph report > journal/$(date -I).md
```

### Options

```
  -h, --help              help for report
      --jq string         filter --json output with a jq expression
      --json string       output JSON with the given comma-separated fields (available: since,work_done,commits,incidents,state_distribution,cost)
      --section strings   limit markdown to these sections (work_done,commits,incidents,state_distribution,cost)
      --since string      duration (e.g. 24h) or RFC3339 timestamp (default "24h")
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

