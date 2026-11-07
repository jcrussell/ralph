package loop

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jcrussell/ralph/internal/bd"
	"github.com/jcrussell/ralph/internal/fsm"
	"github.com/jcrussell/ralph/internal/runner"
)

// Dead sessions carry the runner's exit code, signal, OOM verdict and
// stderr tail into the summary row, the iteration .json, the narrative
// and the incident, so the cause can be read after the fact.
func TestRun_DeadSessionRecordsDiagnostics(t *testing.T) {
	repo := scaffoldRepo(t)
	opts := baseOpts(t, repo)
	// The OOM session is its own mode; the two exit-1 sessions make the streak.
	opts.Cfg.Backoff.DeadSessionThreshold = 2
	opts.BD = &fakeBD{ReadyByLabel: map[string][]bd.Issue{"": {{ID: "x"}}}}
	scopeLine := "Running scope as unit: ralph-1-1.scope"
	opts.Runner = &fakeRunner{Sessions: []*runner.Session{
		// A healthy session first: exit 0 recorded, no stderr tail.
		{
			ExitCode: 0, Duration: time.Second, StderrTail: "chatter",
			Stdout:   `{"total_cost_usd":0.01,"num_turns":1,"subtype":"success"}`,
			Envelope: &runner.Envelope{TotalCostUSD: 0.01, NumTurns: 1, Subtype: "success", Raw: map[string]any{"subtype": "success"}},
		},
		{ExitCode: -1, Signal: "SIGKILL", OOMSignal: true, Duration: time.Minute, Stderr: scopeLine, StderrTail: scopeLine},
		{ExitCode: 1, Duration: time.Minute, Stderr: "crashed", StderrTail: "crashed"},
		{ExitCode: 1, Duration: time.Minute, Stderr: "crashed", StderrTail: "crashed"},
	}}
	opts.Clock = newFakeClock()

	out, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if (out != fsm.Outcome{State: fsm.StateFailed, Reason: fsm.ReasonRunnerTerminal}) {
		t.Fatalf("outcome = %v, want failed{runner_terminal}", out)
	}

	var rows []IterRecord
	for _, r := range readSummary(t, repo) {
		if r.Skipped == "" {
			rows = append(rows, r)
		}
	}
	if len(rows) != 4 {
		t.Fatalf("got %d iteration rows, want 4: %+v", len(rows), rows)
	}

	ok := rows[0]
	if ok.ExitCode == nil || *ok.ExitCode != 0 {
		t.Errorf("healthy row exit_code = %v, want 0", ok.ExitCode)
	}
	if ok.StderrTail != "" {
		t.Errorf("healthy row stderr_tail = %q, want empty", ok.StderrTail)
	}

	oom := rows[1]
	if oom.RunnerMode == string(runner.ModeOK) {
		t.Fatalf("row 1 runner_mode = ok")
	}
	if oom.Signal != "SIGKILL" || !oom.OOMKilled || oom.StderrTail != scopeLine {
		t.Errorf("signalled row = %+v", oom)
	}

	dead := rows[2]
	if dead.RunnerMode != string(runner.ModeDeadSession) {
		t.Fatalf("row 2 runner_mode = %q, want dead_session", dead.RunnerMode)
	}
	if dead.ExitCode == nil || *dead.ExitCode != 1 || dead.Signal != "" || dead.StderrTail != "crashed" {
		t.Errorf("dead row = %+v", dead)
	}
	if !strings.Contains(dead.Narrative, "dead session (exit 1)") {
		t.Errorf("dead narrative = %q, want dead session (exit 1)", dead.Narrative)
	}

	last := rows[3]
	if !strings.Contains(last.Narrative, "failure: dead_session (exit 1)") {
		t.Errorf("terminal narrative = %q", last.Narrative)
	}

	// The per-iteration .json carries the same fields (ralph trace).
	raw, err := os.ReadFile(filepath.Join(repo, ".ralph", "state", "logs", dead.IterID+".json"))
	if err != nil {
		t.Fatalf("read iter json: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["exit_code"] != float64(1) || m["stderr_tail"] != "crashed" {
		t.Errorf("iter json exit_code=%v stderr_tail=%v", m["exit_code"], m["stderr_tail"])
	}

	tf, _ := filepath.Glob(filepath.Join(repo, ".ralph", "state", "incidents", "*-terminal-failure.md"))
	if len(tf) != 1 {
		t.Fatalf("terminal-failure incidents = %v", tf)
	}
	body, _ := os.ReadFile(tf[0])
	if !strings.Contains(string(body), "(dead_session: exit 1)") {
		t.Errorf("incident does not name the cause:\n%s", body)
	}
}

func TestSessionDiag(t *testing.T) {
	for _, tc := range []struct {
		sess *runner.Session
		want string
	}{
		{nil, ""},
		{&runner.Session{ExitCode: 1}, "exit 1"},
		{&runner.Session{ExitCode: -1, Signal: "SIGKILL", OOMSignal: true}, "SIGKILL, oom"},
		{&runner.Session{ExitCode: -1, Signal: "SIGKILL", KilledByTimeout: true}, "SIGKILL, timeout"},
	} {
		if got := sessionDiag(tc.sess); got != tc.want {
			t.Errorf("sessionDiag(%+v) = %q, want %q", tc.sess, got, tc.want)
		}
	}
}

func TestRunnerMemLimitZeroDisables(t *testing.T) {
	for in, want := range map[string]string{"": "", "0": "", "0G": "", "7G": "7G", "512m": "512m"} {
		if got := runnerMemLimit(in); got != want {
			t.Errorf("runnerMemLimit(%q) = %q, want %q", in, got, want)
		}
	}
}
