## ralph init

Scaffold .ralph/ in the current repo

### Synopsis

Creates .ralph/{config.toml, prompts/*.md, hooks/...} and writes a
.gitignore inside .ralph/state/ so runtime state is ignored. Existing
files are preserved unless --force is given.

Run this once per repo before the first ralph run. Re-running is
safe — already-edited files are skipped. Use --force only when you
want to reset prompts or hooks back to the defaults.

```
ralph init [flags]
```

### Examples

```
  # scaffold a fresh .ralph/ tree
  ralph init

  # reset prompts and hooks to defaults (overwrites your edits)
  ralph init --force
```

### Options

```
  -f, --force   overwrite existing files
  -h, --help    help for init
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

