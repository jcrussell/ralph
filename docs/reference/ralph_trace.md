## ralph trace

Show every captured artifact for a single iteration

### Synopsis

trace prints the full forensic record for one iteration: the
JSON iter record, the rendered prompt, and the runner's stdout/
stderr (tailed by default). All four come from .ralph/state/logs/.

Use this when 'ralph logs --iter=N' is not enough — typically to
debug a failed iteration or to verify what the runner actually saw.
--tail-lines=0 disables the tail and dumps the full stdout/stderr.

```
ralph trace <iter> [flags]
```

### Examples

```
  # full trace of iter 42 (50-line stdout/stderr tail by default)
  ralph trace 42

  # dump the whole stdout/stderr, no tail
  ralph trace 42 --tail-lines=0

  # last 200 lines of runner output
  ralph trace 42 --tail-lines=200
```

### Options

```
  -h, --help             help for trace
      --tail-lines int   tail this many lines from stdout/stderr (0 = all) (default 50)
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

