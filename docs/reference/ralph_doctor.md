## ralph doctor

Check ralph's runtime environment

### Synopsis

Verify that ralph's runtime dependencies are present and usable:
Linux, systemd-run --user --scope, claude, bd, git, and the
.ralph/ and .beads/ directories in the current repo.

```
ralph doctor [flags]
```

### Options

```
  -h, --help   help for doctor
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

