## ralph prompt

Inspect ralph prompts

### Synopsis

Prompts compose as _header.md + prompts/<state>.md + _footer.md
(header and footer optional). Use the prompt subcommand to render a
state's prompt without invoking the runner — fast iteration on
prompt authoring without burning tokens.

### Options

```
  -h, --help   help for prompt
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
* [ralph prompt show](ralph_prompt_show.md)	 - Render the prompt for <state> without running anything

