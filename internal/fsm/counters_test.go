package fsm

import "testing"

func TestBumpIter(t *testing.T) {
	f := &FSM{}
	f.BumpIter()
	f.BumpIter()
	f.BumpIter()
	if f.Iter != 3 {
		t.Errorf("Iter = %d, want 3", f.Iter)
	}
}

func TestObserveTransition(t *testing.T) {
	cases := []struct {
		name      string
		from, to  State
		startDirt int
		wantDirt  int
	}{
		{"clean->dirty increments", StateClean, StateDirty, 0, 1},
		{"dirty->dirty increments", StateDirty, StateDirty, 1, 2},
		{"dirty->clean resets", StateDirty, StateClean, 3, 0},
		{"clean->clean unchanged", StateClean, StateClean, 0, 0},
		{"dirty->revert resets", StateDirty, StateRevert, 4, 0},
		{"clean->revert resets", StateClean, StateRevert, 2, 0},
		{"review->dirty does not increment (only clean|dirty source)", StateReview, StateDirty, 5, 5},
		{"start->clean resets", StateStart, StateClean, 1, 0},
		{"dirty->review unchanged", StateDirty, StateReview, 2, 2},
		{"dirty->done unchanged", StateDirty, StateDone, 7, 7},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &FSM{
				Outcome:          Outcome{State: c.from},
				ConsecutiveDirty: c.startDirt,
			}
			f.ObserveTransition(c.to)
			if f.ConsecutiveDirty != c.wantDirt {
				t.Errorf("ConsecutiveDirty after %s->%s (start=%d) = %d, want %d",
					c.from, c.to, c.startDirt, f.ConsecutiveDirty, c.wantDirt)
			}
			// ObserveTransition must not mutate State.
			if f.State != c.from {
				t.Errorf("ObserveTransition mutated State: got %s, want %s", f.State, c.from)
			}
		})
	}
}
