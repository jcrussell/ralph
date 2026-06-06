package runner

import (
	"encoding/json"
	"regexp"
	"strings"
)

// ResetHintRE matches a "resets 4am (UTC)" / "resets 10:30pm (UTC)" wall-clock
// hint in runner output. It is the canonical source of this pattern: Classify
// uses it (via MatchString) to recognize a subscription usage cap, which always
// renders a reset time, while backoff.ParseRateLimitReset reuses it to compute
// the sleep duration from its capture groups — one regexp keeps the two in
// lockstep. The :MM group is optional so hour-only hints keep matching.
var ResetHintRE = regexp.MustCompile(`(?i)resets\s+(\d{1,2})(?::(\d{2}))?\s*(am|pm)\s*\(UTC\)`)

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

	// ModeQuota — the runner reports it has hit a session/usage cap
	// that resets (5-hour window, weekly limit, etc.) rather than the
	// account being out of money. Terminal: ralph cannot make progress
	// on this run; the operator either waits for the cap to reset or
	// switches plans. Distinct from ModeBudget because the operator's
	// action differs (top up vs. wait).
	ModeQuota Mode = "quota"

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

// Error-text substrings scanned case-insensitively in both the runner's
// stderr and the envelope's api_error_status/result text. Lifted to
// consts so the two scan sites — Classify's stderr switch and
// matchAPIErrorText — reference one source of truth and cannot drift
// apart. Substrings used by only one site (underscore variants, OOM
// phrases, "too many requests") stay inline at their single use.
const (
	txtCreditBalance      = "credit balance"
	txtInsufficientCredit = "insufficient credit"
	txtUsageLimit         = "usage limit"
	txtExtraUsage         = "out of extra usage"
	txtQuotaExceeded      = "quota exceeded"
	txtSessionLimit       = "session limit"
	txtWeeklyLimit        = "weekly limit"
	txtFiveHourLimit      = "5-hour limit"
	// Generic cap wording the claude CLI now renders without a qualifier,
	// e.g. "You've hit your limit · resets 3:20am (UTC)". Added so wording
	// drift off the specific phrases above doesn't slip past as ModeOK (see
	// ralph-5vt / ralph-ii3). Kept narrow ("your limit", not a bare "limit
	// reached") so it can't collide with a transient "rate limit reached" in
	// the un-IsError-gated stderr scan and flip it to terminal quota.
	txtHitYourLimit   = "hit your limit"
	txtInvalidAPIKey  = "invalid api key"
	txtAuthentication = "authentication"
	txtUnauthorized   = "unauthorized"
	txtRateLimit      = "rate limit"
	txtOverloaded     = "overloaded"
)

// Terminal reports whether m forces the FSM into failed{m}. ModeAuth,
// ModeBudget, and ModeQuota are the intrinsically terminal modes —
// ModeDeadSession only escalates after dead_session_threshold hits
// (handled by the loop, not here).
func (m Mode) Terminal() bool {
	return m == ModeAuth || m == ModeBudget || m == ModeQuota
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

	// Cgroup memory.events from the systemd-run scope is authoritative
	// when set — exit code 137 (the fallback below) can also mean
	// "killed by some other SIGKILL", so prefer the in-kernel signal.
	if s.OOMSignal {
		return ModeOOM
	}

	// API-error fields in the parsed envelope are the strongest
	// signal — Claude itself tells us what happened.
	if s.Envelope != nil {
		if m := classifyAPIError(s.Envelope.APIErrorStatus); m != "" {
			return m
		}
		// When the CLI flags is_error=true, the human-readable failure
		// text lives in the "result" field rather than stderr (the
		// --output-format=json wrapper routes everything through stdout).
		// Gate on IsError so prose containing "usage limit" in a normal
		// model response cannot trip a terminal mode.
		if s.Envelope.IsError {
			if m := matchAPIErrorText(s.Envelope.Result); m != "" {
				return m
			}
			// Structural fallback: an is_error envelope carrying a numeric
			// HTTP error status whose wording we don't recognize must never
			// degrade to ModeOK — the loop reads ModeOK as a clean success
			// and spins to iter_cap (the ralph-5vt / ralph-ii3 failure). A
			// 429 with a wall-clock "resets ... (UTC)" hint is a subscription
			// usage cap (ModeQuota, which wait_on_quota sleeps out); a bare
			// 429 is a transient API rate-limit (ModeRateLimit). Any other
			// 4xx/5xx is recoverable ModeUnknown pending a captured sample.
			if code, ok := httpErrStatus(s.Envelope.APIErrorStatus); ok {
				switch {
				case code == 429 && ResetHintRE.MatchString(s.Envelope.Result):
					return ModeQuota
				case code == 429:
					return ModeRateLimit
				default:
					return ModeUnknown
				}
			}
		}
		if strings.EqualFold(s.Envelope.Subtype, "error_max_turns") {
			return ModeDeadSession
		}
	}

	// Stderr substring scan — case-insensitive, longest-match-wins
	// implied by check order: auth + credit + quota before rate-limit
	// before overloaded before oom.
	low := strings.ToLower(s.Stderr + "\n" + s.StderrTail)
	switch {
	case strings.Contains(low, txtCreditBalance) || strings.Contains(low, txtInsufficientCredit):
		return ModeBudget
	case strings.Contains(low, txtUsageLimit),
		strings.Contains(low, txtExtraUsage),
		strings.Contains(low, txtFiveHourLimit),
		strings.Contains(low, txtWeeklyLimit),
		strings.Contains(low, txtSessionLimit),
		strings.Contains(low, txtQuotaExceeded),
		strings.Contains(low, txtHitYourLimit):
		// Best-guess substrings for claude CLI quota-exhaustion
		// errors (5-hour window, weekly cap, session cap). Refine
		// when a real quota failure is captured — see ralph-ii3.
		return ModeQuota
	case strings.Contains(low, txtInvalidAPIKey),
		strings.Contains(low, txtAuthentication),
		strings.Contains(low, txtUnauthorized):
		return ModeAuth
	case strings.Contains(low, txtRateLimit), strings.Contains(low, "too many requests"):
		return ModeRateLimit
	case strings.Contains(low, txtOverloaded):
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
// through to other signals. Numeric HTTP status codes (e.g. float64
// 429) are intentionally not classified here — 429 is ambiguous
// between rate-limit and quota, and the disambiguating text lives in
// the envelope's "result" field, scanned separately by Classify.
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
	return matchAPIErrorText(s)
}

// httpErrStatus extracts a numeric HTTP status from the envelope's
// api_error_status field when it carries a bare status code — the claude CLI
// renders 429/5xx this way alongside is_error=true, with the human-readable
// reason in the separate "result" field. JSON decoding yields float64; int and
// json.Number are handled defensively in case the decoder differs. Reports
// (0, false) for non-numeric values and for sub-400 codes (not error statuses),
// so the caller's structural fallback only fires on genuine error responses.
func httpErrStatus(v any) (int, bool) {
	var code int
	switch t := v.(type) {
	case float64:
		code = int(t)
	case int:
		code = t
	case int64:
		code = int(t)
	case json.Number:
		n, err := t.Int64()
		if err != nil {
			return 0, false
		}
		code = int(n)
	default:
		return 0, false
	}
	if code < 400 {
		return 0, false
	}
	return code, true
}

// matchAPIErrorText scans free-form error text (envelope result or
// api_error_status string/message) for the same set of substrings used
// by classifyAPIError, so callers stay in sync about what counts as a
// budget/quota/auth/rate-limit/overloaded signal.
func matchAPIErrorText(s string) Mode {
	low := strings.ToLower(s)
	switch {
	case strings.Contains(low, txtCreditBalance), strings.Contains(low, txtInsufficientCredit):
		return ModeBudget
	case strings.Contains(low, "quota_exceeded"),
		strings.Contains(low, txtQuotaExceeded),
		strings.Contains(low, "usage_limit"),
		strings.Contains(low, txtUsageLimit),
		strings.Contains(low, txtExtraUsage),
		strings.Contains(low, "session_limit"),
		strings.Contains(low, txtSessionLimit),
		strings.Contains(low, "monthly usage"),
		strings.Contains(low, txtWeeklyLimit),
		strings.Contains(low, txtFiveHourLimit),
		strings.Contains(low, txtHitYourLimit):
		return ModeQuota
	case strings.Contains(low, txtAuthentication), strings.Contains(low, txtInvalidAPIKey), strings.Contains(low, txtUnauthorized):
		return ModeAuth
	case strings.Contains(low, txtRateLimit), strings.Contains(low, "rate_limit"):
		return ModeRateLimit
	case strings.Contains(low, txtOverloaded):
		return ModeModelOverloaded
	}
	return ""
}
