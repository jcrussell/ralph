// Package fsm is the closed-core state machine that routes the ralph
// orchestrator. States, predicates, and the routing function are baked
// into the binary; .ralph/ customizes prompts and hooks within them, not
// the topology.
//
// See ~/.claude/plans/i-want-to-ralph-sequential-cherny.md for the
// design rationale.
package fsm

import "fmt"

// State is a node in the FSM. Loop states (clean, dirty, review) repeat
// until selectNextState routes elsewhere. revert is one-shot. done and
// failed are terminal and carry a Reason.
type State string

// FSM state names.
const (
	StateStart  State = "start"
	StateClean  State = "clean"
	StateDirty  State = "dirty"
	StateRevert State = "revert"
	StateReview State = "review"
	StateDone   State = "done"
	StateFailed State = "failed"
)

// Reason annotates terminal states. done carries one of {queue_empty,
// iter_cap, idle}; failed carries one of {budget, auth, runner_terminal}.
// Non-terminal states carry ReasonNone.
type Reason string

// Terminal-state reasons. ReasonNone is used by non-terminal states;
// ReasonQueueEmpty/ReasonIterCap/ReasonIdle pair with StateDone; ReasonBudget,
// ReasonAuth, and ReasonRunnerTerminal pair with StateFailed.
const (
	ReasonNone           Reason = ""
	ReasonQueueEmpty     Reason = "queue_empty"
	ReasonIterCap        Reason = "iter_cap"
	ReasonIdle           Reason = "idle"
	ReasonBudget         Reason = "budget"
	ReasonAuth           Reason = "auth"
	ReasonRunnerTerminal Reason = "runner_terminal"
)

// AllStates lists every defined State in topology order. Used by tests
// and by `ralph fsm graph`.
func AllStates() []State {
	return []State{
		StateStart, StateClean, StateDirty, StateRevert,
		StateReview, StateDone, StateFailed,
	}
}

// AllStateNames returns AllStates as a []string for callers (e.g. shell
// completion) that don't otherwise need the typed form.
func AllStateNames() []string {
	s := AllStates()
	out := make([]string, len(s))
	for i, st := range s {
		out[i] = string(st)
	}
	return out
}

// Terminal reports whether s is a terminal state (done or failed).
func (s State) Terminal() bool {
	return s == StateDone || s == StateFailed
}

// Valid reports whether s is a defined State.
func (s State) Valid() bool {
	for _, k := range AllStates() {
		if s == k {
			return true
		}
	}
	return false
}

// ValidReasons returns the reasons defined for s. Non-terminal states
// return a single-element slice containing ReasonNone.
func (s State) ValidReasons() []Reason {
	switch s {
	case StateDone:
		return []Reason{ReasonQueueEmpty, ReasonIterCap, ReasonIdle}
	case StateFailed:
		return []Reason{ReasonBudget, ReasonAuth, ReasonRunnerTerminal}
	default:
		return []Reason{ReasonNone}
	}
}

// Outcome bundles a State with its Reason. Used as the FSM's terminal
// summary (persisted to fsm.json) and as the return shape from
// selectNextState. Non-terminal outcomes carry ReasonNone.
type Outcome struct {
	State  State  `json:"state"`
	Reason Reason `json:"reason,omitempty"`
}

// String renders an Outcome as "state" for non-terminal states and
// "state{reason}" for terminals.
func (o Outcome) String() string {
	if o.Reason == ReasonNone {
		return string(o.State)
	}
	return fmt.Sprintf("%s{%s}", o.State, o.Reason)
}

// ExitCode is the process exit code a terminal outcome maps to: 0 for
// done{*}, 1 for failed{*}. Non-terminal outcomes also return 0 — they
// shouldn't reach an exit-mapping site in production, and a permissive
// zero keeps test fixtures simple.
func (o Outcome) ExitCode() int {
	if o.State == StateFailed {
		return 1
	}
	return 0
}

// Validate reports an error if o is malformed: an unknown state, a
// terminal state missing a reason, a non-terminal state carrying one,
// or a reason that doesn't belong to its state.
func (o Outcome) Validate() error {
	if !o.State.Valid() {
		return fmt.Errorf("fsm: unknown state %q", o.State)
	}
	allowed := o.State.ValidReasons()
	for _, r := range allowed {
		if r == o.Reason {
			return nil
		}
	}
	if o.State.Terminal() {
		return fmt.Errorf("fsm: state %q requires reason in %v, got %q", o.State, allowed, o.Reason)
	}
	return fmt.Errorf("fsm: state %q must carry no reason, got %q", o.State, o.Reason)
}
