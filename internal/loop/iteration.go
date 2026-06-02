package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jcrussell/ralph/internal/atomicfile"
	"github.com/jcrussell/ralph/internal/backoff"
	"github.com/jcrussell/ralph/internal/bd"
	"github.com/jcrussell/ralph/internal/fsm"
	"github.com/jcrussell/ralph/internal/git"
	"github.com/jcrussell/ralph/internal/hooks"
	"github.com/jcrussell/ralph/internal/incidents"
	"github.com/jcrussell/ralph/internal/narrative"
	"github.com/jcrussell/ralph/internal/paths"
	"github.com/jcrussell/ralph/internal/promptlib"
	"github.com/jcrussell/ralph/internal/runner"
	"github.com/jcrussell/ralph/internal/runs"
)

// IterRecord is the schema of one line in state/logs/summary.jsonl.
// It is the single source of truth for that shape; observability
// commands (logs, report, timeline) decode the subset they need.
type IterRecord struct {
	Iter           int      `json:"iter"`
	IterID         string   `json:"iter_id"`
	Timestamp      string   `json:"timestamp"`
	State          string   `json:"state"`
	Reason         string   `json:"reason,omitempty"`
	PrevState      string   `json:"prev_state,omitempty"`
	Narrative      string   `json:"narrative"`
	RunnerMode     string   `json:"runner_mode,omitempty"`
	GateResult     string   `json:"gate_result,omitempty"`
	GateStdoutFile string   `json:"gate_stdout_file,omitempty"`
	GateStderrFile string   `json:"gate_stderr_file,omitempty"`
	CostUSD        float64  `json:"cost_usd,omitempty"`
	DurationSecs   float64  `json:"duration_secs,omitempty"`
	Commits        int      `json:"commits,omitempty"`
	GitHead        string   `json:"git_head,omitempty"`
	BDDiff         *bd.Diff `json:"bd_diff,omitempty"`
	PromptFile     string   `json:"prompt_file,omitempty"`
	Skipped        string   `json:"skipped,omitempty"`
	QuotaWaitSecs  int      `json:"quota_wait_secs,omitempty"`
}

// iterPaths bundles the per-iteration artifact paths. Computed
// once at the top of runIteration.
type iterPaths struct {
	stem       string
	prompt     string
	stdout     string
	stderr     string
	gateStdout string
	gateStderr string
	json       string
	logsDir    string
}

func newIterPaths(repo string, iter int, ts time.Time) iterPaths {
	logsDir := paths.New(repo).LogsDir()
	stem := fmt.Sprintf("iter-%04d-%s", iter, ts.UTC().Format("20060102T150405Z"))
	join := func(suffix string) string { return filepath.Join(logsDir, stem+suffix) }
	return iterPaths{
		stem:       stem,
		prompt:     join("-prompt.txt"),
		stdout:     join("-stdout.txt"),
		stderr:     join("-stderr.txt"),
		gateStdout: join("-gate-stdout.txt"),
		gateStderr: join("-gate-stderr.txt"),
		json:       join(".json"),
		logsDir:    logsDir,
	}
}

// runIteration executes one tick of the loop: bump counters, run hooks
// + runner + gate, classify, route, persist. Returns the new outcome.
// Each numbered phase is an unexported helper below so the steps stay
// independently testable and the future Observer call site (the persist
// phase) is obvious.
func runIteration(ctx context.Context, rc *runContext) (fsm.Outcome, error) {
	if err := ctx.Err(); err != nil {
		return rc.fsm.Outcome, err
	}

	rc.fsm.BumpIter()
	prev := rc.fsm.Outcome
	now := rc.clock.Now().UTC()
	rc.paths = newIterPaths(rc.repo, rc.fsm.Iter, now)
	if err := os.MkdirAll(rc.paths.logsDir, 0o750); err != nil {
		return prev, fmt.Errorf("loop: mkdir logs: %w", err)
	}

	beforeBD := snapshotBD(ctx, rc)
	beforeHead, _ := git.HeadSHA(ctx, rc.repo)

	// 1. Global pre-iteration hook. Non-zero → skip the runner this tick.
	skipReason, err := runPreIterationHook(ctx, rc)
	if err != nil {
		return prev, err
	}
	if skipReason != "" {
		return prev, recordSkippedIteration(rc, prev, skipReason)
	}

	// 2. Per-state enter hook (only on entry into a new state).
	runEnterHook(ctx, rc, prev)

	// 3+4. Render + capture the prompt, then run the runner.
	sess, mode, err := runRunnerPhase(ctx, rc, prev, beforeHead)
	if err != nil {
		return prev, err
	}

	// 4b. Opt-in quota wait. A quota cap normally routes to
	// failed{runner_terminal} (classifyToReason). When wait_on_quota is
	// set, sleep a bounded, ctx-cancellable interval and resume the same
	// state instead — the 5-hour/weekly/monthly window is expected to
	// reset. When the error surfaces a reset instant ("resets 10:30pm
	// (UTC)") the wait sleeps until then; otherwise it polls. Default stays
	// terminate so we don't regress the fail-fast behavior. SIGINT/SIGTERM
	// during the sleep cancels it and the loop's top-level ctx.Err() check
	// exits cleanly on the next tick.
	if mode == runner.ModeQuota && rc.cfg.Loop.WaitOnQuota {
		return waitOnQuota(ctx, rc, prev, sess)
	}

	// 5. Gate hook (post-runner, pre-routing) + commit count for the tick.
	gate, commits := runGatePhase(ctx, rc, prev, beforeHead)

	// 6. Write the iter JSON (post-iteration sees this on stdin + env).
	preJSON := composeIterRecord(rc, prev, prev, sess, mode, gate, bd.Diff{}, commits, now, beforeHead)
	if werr := writeIterJSON(rc.paths.json, preJSON); werr != nil {
		return prev, fmt.Errorf("loop: write iter json: %w", werr)
	}

	// 7. Global post-iteration hook (stdin = iter json).
	runPostIterationHook(ctx, rc)

	// 8–14. Route to the next state, persist FSM + summary + transition +
	// incident. This is where a live-run Observer would be notified.
	next, err := routeAndPersist(ctx, rc, prev, beforeBD, sess, mode, gate, commits, now)
	if err != nil {
		return next, err
	}

	// 15. Backoff sleep — skipped on terminal so Run can exit cleanly.
	sleepBackoff(ctx, rc, next, mode, sess)

	return next, nil
}

// runPreIterationHook runs the global pre-iteration hook. It returns a
// non-empty skip reason when the hook exits non-zero (the tick should
// skip the runner), and an error only when the hook itself fails to run
// (fatal — it gates the runner). The prompt isn't rendered yet, so
// PromptFile is cleared.
func runPreIterationHook(ctx context.Context, rc *runContext) (string, error) {
	env := buildHookEnv(rc, hooks.PhaseNone, "")
	env.PromptFile = "" // not rendered yet
	res, err := hooks.Run(ctx, hooks.GlobalPath(rc.repo, "pre-iteration"), env, nil, nil, nil)
	if err != nil {
		return "", fmt.Errorf("loop: pre-iteration: %w", err)
	}
	if !res.NoHook && res.ExitCode != 0 {
		rc.log.WarnContext(ctx, "pre-iteration skipped iteration", "exit", res.ExitCode)
		return fmt.Sprintf("pre-iteration exit %d", res.ExitCode), nil
	}
	return "", nil
}

// runEnterHook fires the per-state enter hook the first time the loop
// enters a state. Errors are best-effort (logged, not fatal).
func runEnterHook(ctx context.Context, rc *runContext, prev fsm.Outcome) {
	if rc.lastEnteredState == prev.State {
		return
	}
	env := buildHookEnv(rc, hooks.PhaseEnter, "")
	if _, hErr := hooks.Run(ctx, hooks.StatePath(rc.repo, string(prev.State), hooks.PhaseEnter), env, nil, nil, nil); hErr != nil {
		rc.log.WarnContext(ctx, "enter hook error", "state", prev.State, "err", hErr)
	}
	rc.lastEnteredState = prev.State
}

// runRunnerPhase renders the prompt, captures it to disk, and runs the
// runner (skipped on --dry-run). On --dry-run it returns a nil session
// and ModeOK without touching the streak/cumulative counters. Returns
// the session + classified mode, or an error for prompt/runner failures.
func runRunnerPhase(ctx context.Context, rc *runContext, prev fsm.Outcome, beforeHead string) (*runner.Session, runner.Mode, error) {
	prompt, err := composePrompt(ctx, rc, prev, beforeHead)
	if err != nil {
		return nil, runner.ModeOK, err
	}
	if werr := writeAtomic(rc.paths.prompt, []byte(prompt)); werr != nil {
		return nil, runner.ModeOK, fmt.Errorf("loop: capture prompt: %w", werr)
	}
	if rc.opts.DryRun {
		return nil, runner.ModeOK, nil
	}

	sessCtx := ctx
	if rc.cfg.Loop.SessionTimeoutSecs > 0 {
		var cancel context.CancelFunc
		sessCtx, cancel = context.WithTimeout(ctx, time.Duration(rc.cfg.Loop.SessionTimeoutSecs)*time.Second)
		defer cancel()
	}
	var stdoutTee, stderrTee io.Writer
	if rc.io != nil {
		stdoutTee, stderrTee = rc.io.Out, rc.io.ErrOut
	}
	sess, err := rc.runr.Run(sessCtx, runner.RunOpts{
		Prompt:     prompt,
		Cwd:        rc.repo,
		StdoutPath: rc.paths.stdout,
		StderrPath: rc.paths.stderr,
		StdoutTee:  stdoutTee,
		StderrTee:  stderrTee,
	})
	if err != nil {
		return nil, runner.ModeOK, fmt.Errorf("loop: runner start: %w", err)
	}
	mode := runner.Classify(sess)
	updateStreaks(rc, mode)
	updateCumulatives(rc, sess)
	return sess, mode, nil
}

// runGatePhase counts commits the runner produced this tick and runs the
// per-state gate hook. commits is returned for use in the summary record
// and transition row (it also gates the "commits-only" run-when policy).
func runGatePhase(ctx context.Context, rc *runContext, prev fsm.Outcome, beforeHead string) (gateOutcome, int) {
	commits := 0
	if beforeHead != "" {
		if n, cerr := git.CountCommits(ctx, rc.repo, beforeHead, "HEAD"); cerr == nil {
			commits = n
		}
	}
	return runGate(ctx, rc, prev, commits), commits
}

// runPostIterationHook fires the global post-iteration hook with the iter
// JSON on stdin + env. Best-effort: a failure here is recoverable — the
// iteration's work is already persisted (iter json above, summary below),
// so log and continue rather than abort the loop. This is unlike the
// pre-iteration hook, whose error is fatal because it gates the runner.
// Mirrors the enter/exit hook convention: slog WarnContext, no ErrOut spam.
func runPostIterationHook(ctx context.Context, rc *runContext) {
	env := buildHookEnv(rc, hooks.PhaseNone, "")
	env.IterJSON = rc.paths.json
	env.PromptFile = rc.paths.prompt
	f, oerr := os.Open(rc.paths.json) //nolint:gosec // path joined from rc.repo + state-controlled stem
	if oerr != nil {
		rc.log.WarnContext(ctx, "post-iteration hook: open iter json", "err", oerr)
		return
	}
	_, hErr := hooks.Run(ctx, hooks.GlobalPath(rc.repo, "post-iteration"), env, f, nil, nil)
	_ = f.Close()
	if hErr != nil {
		rc.log.WarnContext(ctx, "post-iteration hook error", "err", hErr)
	}
}

// routeAndPersist computes the bd diff, routes to the next state, fires
// the exit hook on a transition, and persists every iteration artifact:
// the FSM, the summary row (+ ErrOut mirror), the transition row, and any
// incident. Returns the new outcome. On a routing error it returns prev;
// on a persist error it returns next so the caller surfaces the partial
// transition. This is the natural site for a future live-run Observer.
func routeAndPersist(ctx context.Context, rc *runContext, prev fsm.Outcome, beforeBD *bd.Snapshot, sess *runner.Session, mode runner.Mode, gate gateOutcome, commits int, ts time.Time) (fsm.Outcome, error) {
	// Compute bd diff for the narrative.
	afterBD := snapshotBD(ctx, rc)
	diff := bd.DiffSnapshots(beforeBD, afterBD)

	// Sample the ready-queue depth for the live UI's header. This is the
	// `ready` half of the done{queue_empty} routing below (which also requires
	// no in-progress issues), honoring the same exclude_types filter; the
	// header's terminal badge keys off the authoritative FSM state, not this
	// count. afterBD can't be reused: `bd ready` respects blockers/dependencies
	// a status map doesn't express. Best-effort — on error keep the last value.
	if n, rerr := fsm.BDReadyCount(ctx, rc.bdClient, ""); rerr == nil {
		rc.lastReadyBeads = n
	}

	// Route to the next state.
	runnerFailure := classifyToReason(mode, rc.deadStreak, rc.cfg.Backoff.DeadSessionThreshold)
	next, err := fsm.SelectNextState(ctx, fsm.RouteInput{
		FSM:           rc.fsm,
		Cfg:           rc.cfg,
		BD:            rc.bdClient,
		Repo:          rc.repo,
		RunnerFailure: runnerFailure,
	})
	if err != nil {
		return prev, fmt.Errorf("loop: select next state: %w", err)
	}

	// Update counters *before* assigning the new state.
	rc.fsm.ObserveTransition(next.State)

	// Exit hook for the old state when leaving it.
	if next.State != prev.State {
		env := buildHookEnv(rc, hooks.PhaseExit, string(next.State))
		if _, hErr := hooks.Run(ctx, hooks.StatePath(rc.repo, string(prev.State), hooks.PhaseExit), env, nil, nil, nil); hErr != nil {
			rc.log.WarnContext(ctx, "exit hook error", "state", prev.State, "err", hErr)
		}
	}

	// Persist FSM with the new outcome.
	rc.fsm.Outcome = next
	if gate.Result != "" {
		rc.fsm.LastGateResult = gate.Result
	}
	// Track the gate-failure streak. Only passed/failed are real "the gate
	// ran" signals; skipped/not-run leave the streak untouched.
	switch gate.Result {
	case narrative.GatePassed:
		rc.fsm.ObserveGate(true, true)
	case narrative.GateFailed:
		rc.fsm.ObserveGate(true, false)
	}
	if err := rc.fsm.Save(rc.repo); err != nil {
		return next, fmt.Errorf("loop: save fsm: %w", err)
	}

	// Compose narrative + append summary.jsonl + transitions.jsonl.
	currentHead, _ := git.HeadSHA(ctx, rc.repo)
	rec := composeIterRecord(rc, prev, next, sess, mode, gate, diff, commits, ts, currentHead)
	if werr := rc.sum.Write(rec); werr != nil {
		rc.log.ErrorContext(ctx, "write summary failed", "err", werr)
	}
	emitIterLine(rc, rec)
	if werr := rc.run.AppendTransition(runs.Transition{
		TS:           ts,
		Iter:         rec.Iter,
		From:         string(prev.State),
		To:           string(next.State),
		Reason:       string(next.Reason),
		RunnerMode:   string(mode),
		GateResult:   gate.Result,
		CostUSDDelta: rec.CostUSD,
	}); werr != nil {
		rc.log.ErrorContext(ctx, "append transition failed", "err", werr)
	}

	// Write an incident if this transition triggers one.
	if werr := writeIncidentIfNeeded(rc, prev, next, mode, gate.Result, rec.IterID); werr != nil {
		rc.log.ErrorContext(ctx, "incident write failed", "err", werr)
	}
	rc.lastGateResult = gate.Result
	rc.lastGateStdoutFile = gate.StdoutFile

	return next, nil
}

// sleepBackoff sleeps the inter-iteration backoff, skipped on a terminal
// outcome so Run can exit cleanly.
func sleepBackoff(ctx context.Context, rc *runContext, next fsm.Outcome, mode runner.Mode, sess *runner.Session) {
	if next.State.Terminal() {
		return
	}
	if d := composeBackoff(rc, mode, sess); d > 0 {
		rc.clock.Sleep(ctx, d)
	}
}

// waitOnQuota records a non-productive quota-wait row, sleeps a bounded,
// ctx-cancellable interval, then returns prev so the loop resumes the
// same state. The sleep is the opt-in alternative to routing ModeQuota
// to failed{runner_terminal}; see the call site in runIteration. State
// is not advanced (prev == rc.fsm.Outcome), so no FSM save is needed —
// the same iteration re-runs once the cap (hopefully) resets.
//
// When the runner surfaced a "resets ...pm (UTC)" hint (in the envelope
// result or stderr) the wait sleeps until that instant, capped at
// MaxQuotaWait; otherwise it falls back to the blind poll interval.
func waitOnQuota(ctx context.Context, rc *runContext, prev fsm.Outcome, sess *runner.Session) (fsm.Outcome, error) {
	d := backoff.QuotaWait(rc.cfg.Loop.QuotaWaitSecs)
	if sess != nil {
		hint := sess.Stderr
		if sess.Envelope != nil {
			hint = sess.Envelope.Result + "\n" + hint
		}
		if reset := backoff.ParseRateLimitReset(hint, rc.clock.Now().UTC()); reset > 0 {
			d = backoff.CapQuotaWait(reset)
		}
	}
	// Round to whole seconds so the log line, the quota-wait narrative, the
	// recorded QuotaWaitSecs, and the TUI countdown all agree (a parsed
	// reset otherwise carries sub-second precision from time.Now).
	d = d.Round(time.Second)
	rc.log.WarnContext(ctx, "quota cap hit; waiting before resuming",
		"state", prev.State, "wait", d.String())
	if err := recordQuotaWait(rc, prev, d); err != nil {
		return prev, err
	}
	rc.clock.Sleep(ctx, d)
	return prev, nil
}

// recordQuotaWait writes a summary row for a quota-wait tick. The runner
// ran and hit the cap, so RunnerMode is quota; Skipped marks the tick as
// non-productive for tooling that filters such rows.
func recordQuotaWait(rc *runContext, prev fsm.Outcome, d time.Duration) error {
	now := rc.clock.Now().UTC()
	rec := IterRecord{
		Iter:          rc.fsm.Iter,
		IterID:        rc.paths.stem,
		Timestamp:     now.Format(time.RFC3339),
		State:         string(prev.State),
		PrevState:     string(prev.State),
		RunnerMode:    string(runner.ModeQuota),
		Narrative:     fmt.Sprintf("%s: quota cap — waiting %s before resuming", prev.State, d),
		Skipped:       "quota-wait",
		QuotaWaitSecs: int(d.Round(time.Second).Seconds()),
	}
	if err := rc.sum.Write(rec); err != nil {
		return fmt.Errorf("loop: write quota-wait: %w", err)
	}
	emitIterLine(rc, rec)
	return nil
}

// recordSkippedIteration writes a minimal summary record for ticks the
// pre-iteration hook short-circuited. Keeps iter counters honest.
func recordSkippedIteration(rc *runContext, prev fsm.Outcome, reason string) error {
	now := rc.clock.Now().UTC()
	rec := IterRecord{
		Iter:      rc.fsm.Iter,
		IterID:    rc.paths.stem,
		Timestamp: now.Format(time.RFC3339),
		State:     string(prev.State),
		PrevState: string(prev.State),
		Narrative: fmt.Sprintf("%s: skipped (%s)", prev.State, reason),
		Skipped:   reason,
	}
	if err := rc.sum.Write(rec); err != nil {
		return fmt.Errorf("loop: write skipped: %w", err)
	}
	emitIterLine(rc, rec)
	return nil
}

// emitIterLine surfaces one iteration's IterRecord: it notifies the
// live-run Observer, then writes one progress line to ErrOut in the same
// shape `ralph logs` renders by default. It is the single chokepoint every
// IterRecord passes through — normal, skipped, and quota-wait ticks all
// call it — so the Observe call here fires exactly once per iteration.
// When the stderr-gated color scheme is enabled, the gate token is colored;
// the summary.jsonl row is untouched — only the bytes printed here carry
// any ANSI codes. The Observer is notified before the ErrOut guard so a nil
// IOStreams still produces a Snapshot.
func emitIterLine(rc *runContext, rec IterRecord) {
	notifyObserver(rc, rec)
	if rc.io == nil || rc.io.ErrOut == nil {
		return
	}
	cs := rc.io.ErrColorScheme()
	text := rec.Narrative
	if cs != nil && cs.Enabled() {
		text = strings.Replace(text, "gate green", "gate "+cs.Green("green"), 1)
		text = strings.Replace(text, "gate red", "gate "+cs.Red("red"), 1)
	}
	_, _ = fmt.Fprintf(rc.io.ErrOut, "iter %04d  %s\n", rec.Iter, text)
}

// notifyObserver hands the Observer a Snapshot built from the iteration's
// record, the current cumulative FSM counters, the configured caps, and
// the wallclock elapsed since Run started. Reads fsm/cfg through rc so the
// values are whatever the calling site has already persisted: in
// routeAndPersist the FSM is at the new state; on skipped/quota-wait ticks
// it is unchanged, which is the honest view for those rows.
func notifyObserver(rc *runContext, rec IterRecord) {
	if rc.observer == nil {
		return
	}
	rc.observer.Observe(Snapshot{
		Record:                  rec,
		Iter:                    rc.fsm.Iter,
		State:                   rc.fsm.State,
		Reason:                  rc.fsm.Reason,
		CumulativeCostUSD:       rc.fsm.CumulativeCostUSD,
		CumulativeWallclockSecs: rc.fsm.CumulativeWallclockSecs,
		ConsecutiveDirty:        rc.fsm.ConsecutiveDirty,
		LastGateResult:          rc.fsm.LastGateResult,
		MaxIterations:           rc.cfg.Loop.MaxIterations,
		MaxCostUSD:              rc.cfg.Budget.MaxCostUSD,
		MaxWallclockSecs:        rc.cfg.Budget.MaxWallclockSecs,
		Elapsed:                 rc.clock.Now().Sub(rc.started),
		ReadyBeads:              rc.lastReadyBeads,
	})
}

// composePrompt renders the per-state prompt with the iteration's vars.
func composePrompt(ctx context.Context, rc *runContext, prev fsm.Outcome, headSHA string) (string, error) {
	clean, _ := git.Clean(ctx, rc.repo)
	vars := promptlib.Vars{
		Iter:           rc.fsm.Iter,
		State:          string(prev.State),
		PrevState:      string(prev.State),
		GitDirty:       !clean,
		GitHead:        headSHA,
		RepoRoot:       rc.repo,
		GateResult:     rc.lastGateResult,
		GateOutput:     gateOutputTail(rc.lastGateStdoutFile),
		GateFailStreak: rc.fsm.ConsecutiveGateFail,
		Review: promptlib.ReviewVars{
			Branch: rc.fsm.ReviewBranch,
			Base:   rc.fsm.ReviewBase,
		},
		Beads: promptlib.BeadsVarsFromExcludeTypes(rc.cfg.Beads.ExcludeTypes),
	}
	root, err := promptlib.Open(rc.repo)
	if err != nil {
		return "", fmt.Errorf("loop: open prompts: %w", err)
	}
	defer func() { _ = root.Close() }()
	out, err := promptlib.Render(root.FS(), string(prev.State), vars)
	if err != nil {
		return "", fmt.Errorf("loop: render prompt: %w", err)
	}
	return out, nil
}

// gateOutcome bundles what runGate produced: the narrative gate-result
// constant plus the on-disk paths for the hook's stdout/stderr (empty
// when the hook didn't actually run).
type gateOutcome struct {
	Result     string
	StdoutFile string
	StderrFile string
}

// runGate runs the per-state gate hook and returns the outcome.
// Honors --skip-gate and cfg.Gate.RunWhen. When the hook actually
// executes, its stdout/stderr stream to disk (rc.paths.gate{Stdout,Stderr})
// and to the operator's terminal — same pattern as the runner subprocess.
func runGate(ctx context.Context, rc *runContext, prev fsm.Outcome, commits int) gateOutcome {
	if rc.opts.SkipGate {
		return gateOutcome{Result: narrative.GateSkipped}
	}
	switch rc.cfg.Gate.RunWhen {
	case "never":
		return gateOutcome{Result: narrative.GateSkipped}
	case "commits-only":
		if commits == 0 {
			return gateOutcome{Result: narrative.GateNotRun}
		}
	}

	gateCtx := ctx
	var cancel context.CancelFunc
	if rc.cfg.Gate.TimeoutSecs > 0 {
		gateCtx, cancel = context.WithTimeout(ctx, time.Duration(rc.cfg.Gate.TimeoutSecs)*time.Second)
		defer cancel()
	}
	env := buildHookEnv(rc, hooks.PhaseGate, "")
	env.PromptFile = rc.paths.prompt

	outW, errW, closeStreams, ferr := openGateStreams(ctx, rc)
	if ferr != nil {
		return gateOutcome{Result: narrative.GateFailed}
	}
	defer closeStreams()

	res, err := hooks.Run(gateCtx, hooks.StatePath(rc.repo, string(prev.State), hooks.PhaseGate), env, nil, outW, errW)
	if err != nil {
		rc.log.WarnContext(ctx, "gate hook error", "state", prev.State, "err", err)
		return gateOutcome{Result: narrative.GateFailed, StdoutFile: rc.paths.gateStdout, StderrFile: rc.paths.gateStderr}
	}
	if res.NoHook {
		// No artifact when the hook isn't present — caller has nothing
		// useful to surface.
		_ = os.Remove(rc.paths.gateStdout)
		_ = os.Remove(rc.paths.gateStderr)
		return gateOutcome{Result: narrative.GateNotRun}
	}
	result := narrative.GateFailed
	if res.ExitCode == 0 {
		result = narrative.GatePassed
	}
	return gateOutcome{
		Result:     result,
		StdoutFile: rc.paths.gateStdout,
		StderrFile: rc.paths.gateStderr,
	}
}

// openGateStreams creates the per-iteration gate stdout/stderr artifact
// files and returns writers that also tee to the operator terminal when
// IOStreams is present, plus a closer for the underlying files. Files are
// created up-front; if the hook doesn't exist they stay empty zero-byte
// files, but they're cheap and keep the on-disk artifact set consistent.
// On a create failure it logs and returns a non-nil error; the caller
// maps that to GateFailed.
func openGateStreams(ctx context.Context, rc *runContext) (io.Writer, io.Writer, func(), error) {
	outFile, err := os.Create(rc.paths.gateStdout)
	if err != nil {
		rc.log.WarnContext(ctx, "gate stdout create", "err", err)
		return nil, nil, nil, err
	}
	errFile, err := os.Create(rc.paths.gateStderr)
	if err != nil {
		_ = outFile.Close()
		rc.log.WarnContext(ctx, "gate stderr create", "err", err)
		return nil, nil, nil, err
	}
	closeStreams := func() {
		_ = outFile.Close()
		_ = errFile.Close()
	}
	var outW io.Writer = outFile
	var errW io.Writer = errFile
	if rc.io != nil {
		if rc.io.Out != nil {
			outW = io.MultiWriter(outFile, rc.io.Out)
		}
		if rc.io.ErrOut != nil {
			errW = io.MultiWriter(errFile, rc.io.ErrOut)
		}
	}
	return outW, errW, closeStreams, nil
}

// buildHookEnv produces hooks.Env consistent across all invocations in
// one iteration. RALPH_NEXT_STATE is set only when phase==exit, per
// the hooks contract.
func buildHookEnv(rc *runContext, phase hooks.Phase, nextState string) hooks.Env {
	e := hooks.Env{
		Repo:       rc.repo,
		Iter:       rc.fsm.Iter,
		State:      string(rc.fsm.State),
		PromptFile: rc.paths.prompt,
	}
	if phase == hooks.PhaseExit {
		e.NextState = nextState
	}
	if rc.fsm.ReviewMode {
		e.ReviewBranch = rc.fsm.ReviewBranch
		e.ReviewBase = rc.fsm.ReviewBase
	}
	return e
}

// classifyToReason maps a runner.Mode (plus the dead-session streak)
// to an fsm.Reason. ModeAuth → ReasonAuth; ModeBudget and ModeQuota at
// the runner level → ReasonRunnerTerminal (fsm.ReasonBudget is reserved
// for ralph's own cost cap; ModeBudget/ModeQuota stay distinguishable
// via Session.Mode in the iteration record). ModeDeadSession crossing
// the threshold also escalates. Everything else → ReasonNone.
func classifyToReason(mode runner.Mode, deadStreak, threshold int) fsm.Reason {
	switch mode {
	case runner.ModeAuth:
		return fsm.ReasonAuth
	case runner.ModeBudget, runner.ModeQuota:
		return fsm.ReasonRunnerTerminal
	}
	if threshold <= 0 {
		threshold = 3
	}
	if mode == runner.ModeDeadSession && deadStreak >= threshold {
		return fsm.ReasonRunnerTerminal
	}
	return fsm.ReasonNone
}

// updateStreaks bumps loop-owned counters based on this iteration's
// runner outcome.
func updateStreaks(rc *runContext, mode runner.Mode) {
	switch mode {
	case runner.ModeOK:
		rc.consecFailures = 0
		rc.deadStreak = 0
	case runner.ModeDeadSession:
		rc.consecFailures++
		rc.deadStreak++
	default:
		rc.consecFailures++
		rc.deadStreak = 0
	}
}

// updateCumulatives adds session cost + wallclock to the FSM totals.
func updateCumulatives(rc *runContext, sess *runner.Session) {
	if sess == nil {
		return
	}
	if sess.Envelope != nil {
		rc.fsm.CumulativeCostUSD += sess.Envelope.TotalCostUSD
	}
	rc.fsm.CumulativeWallclockSecs += int(sess.Duration / time.Second)
}

// composeBackoff turns a runner mode + session into a sleep duration.
func composeBackoff(rc *runContext, mode runner.Mode, sess *runner.Session) time.Duration {
	rlReset := time.Duration(0)
	if mode == runner.ModeRateLimit && sess != nil {
		rlReset = backoff.ParseRateLimitReset(sess.Stderr, rc.clock.Now().UTC())
	}
	return backoff.Compute(backoff.Input{
		Mode:           mode,
		Session:        sess,
		Streaks:        backoff.Streaks{ConsecutiveFailures: rc.consecFailures, DeadSession: rc.deadStreak},
		RateLimitReset: rlReset,
	}, &rc.cfg.Backoff)
}

// composeIterRecord builds the JSONL summary row.
func composeIterRecord(rc *runContext, prev, next fsm.Outcome, sess *runner.Session, mode runner.Mode, gate gateOutcome, diff bd.Diff, commits int, ts time.Time, headSHA string) IterRecord {
	failureMode := ""
	if next.State == fsm.StateFailed {
		failureMode = string(mode)
	}
	narrText := narrative.Compose(narrative.Input{
		Prev:        prev,
		Next:        next,
		Diff:        diff,
		Commits:     commits,
		Gate:        gate.Result,
		FailureMode: failureMode,
	})
	rec := IterRecord{
		Iter:       rc.fsm.Iter,
		IterID:     rc.paths.stem,
		Timestamp:  ts.Format(time.RFC3339),
		State:      string(next.State),
		Reason:     string(next.Reason),
		PrevState:  string(prev.State),
		Narrative:  narrText,
		RunnerMode: string(mode),
		GateResult: gate.Result,
		Commits:    commits,
		GitHead:    headSHA,
		PromptFile: relPathOrAbs(rc.repo, rc.paths.prompt),
	}
	if gate.StdoutFile != "" {
		rec.GateStdoutFile = relPathOrAbs(rc.repo, gate.StdoutFile)
	}
	if gate.StderrFile != "" {
		rec.GateStderrFile = relPathOrAbs(rc.repo, gate.StderrFile)
	}
	if sess != nil {
		if sess.Envelope != nil {
			rec.CostUSD = sess.Envelope.TotalCostUSD
		}
		rec.DurationSecs = sess.Duration.Seconds()
	}
	if !diffIsEmpty(diff) {
		d := diff
		rec.BDDiff = &d
	}
	return rec
}

// writeIncidentIfNeeded fires incidents.Write when a transition matches
// one of the four trigger kinds. Priority: terminal-failure > revert >
// gate-regression > dead-streak.
func writeIncidentIfNeeded(rc *runContext, prev, next fsm.Outcome, mode runner.Mode, gateResult, iterID string) error {
	_ = prev // explicit: not all triggers consult prev today
	switch {
	case next.State == fsm.StateFailed:
		return writeIncident(rc, incidents.KindTerminalFailure, iterID,
			fmt.Sprintf("terminal failure: %s", next.Reason))
	case next.State == fsm.StateRevert:
		return writeIncident(rc, incidents.KindRevert, iterID,
			fmt.Sprintf("auto-revert after %d consecutive dirty iterations", rc.fsm.ConsecutiveDirty))
	case rc.lastGateResult == narrative.GatePassed && gateResult == narrative.GateFailed:
		return writeIncident(rc, incidents.KindGateRegression, iterID,
			fmt.Sprintf("gate regressed from passed to failed at iter %d", rc.fsm.Iter))
	case mode == runner.ModeDeadSession && rc.cfg.Backoff.DeadSessionThreshold > 0 && rc.deadStreak == rc.cfg.Backoff.DeadSessionThreshold:
		return writeIncident(rc, incidents.KindDeadStreak, iterID,
			fmt.Sprintf("dead session #%d hit threshold %d", rc.deadStreak, rc.cfg.Backoff.DeadSessionThreshold))
	}
	return nil
}

// writeIncident persists a single incident at the current iteration.
func writeIncident(rc *runContext, kind incidents.Kind, iterID, summary string) error {
	_, err := incidents.Write(rc.repo, incidents.Incident{
		Kind:      kind,
		Iter:      rc.fsm.Iter,
		Summary:   summary,
		IterIDs:   []string{iterID},
		Timestamp: rc.clock.Now(),
	})
	return err
}

// snapshotBD is a best-effort Snapshot — errors are logged but the
// loop continues with a nil snapshot.
func snapshotBD(ctx context.Context, rc *runContext) *bd.Snapshot {
	snap, err := rc.bdClient.Snapshot(ctx)
	if err != nil {
		rc.log.WarnContext(ctx, "bd snapshot failed", "err", err)
		return nil
	}
	return snap
}

// writeIterJSON marshals the record and writes it atomically.
func writeIterJSON(path string, rec IterRecord) error {
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("loop: marshal iter record: %w", err)
	}
	b = append(b, '\n')
	return writeAtomic(path, b)
}

// writeAtomic writes b to path via internal/atomicfile, creating the parent
// directory if absent (callers in this package may target a fresh logs dir).
func writeAtomic(path string, b []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("loop: mkdir %s: %w", dir, err)
	}
	if err := atomicfile.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("loop: %w", err)
	}
	return nil
}

func relPathOrAbs(repo, p string) string {
	if r, err := filepath.Rel(repo, p); err == nil {
		return r
	}
	return p
}

func diffIsEmpty(d bd.Diff) bool {
	return len(d.Created) == 0 && len(d.Closed) == 0 && len(d.Opened) == 0 &&
		len(d.Deferred) == 0 && len(d.InProgress) == 0 && len(d.Blocked) == 0
}

// gateOutputTail reads at most gateTailBytes from the end of the gate
// stdout file at path and returns the last gateTailLines lines of it.
// Returns "" when path is empty, the file doesn't exist, or the file is
// empty — callers treat empty {{.GateOutput}} as "no prior gate run".
//
// Reads from the end with os.File.Seek so multi-MB benchmark logs don't
// blow memory: the cap is the read size, not the file size.
func gateOutputTail(path string) string {
	if path == "" {
		return ""
	}
	const gateTailBytes = 4096
	const gateTailLines = 50
	f, err := os.Open(path) //nolint:gosec // path is loop-owned state/logs file
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return ""
	}
	size := info.Size()
	readFrom := int64(0)
	if size > gateTailBytes {
		readFrom = size - gateTailBytes
	}
	if _, err := f.Seek(readFrom, 0); err != nil {
		return ""
	}
	buf := make([]byte, size-readFrom)
	if _, err := io.ReadFull(f, buf); err != nil {
		return ""
	}
	s := string(buf)
	// When we truncated mid-line, drop the partial leading line so the
	// agent sees only complete lines.
	if readFrom > 0 {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
	}
	lines := strings.Split(s, "\n")
	if len(lines) > gateTailLines {
		lines = lines[len(lines)-gateTailLines:]
	}
	return strings.Join(lines, "\n")
}
