## ralph version

Print ralph version, commit, and build info

### Synopsis

Prints the multi-line version block: ralph version, commit hash,
build date, Go toolchain, and OS/arch. Use this when reporting bugs
or verifying which binary is on PATH.

The root --version flag prints only the one-line banner — both
routes share build.Info() so they cannot drift.

```
ralph version [flags]
```

### Examples

```
  # full build info
  ralph version

  # just the banner (cobra's built-in --version)
  ralph --version
```

### Options

```
  -h, --help   help for version
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

