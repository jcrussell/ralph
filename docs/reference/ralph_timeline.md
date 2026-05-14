## ralph timeline

Chronological state transitions with narrative

### Synopsis

Reads .ralph/state/runs/<latest>/transitions.jsonl and prints one
line per FSM transition, joined with the narrative from summary.jsonl
on iter number.

```
ralph timeline [flags]
```

### Options

```
  -h, --help            help for timeline
      --json            emit raw transition JSONL
      --reason string   filter rows whose reason matches this string
      --since string    duration (e.g. 1h) or RFC3339 timestamp; empty = all
      --state string    filter rows whose from or to matches this state
```

### SEE ALSO

* [ralph](ralph.md)	 - FSM-driven autonomous-loop CLI

