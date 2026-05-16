# ralph

An FSM-driven autonomous-loop CLI for running an AI coding agent on a task queue, hour after hour, without losing the plot. Linux-only; requires [`bd`](https://github.com/jcrussell/beads) and the `claude` CLI on `$PATH`.

## Install

```sh
go install github.com/jcrussell/ralph/cmd/ralph@latest
```

Binaries and checksums: [releases page](https://github.com/jcrussell/ralph/releases).

## Quickstart

From inside a git repo with a populated `bd` queue:

```sh
$ ralph init
created .ralph/config.toml
created .ralph/prompts/clean.md
...
23 created, 0 skipped

$ ralph run --once --dry-run    # routes, renders, exits — claude never invoked

$ ralph status
state:             clean
iter:              1 / 30
review:            off
cost:              $0.00 / unlimited
```

The rendered prompt is at `.ralph/state/logs/iter-0001-*-prompt.txt`. Drop `--dry-run` for a real iteration; drop `--once` for an autonomous session that terminates on `done{queue_empty}`, `done{iter_cap}`, or `failed{*}`.

## Where to look next

- `ralph --help` (and `ralph <cmd> --help`) — every subcommand and flag.
- [docs/](docs/) — concepts (FSM, prompts, hooks, bd integration) and generated CLI reference, regenerated each release.
- [Design decisions](https://github.com/jcrussell/byob-go-cli) — inherited from byob-go-cli; browse with `bd list --type=decision`.
