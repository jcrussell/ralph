## ralph prompt show

Render the prompt for <state> without running anything

### Synopsis

Renders prompts/<state>.md from the current repo's .ralph/ directory,
wrapped with prompts/_header.md and prompts/_footer.md when present.
<state> must be one of: clean, dirty, revert, review.

Template vars (.Iter, .GitHead, .GitDirty, .GateResult) default to
zero values; override with the matching flags to preview how the
prompt looks under realistic loop conditions.

```
ralph prompt show <state> [flags]
```

### Examples

```
  # preview the clean-state prompt as it would render on iter 0
  ralph prompt show clean

  # preview the dirty-state prompt after a failed gate, iter 42
  ralph prompt show dirty --iter=42 --gate-result=failed --git-dirty

  # diff a prompt edit against the live render
  ralph prompt show clean | diff -u .ralph/prompts/clean.md -
```

### Options

```
      --gate-result gate-result   value for .GateResult (passed|failed|not-run) (default not-run)
      --git-dirty                 value for .GitDirty
      --git-head string           value for .GitHead
  -h, --help                      help for show
      --iter int                  value for .Iter
```

### Options inherited from parent commands

```
      --log-file string     append log records to this file instead of stderr
      --log-format string   log record format (text|json); default text
      --log-level string    explicit log level (warn|info|debug); overrides -v
  -v, --verbose count       increase log verbosity (-v=info, -vv=debug)
```

### SEE ALSO

* [ralph prompt](ralph_prompt.md)	 - Inspect ralph prompts

