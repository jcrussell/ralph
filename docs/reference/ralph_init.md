## ralph init

Scaffold .ralph/ in the current repo

### Synopsis

Creates .ralph/{config.toml, prompts/*.md, hooks/...} and writes a
.gitignore inside .ralph/state/ so runtime state is ignored. Existing
files are preserved unless --force is given.

```
ralph init [flags]
```

### Options

```
  -f, --force   overwrite existing files
  -h, --help    help for init
```

### SEE ALSO

* [ralph](ralph.md)	 - FSM-driven autonomous-loop CLI

