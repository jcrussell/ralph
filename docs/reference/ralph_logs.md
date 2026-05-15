## ralph logs

Stream the per-iteration log

### Synopsis

Reads .ralph/state/logs/summary.jsonl.
Default: one narrative line per iteration.
--iter N selects a single record (pretty JSON).
--tail follows appends until interrupted.
--json emits raw JSONL.

```
ralph logs [flags]
```

### Options

```
  -h, --help       help for logs
      --iter int   only print the record with this iter
      --json       emit raw JSONL
      --tail       follow new records as they're appended
```

### Options inherited from parent commands

```
      --log-level string   explicit log level (warn|info|debug); overrides -v
  -v, --verbose count      increase log verbosity (-v=info, -vv=debug)
```

### SEE ALSO

* [ralph](ralph.md)	 - FSM-driven autonomous-loop CLI

