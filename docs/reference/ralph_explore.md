## ralph explore

Browse runs, incidents, and iteration artifacts under .ralph/state

### Synopsis

explore opens a read-only browser over .ralph/state with three
categories — Runs, Incidents, and Iterations — plus a Search entry. Pick a
category to list its items, then open an item to see its detail: a run's
transitions, an incident's report, or an iteration's full artifact dump.

It is interactive: arrow keys move, enter opens, esc goes back, and q quits
(with a y/N confirmation). tab/shift+tab switch category in a list and flip
between items in a detail view; / filters a list; the Search entry runs a
full-text search across everything, and / inside a detail finds within it
(n/N jump between matches); r re-reads .ralph/state. Off a TTY (or with
--no-tui) it prints a plain listing of the categories instead; for scripted
detail use 'ralph trace', 'ralph logs', or 'ralph status'.

```
ralph explore [flags]
```

### Examples

```
  # browse interactively
  ralph explore

  # plain listing (pipeable)
  ralph explore --no-tui
```

### Options

```
  -h, --help     help for explore
      --no-tui   print a plain listing instead of the interactive browser (auto-enabled off a TTY)
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

