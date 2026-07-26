## ralph gc

Prune old runs, iteration artifacts, and incidents from .ralph/state

### Synopsis

gc removes aged orchestrator state under .ralph/state:

  - run directories      .ralph/state/runs/<RUN_ID>/
  - iteration artifacts  .ralph/state/logs/iter-*
  - incidents            .ralph/state/incidents/*.md

An item is eligible when the timestamp encoded in its directory or file
name is older than --older-than (e.g. 30d, 2w, 72h). summary.jsonl and
orchestrator.log are never touched.

Like 'git clean', gc is dry-run by default: with no --force it prints
exactly what it would delete and exits without changing anything. Pass
-f/--force to actually delete.

If an orchestrator is currently running (a live pid.lock), its run is
held back regardless of age. Run directories whose names don't parse as
timestamps are left alone for manual inspection.

```
ralph gc [flags]
```

### Examples

```
  # preview what is older than 30 days (deletes nothing)
  ralph gc --older-than 30d

  # actually delete it
  ralph gc --older-than 30d --force

  # machine-readable plan
  ralph gc --older-than 2w --json runs,iterations,incidents,total_bytes,total_count

  # just the byte total, via built-in jq
  ralph gc --older-than 2w --json total_bytes --jq '.total_bytes'
```

### Options

```
  -f, --force               actually delete; without it gc only prints what would be removed
  -h, --help                help for gc
      --jq string           filter --json output with a jq expression
      --json string         output JSON with the given comma-separated fields (available: cutoff,older_than,runs,iterations,incidents,skipped_active_run,total_bytes,total_count,applied)
      --older-than string   delete state older than this age (e.g. 30d, 2w, 72h); required
      --template string     format --json output with a Go template
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

