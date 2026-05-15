## ralph fsm graph

Render the FSM topology as a Mermaid diagram with edge counts

### Synopsis

graph prints a Mermaid stateDiagram with every state defined in
internal/fsm and every transition observed in transitions.jsonl.

Edge labels carry the observed count; the current state (from
fsm.json) is highlighted with the "current" classDef.

--run selects the source for edge counts:
  latest  the most recent run (default)
  all     aggregate every run under state/runs/
  <id>    a specific run id (e.g. 20260513T180405Z)

Use --no-counts to render bare topology.

```
ralph fsm graph [flags]
```

### Examples

```
  # default: latest run, with counts
  ralph fsm graph

  # all runs aggregated
  ralph fsm graph --run=all

  # bare topology
  ralph fsm graph --no-counts
```

### Options

```
  -h, --help         help for graph
      --no-counts    omit edge counts
      --run string   source run for edge counts: latest|all|<id> (default "latest")
```

### Options inherited from parent commands

```
      --log-level string   explicit log level (warn|info|debug); overrides -v
  -v, --verbose count      increase log verbosity (-v=info, -vv=debug)
```

### SEE ALSO

* [ralph fsm](ralph_fsm.md)	 - Inspect the orchestrator state machine

