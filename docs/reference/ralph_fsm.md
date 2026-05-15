## ralph fsm

Inspect the orchestrator state machine

### Synopsis

fsm has two subcommands:

  ralph fsm show    pretty-print .ralph/state/fsm.json (default)
  ralph fsm graph   render the FSM topology as a Mermaid diagram,
                    with edge counts tallied from transitions.jsonl

Without a subcommand, "show" runs.

```
ralph fsm [flags]
```

### Options

```
  -h, --help   help for fsm
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
* [ralph fsm graph](ralph_fsm_graph.md)	 - Render the FSM topology as a Mermaid diagram with edge counts
* [ralph fsm show](ralph_fsm_show.md)	 - Pretty-print .ralph/state/fsm.json

