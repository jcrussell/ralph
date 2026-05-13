# Isolation reference

Ralph runs the AI runner inside a `systemd-run --user --scope` cgroup so a runaway iteration can't take down the host. Three reasons:

1. **Hard memory cap.** Bound the runner's RSS so a pathological prompt can't OOM-kill the parent or fill swap.
2. **OOM detection.** A clean signal — kernel-killed iterations are classified as `ModeOOM` and trigger the `oom_secs` backoff instead of looking like a runner crash.
3. **Killable process tree.** `systemctl --user kill <unit>` stops every descendant cleanly, even when the runner has forked grandchildren.

Source: `internal/isolation/systemd.go`.

## Linux-only

`systemd-run` is a Linux primitive. `ralph run` refuses to start on any other GOOS (per memory `isolation-linux-only`). No rlimit fallback. If you need ralph on macOS/Windows, file an issue — we'd rather know the demand than ship a half-isolated path.

## The scope

A scope is the (unit-name, memory-cap) pair `internal/isolation.Scope` wraps. `NewScope(unitBase, memoryLimit)` constructs one; the loop builds one per iteration so unit names don't collide. `Argv` wraps the configured runner command:

```
systemd-run --user --scope --unit=<unit>.scope \
  -p MemoryMax=<bytes> -p MemorySwapMax=0 \
  -- <command> [args...]
```

`MemorySwapMax=0` is load-bearing: without it, the kernel will swap out the runner before invoking the OOM killer, which makes the iteration crawl and never shows up in `memory.events`. With swap pinned at zero, exceeding `MemoryMax` becomes a clean OOM.

## OOM detection

```mermaid
sequenceDiagram
  participant Loop as orchestrator
  participant Run as runner subprocess
  participant SD as systemd-run scope
  participant Cg as cgroup memory.events
  participant K as Linux OOM killer
  Loop->>SD: Argv(...) → exec
  SD->>Run: start
  Run->>Run: allocate beyond MemoryMax
  K->>SD: oom_kill events++
  SD->>Run: SIGKILL
  Run-->>Loop: exit (nonzero, no envelope)
  Loop->>Cg: read EventsPath
  Cg-->>Loop: oom_kill N>0
  Note over Loop: Classify → ModeOOM,<br/>back off oom_secs
```

`Scope.OOMKilled()` reads `/sys/fs/cgroup/user.slice/user-<uid>.slice/user@<uid>.service/<unit>/memory.events` and returns true when `oom_kill` is > 0. Missing or unparsable file = no OOM (matches the Python reference behavior — fail open rather than false-positive on systemd quirks).

The classifier in `internal/runner/classify.go` checks `s.ExitCode == 137` as a backup signal — `systemd-run` propagates SIGKILL as exit 137, so even when `memory.events` is unavailable the iteration ends up in `ModeOOM`.

## Probing at startup

`ralph doctor` calls `isolation.Available(ctx)` which runs `systemd-run --user --scope true` and reports the systemd output if it fails. Two common failures:

- `Failed to connect to bus: $DBUS_SESSION_BUS_ADDRESS` — no user session bus. Run `loginctl enable-linger $USER` and re-login.
- `Failed to start transient scope: Unit ...service not loaded` — `user@<uid>.service` not active. Usually fixed by `systemctl --user start dbus.socket`.

## Configuration knobs

See [config.md](config.md):

- `[loop] memory_limit_bytes` — the `MemoryMax` value (default `"7G"`).
- `[loop] session_timeout_secs` — how long ralph waits before SIGKILLing the scope if the runner doesn't exit.
- `[backoff] oom_secs` — sleep applied after `ModeOOM` before the next iteration.

## What isolation does NOT do

- **Network namespacing.** The runner has the same network access as ralph itself. If you need air-gapping, run ralph in a network-namespaced container.
- **Filesystem confinement.** The runner can read and write anything ralph can. The point of isolation is to cap memory and contain the process tree, not to sandbox the agent's actions.
- **CPU limits.** Currently no `CPUQuota=...` knob. The model rate-limits naturally and CPU caps would mostly slow recovery from a transient overload, so we haven't added one.
