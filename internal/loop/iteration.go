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
	logsDir := filepath.Join(repo, ".ralph", "state", "logs")
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
	preEnv := buildHookEnv(rc, hooks.PhaseNone, "")
	preEnv.PromptFile = "" // not rendered yet
	preRes, err := hooks.Run(ctx, hooks.GlobalPath(rc.repo, "pre-iteration"), preEnv, nil, nil, nil)
	if err != nil {
		return prev, fmt.Errorf("loop: pre-iteration: %w", err)
	}
	if !preRes.NoHook && preRes.ExitCode != 0 {
		rc.log.WarnContext(ctx, "pre-iteration skipped iteration", "exit", preRes.ExitCode)
		return prev, recordSkippedIteration(rc, prev, fmt.Sprintf("pre-iteration exit %d", preRes.ExitCode))
	}

	// 2. Per-state enter hook (only on entry into a new state).
	if rc.lastEnteredState != prev.State {
		env := buildHookEnv(rc, hooks.PhaseEnter, "")
		_, hErr := hooks.Run(ctx, hooks.StatePath(rc.repo, string(prev.State), hooks.PhaseEnter), env, nil, nil, nil)
		if hErr != nil {
			rc.log.WarnContext(ctx, "enter hook error", "state", prev.State, "err", hErr)
		}
		rc.lastEnteredState = prev.State
	}

	// 3. Render the prompt and capture it to disk.
	prompt, err := composePrompt(ctx, rc, prev, beforeHead)
	if err != nil {
		return prev, err
	}
	if werr := writeAtomic(rc.paths.prompt, []byte(prompt)); werr != nil {
		return prev, fmt.Errorf("loop: capture prompt: %w", werr)
	}

	// 4. Run the runner (skipped on --dry-run).
	var (
		sess *runner.Session
		mode = runner.ModeOK
	)
	if !rc.opts.DryRun {
		sessCtx := ctx
		var cancel context.CancelFunc
		if rc.cfg.Loop.SessionTimeoutSecs > 0 {
			sessCtx, cancel = context.WithTimeout(ctx, time.Duration(rc.cfg.Loop.SessionTimeoutSecs)*time.Second)
			defer cancel()
		}
		var stdoutTee, stderrTee io.Writer
		if rc.io != nil {
			stdoutTee, stderrTee = rc.io.Out, rc.io.ErrOut
		}
		sess, err = rc.runr.Run(sessCtx, runner.RunOpts{
			Prompt:     prompt,
			Cwd:        rc.repo,
			StdoutPath: rc.paths.stdout,
			StderrPath: rc.paths.stderr,
			StdoutTee:  stdoutTee,
			StderrTee:  stderrTee,
		})
		if err != nil {
			return prev, fmt.Errorf("loop: runner start: %w", err)
		}
		mode = runner.Classify(sess)
		updateStreaks(rc, mode)
		updateCumulatives(rc, sess)
	}

	// 5. Gate hook (post-runner, pre-routing).
	commits := 0
	if beforeHead != "" {
		if n, cerr := git.CountCommits(ctx, rc.repo, beforeHead, "HEAD"); cerr == nil {
			commits = n
		}
	}
	gate := runGate(ctx, rc, prev, commits)
	gateResult := gate.Result

	// 6. Write the iter JSON (post-iteration sees this on stdin + env).
	preJSON := composeIterRecord(rc, prev, prev, sess, mode, gate, bd.Diff{}, commits, now, beforeHead)
	if werr := writeIterJSON(rc.paths.json, preJSON); werr != nil {
		return prev, fmt.Errorf("loop: write iter json: %w", werr)
	}

	// 7. Global post-iteration hook (stdin = iter json).
	postEnv := buildHookEnv(rc, hooks.PhaseNone, "")
	postEnv.IterJSON = rc.paths.json
	postEnv.PromptFile = rc.paths.prompt
	if f, oerr := os.Open(rc.paths.json); oerr == nil { //nolint:gosec // path joined from rc.repo + state-controlled stem
		_, _ = hooks.Run(ctx, hooks.GlobalPath(rc.repo, "post-iteration"), postEnv, f, nil, nil)
		_ = f.Close()
	}

	// 8. Compute bd diff for narrative.
	afterBD := snapshotBD(ctx, rc)
	diff := bd.DiffSnapshots(beforeBD, afterBD)

	// 9. Route to next state.
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

	// 10. Update counters *before* assigning the new state.
	rc.fsm.ObserveTransition(next.State)

	// 11. Exit hook for the old state when leaving it.
	if next.State != prev.State {
		env := buildHookEnv(rc, hooks.PhaseExit, string(next.State))
		_, hErr := hooks.Run(ctx, hooks.StatePath(rc.repo, string(prev.State), hooks.PhaseExit), env, nil, nil, nil)
		if hErr != nil {
			rc.log.WarnContext(ctx, "exit hook error", "state", prev.State, "err", hErr)
		}
	}

	// 12. Persist FSM with the new outcome.
	rc.fsm.Outcome = next
	if gateResult != "" {
		rc.fsm.LastGateResult = gateResult
	}
	if err := rc.fsm.Save(rc.repo); err != nil {
		return next, fmt.Errorf("loop: save fsm: %w", err)
	}

	// 13. Compose narrative + append summary.jsonl + transitions.jsonl.
	currentHead, _ := git.HeadSHA(ctx, rc.repo)
	rec := composeIterRecord(rc, prev, next, sess, mode, gate, diff, commits, now, currentHead)
	if werr := rc.sum.Write(rec); werr != nil {
		rc.log.ErrorContext(ctx, "write summary failed", "err", werr)
	}
	emitIterLine(rc, rec)
	if werr := rc.run.AppendTransition(runs.Transition{
		TS:           now,
		Iter:         rec.Iter,
		From:         string(prev.State),
		To:           string(next.State),
		Reason:       string(next.Reason),
		RunnerMode:   string(mode),
		GateResult:   gateResult,
		CostUSDDelta: rec.CostUSD,
	}); werr != nil {
		rc.log.ErrorContext(ctx, "append transition failed", "err", werr)
	}

	// 14. Write an incident if this transition triggers one.
	if werr := writeIncidentIfNeeded(rc, prev, next, mode, gateResult, rec.IterID); werr != nil {
		rc.log.ErrorContext(ctx, "incident write failed", "err", werr)
	}
	rc.lastGateResult = gateResult
	rc.lastGateStdoutFile = gate.StdoutFile

	// 15. Backoff sleep — skipped on terminal so Run can exit cleanly.
	if !next.State.Terminal() {
		d := composeBackoff(rc, mode, sess)
		if d > 0 {
			rc.clock.Sleep(ctx, d)
		}
	}

	return next, nil
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

// emitIterLine writes one progress line per iteration to ErrOut, in the
// same shape `ralph logs` renders by default. When the attached color
// scheme is enabled, the gate token is colored. The summary.jsonl row
// is untouched — only the bytes printed here carry any ANSI codes.
func emitIterLine(rc *runContext, rec IterRecord) {
	if rc.io == nil || rc.io.ErrOut == nil {
		return
	}
	cs := rc.io.ColorScheme()
	text := rec.Narrative
	if cs != nil && cs.Enabled() {
		text = strings.Replace(text, "gate green", "gate "+cs.Green("green"), 1)
		text = strings.Replace(text, "gate red", "gate "+cs.Red("red"), 1)
	}
	_, _ = fmt.Fprintf(rc.io.ErrOut, "iter %04d  %s\n", rec.Iter, text)
}

// composePrompt renders the per-state prompt with the iteration's vars.
func composePrompt(ctx context.Context, rc *runContext, prev fsm.Outcome, headSHA string) (string, error) {
	clean, _ := git.Clean(ctx, rc.repo)
	vars := promptlib.Vars{
		Iter:       rc.fsm.Iter,
		State:      string(prev.State),
		PrevState:  string(prev.State),
		GitDirty:   !clean,
		GitHead:    headSHA,
		RepoRoot:   rc.repo,
		GateResult: rc.lastGateResult,
		GateOutput: gateOutputTail(rc.lastGateStdoutFile),
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

	// Stream to disk (always) + terminal (when IOStreams present).
	// Files are created up-front; if the hook doesn't exist they stay
	// empty zero-byte files, but they're cheap and keep the artifact
	// set on disk consistent.
	outFile, ferr := os.Create(rc.paths.gateStdout)
	if ferr != nil {
		rc.log.WarnContext(ctx, "gate stdout create", "err", ferr)
		return gateOutcome{Result: narrative.GateFailed}
	}
	defer func() { _ = outFile.Close() }()
	errFile, ferr := os.Create(rc.paths.gateStderr)
	if ferr != nil {
		rc.log.WarnContext(ctx, "gate stderr create", "err", ferr)
		return gateOutcome{Result: narrative.GateFailed}
	}
	defer func() { _ = errFile.Close() }()
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
