# Unattended runs

`ralph run` is built to run for days without anyone watching. It can bound
itself by wall-clock time (`--until`), ride out usage caps
(`--wait-on-quota`), cool down and retry a runner that keeps dying
(`[loop] runner_terminal_retries`), and it exits with a code that names how it
ended. What it can't do alone is survive a reboot or a crash of its own
process. For that, run it as a systemd service whose `Restart=` policy keys
off those exit codes.

[`contrib/systemd/ralph.service`](../../contrib/systemd/ralph.service) is a
working starting point, generalized from a setup that keeps ralph running for
days at a time.

## Install

Edit `ralph.service` first:

- Replace `USER`, `UID` and `/path/to/repo`.
- Set `RALPH_UNTIL` to the end of the window.
- Extend `PATH` to cover your toolchain.
- Add any proxy or CA variables the runner needs.

Then:

```sh
sudo cp contrib/systemd/ralph.service /etc/systemd/system/ralph-myrepo.service
sudo systemctl daemon-reload
sudo loginctl enable-linger USER
sudo systemctl enable --now ralph-myrepo
```

To have a dead runner cool down and retry instead of stopping the service, add
this to the repo's `.ralph/config.toml`:

```toml
[loop]
runner_terminal_retries = 6        # in a row; a healthy session resets the count
runner_terminal_cooldown_secs = 900
```

Run `ralph doctor` as the service user before enabling the unit: it checks
`systemd-run --user --scope` and warns when `memory_limit_bytes` leaves the
host too little headroom.

Watch it with `journalctl -u ralph-myrepo -f`. Every run ends with one
`ralph: run ended: <outcome> (exit N)` line, or a JSON object under
`--log-format json`. `ralph status` works as usual from the repo and shows the
deadline.

## What each outcome does

ralph's exit codes are a documented contract
([exit codes](../reference/exit-codes.md)). The unit restarts on the ones that
mean "carry on" and stops on the rest:

| Exit | Outcome | Unit |
|------|---------|------|
| 1 | Unexpected error (lock held, disk full, bd failure) | Restart after `RestartSec`; the run resumes with its totals. |
| — | ralph crashed or was killed | Restart after `RestartSec`; the run resumes with its totals. |
| 22 | `failed{runner_terminal}`: retries spent, or none configured | Stop, unit failed. Read the dead sessions' records (below), then start again; the first start needs `--fresh` (exit 3 otherwise). |
| 21 | `failed{auth}` | Stop, unit failed: log the runner in again. |
| 20 | `failed{budget}` | Stop, unit failed: the `[budget]` cap was hit. |
| 3 | `fsm.json` is `failed{*}` | Stop, unit failed: inspect, then run `ralph run --fresh` once by hand. |
| 12 | `done{deadline}` | Stop. A restart before you move `RALPH_UNTIL` exits 12 again at once. |
| 0, 10 | `done{queue_empty}`, `done{idle}` | Stop. Pass `--wait-for-beads` to park on an empty queue instead. |
| 11 | `done{iter_cap}` | Stop. With `--unlimited` this only happens if a cap is set in config. |

The unit doesn't restart on any `done{*}`: the next `ralph run` resets a
finished run and starts a fresh one, which zeroes the cost and wallclock
totals that `[budget]` caps.

The deadline is an absolute date-time, so the same `--until` means the same
window on every restart. To start a new window, run
`systemctl edit ralph-myrepo`, set `Environment=RALPH_UNTIL=...` and restart
the service. An iteration already running when the deadline passes finishes
first (runner, gate and hooks), so a run can end late by up to
`session_timeout_secs` plus the gate's `timeout_secs` plus however long the
other hooks take, which have no timeout of their own; with either timeout at
0 there is no bound. Every wait (quota, bead park, cooldown) ends at the
deadline.

## Diagnosing a dead runner

`failed{runner_terminal}` is usually a `dead_session` streak (see
[backoff](../reference/backoff.md)): the runner exited without producing a
result. Each iteration record says how the process ended:

```sh
ralph logs | tail -5
# iter 0085  clean → clean: dead session (exit 1), runner_terminal: retry 1/6 after 15m0s cooldown
jq -c 'select(.runner_mode=="dead_session" or .runner_mode=="oom") | {iter, runner_mode, exit_code, signal, oom_killed, stderr_tail}' \
  .ralph/state/logs/summary.jsonl
```

How to read the fields:

- `runner_mode: oom` means the kernel's OOM killer most likely ended the
  session. `oom_killed: true` says systemd saw it happen inside the runner's
  scope (`Result=oom-kill`); `false` with a bare `SIGKILL` (or exit 137 from a
  wrapper script) means the verdict came from the signal alone.
- Neither field says *which* limit ran out: the kernel counts an OOM kill in
  the victim's cgroup whether the scope's own cap or the whole host (or user
  slice) was exhausted. Check the kernel log: `journalctl -k | grep -i
  'out of memory'` shows `Memory cgroup out of memory` for a cgroup cap
  (`memory_limit_bytes`, or a `MemoryMax` above it) and `Out of memory: Killed
  process` for the host. See the memory caps below.
- A `dead_session` with a non-zero `exit_code` and a `stderr_tail` is the
  runner's own error. Another signal (`SIGSEGV`, `SIGABRT`) means it crashed.

## Things that bite

**The user bus.** ralph isolates each iteration with
`systemd-run --user --scope`, which talks to the user manager over
`$XDG_RUNTIME_DIR/bus`. A system service has neither `XDG_RUNTIME_DIR` nor
`DBUS_SESSION_BUS_ADDRESS`, and `user@UID.service` only runs while the user
is logged in unless lingering is on (`loginctl enable-linger`). Without
these, `systemd-run` fails and every iteration dies at start.
`ralph doctor` run as the service user shows the same error.

**Scopes belong to the user manager.** Iteration scopes live under
`user@UID.service`, not the service's cgroup, so `systemctl stop` on the
service does not reach them. ralph stops each iteration's scope itself when
the runner exits, which kills any descendant that outlived it. Only if ralph
is SIGKILLed mid-session (it outlived `TimeoutStopSec`, or the OOM killer took
it) can a scope survive it. Find it with `systemctl --user list-units
'ralph-*'`: units are named `ralph-<ralph pid>-<n>.scope`, so stop the ones
whose pid is gone. Don't add an `ExecStopPost` that stops `ralph-*.scope`:
that also kills other repos' runs as the same user.

**Hooks are not in the runner's scope.** `[loop] memory_limit_bytes` caps the
runner, but gate and state hooks run in ralph's own process tree, so they run
inside the service's cgroup. A test suite or a cold compile in the gate is
often the largest process on the box. Cap the service with `MemoryMax`, and
keep the gate's parallelism within it (for example, fewer compiler jobs on a
small host).

**Leave headroom under both caps.** The default `memory_limit_bytes` is `7G`.
If that is about the size of the machine, a runaway process is only killed
once the host is out of memory, and the OOM kill takes the whole runner
session with it (no output, recorded as `runner_mode: oom`; the kernel log
says it was the host, not the cap).

- Set the runner cap below the host's RAM, with room for the gate's cap: the
  runner's `memory_limit_bytes` plus the unit's `MemoryMax` should fit in RAM
  with some to spare. `ralph doctor` warns when the runner cap alone leaves
  less than 1G; it can't see the service's `MemoryMax`, so add that up
  yourself.
- `MemorySwapMax=0` keeps an over-cap process from swapping instead of being
  killed.

**Stopping is graceful.** `systemctl stop` sends SIGTERM to ralph alone
(`KillMode=mixed`). ralph stops the running session and keeps its cost in the
run totals, then exits 143, which the unit treats as success. It leaves
`fsm.json` in its pre-iteration state: a stop never counts as a dead session,
and a stop while parked for beads doesn't record `done{*}`. `TimeoutStopSec`
bounds the unwind.

**Environment.** A system service does not read your shell profile. Put
everything the runner and hooks need (`PATH` entries for the toolchain,
proxy variables, CA bundles) in the unit's `Environment=` lines. Or point
`[runner] command` at a wrapper script that sets them up before it execs the
runner.

**Updating ralph.** A running ralph keeps the binary it started with. After
rebuilding, restart the service to pick up the new binary. A stop mid-iteration
discards that iteration's session, keeping its cost, and resumes from the same
state.
