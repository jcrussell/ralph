package loop

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/jcrussell/ralph/internal/fsm"
)

// loadAndPrepareFSM is the pure FSM-preparation block extracted from Run
// (no resource acquisition). These unit tests exercise each branch directly,
// complementing the integration coverage in loop_test.go's TestRun_* cases.

func TestLoadAndPrepareFSM_FreshRepoLoadsStart(t *testing.T) {
	repo := scaffoldRepo(t)
	opts := baseOpts(t, repo)
	var errOut bytes.Buffer

	f, err := loadAndPrepareFSM(opts, &errOut)
	if err != nil {
		t.Fatalf("loadAndPrepareFSM: %v", err)
	}
	if f.State != fsm.StateStart {
		t.Errorf("State = %q, want %q", f.State, fsm.StateStart)
	}
	if errOut.Len() != 0 {
		t.Errorf("errOut = %q, want empty", errOut.String())
	}
}

func TestLoadAndPrepareFSM_FreshFlagResetsTerminal(t *testing.T) {
	repo := scaffoldRepo(t)
	seedTerminalFSM(t, repo, fsm.Outcome{State: fsm.StateFailed, Reason: fsm.ReasonRunnerTerminal})
	opts := baseOpts(t, repo)
	opts.Fresh = true
	var errOut bytes.Buffer

	f, err := loadAndPrepareFSM(opts, &errOut)
	if err != nil {
		t.Fatalf("loadAndPrepareFSM: %v", err)
	}
	if f.State != fsm.StateStart {
		t.Errorf("returned State = %q, want %q", f.State, fsm.StateStart)
	}
	// --fresh persists immediately so a crash can't resurrect terminal state.
	if persisted := fsmAt(t, repo); persisted.State != fsm.StateStart {
		t.Errorf("persisted State = %q, want %q", persisted.State, fsm.StateStart)
	}
}

func TestLoadAndPrepareFSM_DoneAutoResetsWithNotice(t *testing.T) {
	repo := scaffoldRepo(t)
	seedTerminalFSM(t, repo, fsm.Outcome{State: fsm.StateDone, Reason: fsm.ReasonIterCap})
	opts := baseOpts(t, repo)
	var errOut bytes.Buffer

	f, err := loadAndPrepareFSM(opts, &errOut)
	if err != nil {
		t.Fatalf("loadAndPrepareFSM: %v", err)
	}
	if f.State != fsm.StateStart {
		t.Errorf("returned State = %q, want %q (auto-reset)", f.State, fsm.StateStart)
	}
	if persisted := fsmAt(t, repo); persisted.State != fsm.StateStart {
		t.Errorf("persisted State = %q, want %q", persisted.State, fsm.StateStart)
	}
	if !strings.Contains(errOut.String(), "resetting fsm.json") {
		t.Errorf("errOut = %q, want a reset notice", errOut.String())
	}
}

func TestLoadAndPrepareFSM_FailedTerminalRefused(t *testing.T) {
	repo := scaffoldRepo(t)
	seedTerminalFSM(t, repo, fsm.Outcome{State: fsm.StateFailed, Reason: fsm.ReasonRunnerTerminal})
	opts := baseOpts(t, repo)
	var errOut bytes.Buffer

	f, err := loadAndPrepareFSM(opts, &errOut)
	if !errors.Is(err, ErrTerminalState) {
		t.Fatalf("err = %v, want ErrTerminalState", err)
	}
	if f != nil {
		t.Errorf("FSM = %+v, want nil on refusal", f)
	}
	// The failed state must NOT have been reset on disk.
	if persisted := fsmAt(t, repo); persisted.State != fsm.StateFailed {
		t.Errorf("persisted State = %q, want %q (untouched)", persisted.State, fsm.StateFailed)
	}
	if !strings.Contains(errOut.String(), "terminal state") {
		t.Errorf("errOut = %q, want a terminal-state notice", errOut.String())
	}
}

func TestLoadAndPrepareFSM_ReviewModeStampsFields(t *testing.T) {
	repo := scaffoldRepo(t)
	opts := baseOpts(t, repo)
	opts.ReviewMode = true
	opts.ReviewBranch = "feature"
	opts.ReviewBase = "main"
	var errOut bytes.Buffer

	f, err := loadAndPrepareFSM(opts, &errOut)
	if err != nil {
		t.Fatalf("loadAndPrepareFSM: %v", err)
	}
	if !f.ReviewMode || f.ReviewBranch != "feature" || f.ReviewBase != "main" {
		t.Errorf("review fields = {%v %q %q}, want {true feature main}",
			f.ReviewMode, f.ReviewBranch, f.ReviewBase)
	}
}
