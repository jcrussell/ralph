package loopexit

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jcrussell/ralph/internal/fsm"
	"github.com/jcrussell/ralph/internal/loop"
	"github.com/jcrussell/ralph/pkg/cmdutil"
)

func exitCode(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var ec *cmdutil.ExitCodeError
	if !errors.As(err, &ec) {
		t.Fatalf("err = %v, want nil or *ExitCodeError", err)
	}
	return ec.Code
}

func TestFinishText(t *testing.T) {
	var w strings.Builder
	err := Finish(context.Background(), &w, "", "", fsm.Outcome{State: fsm.StateFailed, Reason: fsm.ReasonRunnerTerminal}, nil)
	if got := exitCode(t, err); got != 22 {
		t.Errorf("exit = %d, want 22", got)
	}
	if want := "ralph: run ended: failed{runner_terminal} (exit 22)\n"; w.String() != want {
		t.Errorf("line = %q, want %q", w.String(), want)
	}
}

func TestFinishQueueEmptyExitsZero(t *testing.T) {
	var w strings.Builder
	if err := Finish(context.Background(), &w, "text", "", fsm.Outcome{State: fsm.StateDone, Reason: fsm.ReasonQueueEmpty}, nil); err != nil {
		t.Errorf("err = %v, want nil", err)
	}
	if !strings.Contains(w.String(), "done{queue_empty} (exit 0)") {
		t.Errorf("line = %q", w.String())
	}
}

// Under --log-format json the line is one JSON object with the totals
// read back from the repo's fsm.json.
func TestFinishJSONCarriesTotals(t *testing.T) {
	repo := t.TempDir()
	st := fsm.Fresh()
	st.Outcome = fsm.Outcome{State: fsm.StateDone, Reason: fsm.ReasonIdle}
	st.Iter = 7
	st.CumulativeCostUSD = 1.5
	if err := st.Save(repo); err != nil {
		t.Fatal(err)
	}
	var w strings.Builder
	err := Finish(context.Background(), &w, "json", repo, st.Outcome, nil)
	if got := exitCode(t, err); got != 10 {
		t.Errorf("exit = %d, want 10", got)
	}
	var l map[string]any
	if jerr := json.Unmarshal([]byte(w.String()), &l); jerr != nil {
		t.Fatalf("line is not JSON: %q: %v", w.String(), jerr)
	}
	want := map[string]any{"level": "INFO", "msg": "run ended", "state": "done", "reason": "idle",
		"exit_code": float64(10), "iter": float64(7), "cost_usd": 1.5}
	for k, v := range want {
		if l[k] != v {
			t.Errorf("%s = %v, want %v (line %q)", k, l[k], v, w.String())
		}
	}
	if _, ok := l["time"]; !ok {
		t.Errorf("no time in %q", w.String())
	}
	if strings.Count(w.String(), "\n") != 1 {
		t.Errorf("want exactly one line, got %q", w.String())
	}
}

func TestFinishErrors(t *testing.T) {
	var w strings.Builder
	if got := exitCode(t, Finish(context.Background(), &w, "", "", fsm.Outcome{}, loop.ErrTerminalState)); got != ExitRefusedTerminal {
		t.Errorf("terminal-state refusal exit = %d, want %d", got, ExitRefusedTerminal)
	}
	infra := errors.New("disk full")
	if err := Finish(context.Background(), &w, "", "", fsm.Outcome{}, infra); !errors.Is(err, infra) {
		t.Errorf("infra err = %v, want it passed through", err)
	}
	if w.Len() != 0 {
		t.Errorf("no outcome line expected without an outcome, got %q", w.String())
	}
}
