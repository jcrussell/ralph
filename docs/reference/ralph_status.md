## ralph status

Show ralph's current FSM state, counters, and recent transitions

### Synopsis

status reads .ralph/state/fsm.json plus the latest run's
transitions.jsonl and renders a single-screen dashboard:

  - current state (and reason for terminal states)
  - iteration counter vs. cap
  - review mode + branch/base
  - cumulative cost and wallclock against their caps
  - consecutive dirty streak
  - the last N transitions (default 5)

Pass --json with a comma-separated field list to emit machine-readable
output (the flag help lists the available fields). --jq filters that JSON
with a built-in jq engine (no external jq needed); --template formats it
with a Go template.

```
ralph status [flags]
```

### Examples

```
  # human-readable (default)
  ralph status

  # JSON with the fields you want
  ralph status --json state,iter

  # built-in jq (no external jq binary required)
  ralph status --json state --jq '.state'

  # last 20 transitions
  ralph status --tail=20
```

### Options

```
  -h, --help              help for status
      --jq string         filter --json output with a jq expression
      --json string       output JSON with the given comma-separated fields (available: run_id,state,reason,iter,max_iterations,review_mode,review_branch,review_base,cumulative_cost_usd,max_cost_usd,cumulative_wallclock_secs,max_wallclock_secs,consecutive_dirty,last_gate_result,transitions)
      --tail int          number of recent transitions to render (default 5)
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

