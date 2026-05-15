## ralph prompt show

Render the prompt for <state> without running anything

### Synopsis

Renders prompts/<state>.md from the current repo's .ralph/ directory,
wrapped with prompts/_header.md and prompts/_footer.md when present.
<state> must be one of: clean, dirty, revert, review.

```
ralph prompt show <state> [flags]
```

### Options

```
      --gate-result string   value for .GateResult (passed|failed|not-run) (default "not-run")
      --git-dirty            value for .GitDirty
      --git-head string      value for .GitHead
  -h, --help                 help for show
      --iter int             value for .Iter
```

### Options inherited from parent commands

```
      --log-level string   explicit log level (warn|info|debug); overrides -v
  -v, --verbose count      increase log verbosity (-v=info, -vv=debug)
```

### SEE ALSO

* [ralph prompt](ralph_prompt.md)	 - Inspect ralph prompts

