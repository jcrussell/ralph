package fsm

// BumpIter increments the iteration counter. Call once per loop tick
// regardless of state.
func (f *FSM) BumpIter() {
	f.Iter++
}

// ObserveTransition updates counters that depend on the shape of a
// state transition. It does NOT change f.State — the caller assigns
// that after this call. Ordering matters: ObserveTransition reads the
// current f.State as the "from" state.
//
// Rules from the design doc:
//   - consecutive_dirty increments on clean|dirty → dirty
//   - resets on        → clean or → revert
//   - unchanged otherwise (e.g. transitions to review, done, failed)
func (f *FSM) ObserveTransition(next State) {
	switch next {
	case StateDirty:
		if f.State == StateClean || f.State == StateDirty {
			f.ConsecutiveDirty++
		}
	case StateClean, StateRevert:
		f.ConsecutiveDirty = 0
	}
}
