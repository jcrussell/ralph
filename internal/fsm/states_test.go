package fsm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStateTerminal(t *testing.T) {
	cases := map[State]bool{
		StateStart:  false,
		StateClean:  false,
		StateDirty:  false,
		StateRevert: false,
		StateReview: false,
		StateDone:   true,
		StateFailed: true,
	}
	for s, want := range cases {
		if got := s.Terminal(); got != want {
			t.Errorf("%s.Terminal() = %v, want %v", s, got, want)
		}
	}
}

func TestStateValid(t *testing.T) {
	for _, s := range AllStates() {
		if !s.Valid() {
			t.Errorf("%s.Valid() = false, want true", s)
		}
	}
	if State("bogus").Valid() {
		t.Errorf(`State("bogus").Valid() = true, want false`)
	}
}

func TestAllStatesCount(t *testing.T) {
	// Locks the topology size — if you change it, change the plan too.
	if got, want := len(AllStates()), 7; got != want {
		t.Errorf("len(AllStates()) = %d, want %d", got, want)
	}
}

func TestAllStateNames(t *testing.T) {
	names := AllStateNames()
	states := AllStates()
	if len(names) != len(states) {
		t.Fatalf("len mismatch: names=%d states=%d", len(names), len(states))
	}
	for i, s := range states {
		if names[i] != string(s) {
			t.Errorf("names[%d] = %q, want %q", i, names[i], s)
		}
	}
}

func TestStateValidReasons(t *testing.T) {
	cases := []struct {
		s       State
		wantLen int
		want    []Reason
	}{
		{StateStart, 1, []Reason{ReasonNone}},
		{StateClean, 1, []Reason{ReasonNone}},
		{StateDone, 2, []Reason{ReasonQueueEmpty, ReasonIterCap}},
		{StateFailed, 3, []Reason{ReasonBudget, ReasonAuth, ReasonRunnerTerminal}},
	}
	for _, c := range cases {
		got := c.s.ValidReasons()
		if len(got) != c.wantLen {
			t.Errorf("%s.ValidReasons() len = %d, want %d (%v)", c.s, len(got), c.wantLen, got)
			continue
		}
		for i, r := range c.want {
			if got[i] != r {
				t.Errorf("%s.ValidReasons()[%d] = %q, want %q", c.s, i, got[i], r)
			}
		}
	}
}

func TestOutcomeString(t *testing.T) {
	cases := []struct {
		o    Outcome
		want string
	}{
		{Outcome{State: StateClean}, "clean"},
		{Outcome{State: StateDirty}, "dirty"},
		{Outcome{State: StateDone, Reason: ReasonQueueEmpty}, "done{queue_empty}"},
		{Outcome{State: StateDone, Reason: ReasonIterCap}, "done{iter_cap}"},
		{Outcome{State: StateFailed, Reason: ReasonBudget}, "failed{budget}"},
		{Outcome{State: StateFailed, Reason: ReasonAuth}, "failed{auth}"},
		{Outcome{State: StateFailed, Reason: ReasonRunnerTerminal}, "failed{runner_terminal}"},
	}
	for _, c := range cases {
		if got := c.o.String(); got != c.want {
			t.Errorf("Outcome %+v: String() = %q, want %q", c.o, got, c.want)
		}
	}
}

func TestOutcomeExitCode(t *testing.T) {
	cases := []struct {
		o    Outcome
		want int
	}{
		// Terminal done{*} → 0.
		{Outcome{State: StateDone, Reason: ReasonQueueEmpty}, 0},
		{Outcome{State: StateDone, Reason: ReasonIterCap}, 0},
		// Terminal failed{*} → 1.
		{Outcome{State: StateFailed, Reason: ReasonBudget}, 1},
		{Outcome{State: StateFailed, Reason: ReasonAuth}, 1},
		{Outcome{State: StateFailed, Reason: ReasonRunnerTerminal}, 1},
		// Non-terminal — permissive zero by design.
		{Outcome{State: StateClean}, 0},
		{Outcome{State: StateReview}, 0},
	}
	for _, c := range cases {
		if got := c.o.ExitCode(); got != c.want {
			t.Errorf("Outcome %s: ExitCode() = %d, want %d", c.o, got, c.want)
		}
	}
}

func TestOutcomeValidate(t *testing.T) {
	ok := []Outcome{
		{State: StateStart},
		{State: StateClean},
		{State: StateDirty},
		{State: StateRevert},
		{State: StateReview},
		{State: StateDone, Reason: ReasonQueueEmpty},
		{State: StateDone, Reason: ReasonIterCap},
		{State: StateFailed, Reason: ReasonBudget},
		{State: StateFailed, Reason: ReasonAuth},
		{State: StateFailed, Reason: ReasonRunnerTerminal},
	}
	for _, o := range ok {
		if err := o.Validate(); err != nil {
			t.Errorf("Validate(%s) = %v, want nil", o, err)
		}
	}

	bad := []struct {
		o       Outcome
		wantSub string
	}{
		{Outcome{State: "bogus"}, "unknown state"},
		{Outcome{State: StateDone}, "requires reason"},
		{Outcome{State: StateFailed}, "requires reason"},
		{Outcome{State: StateDone, Reason: ReasonBudget}, "requires reason"},
		{Outcome{State: StateFailed, Reason: ReasonQueueEmpty}, "requires reason"},
		{Outcome{State: StateClean, Reason: ReasonQueueEmpty}, "must carry no reason"},
		{Outcome{State: StateRevert, Reason: ReasonBudget}, "must carry no reason"},
	}
	for _, c := range bad {
		err := c.o.Validate()
		if err == nil {
			t.Errorf("Validate(%s) = nil, want error containing %q", c.o, c.wantSub)
			continue
		}
		if !strings.Contains(err.Error(), c.wantSub) {
			t.Errorf("Validate(%s) error = %q, want substring %q", c.o, err.Error(), c.wantSub)
		}
	}
}

func TestOutcomeJSONRoundTrip(t *testing.T) {
	cases := []Outcome{
		{State: StateClean},
		{State: StateDone, Reason: ReasonQueueEmpty},
		{State: StateFailed, Reason: ReasonRunnerTerminal},
	}
	for _, in := range cases {
		b, err := json.Marshal(in)
		if err != nil {
			t.Fatalf("Marshal(%s): %v", in, err)
		}
		var out Outcome
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatalf("Unmarshal(%s): %v", b, err)
		}
		if out != in {
			t.Errorf("round-trip: in=%+v out=%+v (json=%s)", in, out, b)
		}
		if err := out.Validate(); err != nil {
			t.Errorf("round-trip Validate(%s): %v", out, err)
		}
	}
}

func TestOutcomeJSONOmitEmptyReason(t *testing.T) {
	b, err := json.Marshal(Outcome{State: StateClean})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(b), "reason") {
		t.Errorf("expected reason to be omitted for non-terminal state, got %s", b)
	}
}
