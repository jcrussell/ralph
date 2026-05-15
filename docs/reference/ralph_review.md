## ralph review

Run the ralph loop in review mode against a branch

### Synopsis

review drives the FSM-orchestrated loop with review_mode=true:
the loop suppresses auto-revert (would silently destroy work) and
routes via the review state, whose prompt has the agent ingest diff
findings against the base, claim and fix them one at a time, and
file a merge:<branch> bead when the queue is empty. The orchestrator
exits via done{queue_empty} once review:<branch> is drained and the
working tree is clean.

Branch resolution: --branch wins; else the current checkout; HEAD
detached without --branch is an error. Base resolution: --base wins;
else [review] base_branch from config; else "main". Reviewing the
base against itself is rejected.

--pr N is sugar for "gh pr checkout N" run before resolution; gh is
not a hard dependency of ralph itself.

```
ralph review [flags]
```

### Examples

```
  # review the currently checked-out branch against [review] base_branch
  ralph review

  # review feat/foo against main, one iteration only
  ralph review --branch=feat/foo --base=main --once

  # check out PR 123 first, then review against [review] base_branch
  ralph review --pr=123

  # cap iterations at 10 for this run
  ralph review --max-rounds=10
```

### Options

```
      --base string           base branch (default: [review] base_branch)
      --branch string         branch under review (default: current)
      --dry-run               render prompts and route states without invoking the runner
      --fresh                 reset fsm.json before starting (use when the prior run reached a terminal state)
  -h, --help                  help for review
      --label string          explicit iteration label (overrides the review:<branch> default)
      --max-rounds int        override [loop] max_iterations for this run
      --no-label              do not auto-label iterations review:<branch>
      --once                  run one iteration then exit
      --pr gh pr checkout N   run gh pr checkout N before resolving the branch
      --skip-gate             skip the per-state gate hook
```

### Options inherited from parent commands

```
      --log-level string   explicit log level (warn|info|debug); overrides -v
  -v, --verbose count      increase log verbosity (-v=info, -vv=debug)
```

### SEE ALSO

* [ralph](ralph.md)	 - FSM-driven autonomous-loop CLI

