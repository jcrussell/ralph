package runner

import "strings"

// Mode is the classified outcome of one runner invocation. The loop
// uses Mode to pick a backoff duration and to decide whether to fail
// the FSM (Terminal modes) or continue.
type Mode string

const (
	// ModeOK — exit 0 + a valid envelope. The default healthy state.
	ModeOK Mode = "ok"

	// ModeAuth — the runner could not authenticate. Terminal: ralph
	// can't make progress without credentials, so the FSM exits
	// failed{auth}.
	ModeAuth Mode = "auth"

	// ModeBudget — the runner reports it is out of credit. Terminal.
	// Distinct from ralph's own budget cap (which is failed{budget}
	// via the FSM predicate, not via classification).
	ModeBudget Mode = "budget"

	// ModeRateLimit — the runner says it has been rate-limited.
	// Recoverable; the loop sleeps and retries.
	ModeRateLimit Mode = "rate_limit"

	// ModeModelOverloaded — the upstream model is overloaded.
	// Recoverable.
	ModeModelOverloaded Mode = "model_overloaded"

	// ModeOOM — the kernel (or systemd-run cgroup) killed the
	// process for exceeding the memory cap. Recoverable; the loop
	// backs off.
	ModeOOM Mode = "oom"

	// ModeTimeout — the runner ran past session_timeout_secs and was
	// killed by ralph. Recoverable.
	ModeTimeout Mode = "timeout"

	// ModeDeadSession — the runner exited without producing useful
	// output (no envelope, or envelope says max-turns hit). Counted
	// toward the dead-session streak; becomes terminal at
	// dead_session_threshold.
	ModeDeadSession Mode = "dead_session"

	// ModeUnknown — exit non-zero with no signal we recognize.
	// Recoverable but worth investigating.
	ModeUnknown Mode = "unknown"
)

// Terminal reports whether m forces the FSM into failed{m}. ModeAuth
// and ModeBudget are the only intrinsically terminal modes —
// ModeDeadSession only escalates after dead_session_threshold hits
// (handled by the loop, not here).
func (m Mode) Terminal() bool {
	return m == ModeAuth || m == ModeBudget
}

// Classify inspects s and returns the matched Mode. Order of checks is
// load-bearing: explicit signals win over heuristics, terminal modes
// before recoverable ones. nil s reports ModeUnknown.
func Classify(s *Session) Mode {
	if s == nil {
		return ModeUnknown
	}

	if s.KilledByTimeout {
		return ModeTimeout
	}

	// API-error fields in the parsed envelope are the strongest
	// signal — Claude itself tells us what happened.
	if s.Envelope != nil {
		if m := classifyAPIError(s.Envelope.APIErrorStatus); m != "" {
			return m
		}
		if strings.EqualFold(s.Envelope.Subtype, "error_max_turns") {
			return ModeDeadSession
		}
	}

	// Stderr substring scan — case-insensitive, longest-match-wins
	// implied by check order: auth + credit before rate-limit before
	// overloaded before oom.
	low := strings.ToLower(s.Stderr + "\n" + s.StderrTail)
	switch {
	case strings.Contains(low, "credit balance") || strings.Contains(low, "insufficient credit"):
		return ModeBudget
	case strings.Contains(low, "invalid api key"),
		strings.Contains(low, "authentication"),
		strings.Contains(low, "unauthorized"):
		return ModeAuth
	case strings.Contains(low, "rate limit"), strings.Contains(low, "too many requests"):
		return ModeRateLimit
	case strings.Contains(low, "overloaded"):
		return ModeModelOverloaded
	case strings.Contains(low, "killed") && strings.Contains(low, "memory"),
		strings.Contains(low, "out of memory"),
		strings.Contains(low, "oom"):
		return ModeOOM
	}

	// Process-level OOM signal: SIGKILL surfaces as exit code 137.
	// Only trust it when nothing else explained the failure.
	if s.ExitCode == 137 {
		return ModeOOM
	}

	// No envelope and non-zero exit → likely a dead session (the
	// runner died before producing structured output).
	if s.ExitCode != 0 && s.Envelope == nil {
		return ModeDeadSession
	}
	if s.ExitCode != 0 {
		return ModeUnknown
	}

	// Exit 0 with no envelope is still suspicious — the runner is
	// expected to emit JSON. Treat as dead session.
	if s.Envelope == nil {
		return ModeDeadSession
	}
	return ModeOK
}

// classifyAPIError maps the envelope's api_error_status field (which
// claude renders as either a string or an object) to a Mode. Returns
// "" when the field is empty or unrecognized so callers can fall
// through to other signals.
func classifyAPIError(v any) Mode {
	if v == nil {
		return ""
	}
	var s string
	switch t := v.(type) {
	case string:
		s = t
	case map[string]any:
		if msg, ok := t["message"].(string); ok {
			s = msg
		}
		if typ, ok := t["type"].(string); ok && s == "" {
			s = typ
		}
	default:
		return ""
	}
	low := strings.ToLower(s)
	switch {
	case strings.Contains(low, "credit balance"), strings.Contains(low, "insufficient credit"):
		return ModeBudget
	case strings.Contains(low, "authentication"), strings.Contains(low, "invalid api key"), strings.Contains(low, "unauthorized"):
		return ModeAuth
	case strings.Contains(low, "rate limit"), strings.Contains(low, "rate_limit"):
		return ModeRateLimit
	case strings.Contains(low, "overloaded"):
		return ModeModelOverloaded
	}
	return ""
}
