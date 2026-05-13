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

Pass --json to emit the same data in machine-readable form. The JSON
shape is stable enough to script against; treat unknown fields as
informational.

```
ralph status [flags]
```

### Examples

```
  # human-readable (default)
  ralph status

  # JSON for scripts
  ralph status --json | jq '.state, .iter'

  # last 20 transitions
  ralph status --tail=20
```

### Options

```
  -h, --help       help for status
      --json       emit machine-readable JSON
      --tail int   number of recent transitions to render (default 5)
```

### SEE ALSO

* [ralph](ralph.md)	 - FSM-driven autonomous-loop CLI

