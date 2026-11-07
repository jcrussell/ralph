# Isolation reference

Ralph runs the AI runner inside a `systemd-run --user --scope` cgroup so a runaway iteration can't take down the host. Three reasons:

1. **Hard memory cap.** Bound the runner's RSS so a pathological prompt can't OOM-kill the parent or fill swap.
2. **OOM detection.** A clean signal — kernel-killed iterations are classified as `ModeOOM` and trigger the `oom_secs` backoff instead of looking like a runner crash.
3. **Killable process tree.** Stopping the unit kills every process in its cgroup, including grandchildren that left the runner's process group. Ralph stops the scope after every `Run` (see [cleanup](#cleanup)).

Source: `internal/isolation/systemd.go`.

## Linux-only

`systemd-run` is a Linux primitive. `ralph run` refuses to start on any other GOOS (per memory `isolation-linux-only`). No rlimit fallback. If you need ralph on macOS/Windows, file an issue — we'd rather know the demand than ship a half-isolated path.

## The scope

A scope is the (unit-name, memory-cap) pair `internal/isolation.Scope` wraps. `NewScope(unitBase, memoryLimit)` constructs one; the runner builds a fresh `ralph-<pid>-<seq>.scope` per `Run` call so unit names don't collide. `Argv` wraps the configured runner command:

```
systemd-run --user --scope --unit=ralph-<pid>-<seq> \
  -p MemoryMax=<bytes> -p MemorySwapMax=0 \
  -- <command> [args...]
```

`--unit` takes the name without the `.scope` suffix; systemd-run adds it.

`MemorySwapMax=0` is load-bearing: without it, the kernel will swap out the runner before invoking the OOM killer, which makes the iteration crawl and never reaches the OOM killer. With swap pinned at zero, exceeding `MemoryMax` becomes a clean OOM.

## OOM detection

```mermaid
sequenceDiagram
  participant Loop as orchestrator
  participant Run as runner subprocess
  participant SD as systemd-run scope
  participant Cg as systemd (unit Result)
  participant K as Linux OOM killer
  Loop->>SD: Argv(...) → exec
  SD->>Run: start
  Run->>Run: allocate beyond MemoryMax
  K->>SD: oom_kill in the scope's cgroup
  SD->>Run: SIGKILL
  Run-->>Loop: exit (nonzero, no envelope)
  Loop->>Cg: systemctl --user show -p Result
  Cg-->>Loop: Result=oom-kill
  Note over Loop: Classify → ModeOOM,<br/>back off oom_secs
```

After the runner subprocess exits, `internal/runner` asks systemd for the scope's `Result` (`isolation.Scope.Result`) and stamps `Session.OOMSignal` when it is `oom-kill` (some OOM killer, the scope's cap or the host's, killed a process in the scope). It can't read the cgroup's `memory.events` instead: systemd removes the scope's cgroup as soon as its last process exits, but it keeps a failed scope loaded, with its `Result`, until the unit is reset. The classifier prefers this signal over a bare `SIGKILL` (or exit 137 from a wrapper script), which can also mean "killed by some other SIGKILL"; that path stays as a fallback for a probe that fails, and it is also how a host-level OOM kill shows up.

## Cleanup

On cancel or timeout the runner SIGKILLs its process group, but a descendant that called `setsid` (a daemonized test server, a build or test worker) is not in that group and would keep the scope, and its memory and any repo locks, alive after the iteration. So after the runner exits, ralph (`isolation.Scope.Cleanup`):

1. SIGTERMs whatever is left in the scope and gives it 2s to exit (so git and friends can drop their lock files), then SIGKILLs the rest rather than wait the unit's `TimeoutStopSec` (90s).
2. Reads the scope's `Result` for the OOM verdict, once the unit has settled (resetting the unit discards it).
3. If the unit is still loaded (it failed), runs `systemctl --user stop` and `reset-failed` on it so failed scopes don't pile up. A clean exit was already collected.

A unit that is already gone is not an error. If the stop itself fails, ralph logs a warning to `orchestrator.log` and carries on. A scope can still outlive ralph if ralph itself is SIGKILLed mid-iteration; see [unattended runs](../guides/unattended.md#things-that-bite) for finding it.

## Probing at startup

`ralph doctor` calls `isolation.Available(ctx)` which runs `systemd-run --user --scope true` and reports the systemd output if it fails. Two common failures:

- `Failed to connect to bus: $DBUS_SESSION_BUS_ADDRESS` — no user session bus. Run `loginctl enable-linger $USER` and re-login.
- `Failed to start transient scope: Unit ...service not loaded` — `user@<uid>.service` not active. Usually fixed by `systemctl --user start dbus.socket`.

`ralph doctor` also checks the cap itself: its `memory cap` row warns when `memory_limit_bytes` leaves less than 1G under the tightest of the host's `MemTotal` and the `memory.max` of `user-<uid>.slice` and `user@<uid>.service`, or when the cap is disabled. Without that headroom the host (or the user slice) runs out of memory before the runner reaches its own cap, and the kernel's OOM kill takes the whole session with no output. It is classified `oom` either way: the kernel counts an OOM kill in the victim's cgroup whatever limit ran out, so the scope's `Result=oom-kill` (`oom_killed`) does not by itself say the cap fired. The kernel log does (`Memory cgroup out of memory` vs `Out of memory: Killed process`). The check can't see a `MemoryMax` on the service ralph runs in, so leave room for that too.

## Configuration knobs

See [config.md](config.md):

- `[loop] memory_limit_bytes` — the `MemoryMax` value (default `"7G"`).
- `[loop] session_timeout_secs` — how long ralph waits before SIGKILLing the scope if the runner doesn't exit.
- `[backoff] oom_secs` — sleep applied after `ModeOOM` before the next iteration.

## What isolation does NOT do

- **Network namespacing.** The runner has the same network access as ralph itself. If you need air-gapping, run ralph in a network-namespaced container.
- **Filesystem confinement.** The runner can read and write anything ralph can. The point of isolation is to cap memory and contain the process tree, not to sandbox the agent's actions.
- **CPU limits.** Currently no `CPUQuota=...` knob. The model rate-limits naturally and CPU caps would mostly slow recovery from a transient overload, so we haven't added one.
