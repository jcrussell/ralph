## ralph doctor

Check ralph's runtime environment

### Synopsis

Verify that ralph's runtime dependencies are present and usable:
Linux, systemd-run --user --scope, claude, bd, git, and the
.ralph/ and .beads/ directories in the current repo.

Run this after ralph init on a fresh machine, or when a run fails
with an obscure spawn/exec error. Exit code is non-zero when any
check fails; the failing rows are tagged FAIL.

```
ralph doctor [flags]
```

### Examples

```
  # verify the environment
  ralph doctor

  # gate a CI job on a clean environment
  ralph doctor && ralph run --once
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

