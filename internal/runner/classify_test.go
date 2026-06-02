package runner

import "testing"

func TestClassifyTimeoutWins(t *testing.T) {
	// Timeout dominates even when stderr looks like an auth error.
	s := &Session{KilledByTimeout: true, Stderr: "invalid api key"}
	if got := Classify(s); got != ModeTimeout {
		t.Errorf("Classify timeout: %s, want %s", got, ModeTimeout)
	}
}

func TestClassifyOOMSignalBeatsExitCode(t *testing.T) {
	// OOMSignal (cgroup memory.events oom_kill > 0) wins over the
	// envelope, stderr scan, and ExitCode==137 fallback.
	s := &Session{OOMSignal: true, ExitCode: 0, Envelope: &Envelope{}}
	if got := Classify(s); got != ModeOOM {
		t.Errorf("Classify OOMSignal: %s, want %s", got, ModeOOM)
	}
}

func TestClassifyTimeoutBeatsOOMSignal(t *testing.T) {
	// Timeout is the orchestrator's own kill; if both signals are
	// present the timeout is the proximate cause we want to report.
	s := &Session{KilledByTimeout: true, OOMSignal: true}
	if got := Classify(s); got != ModeTimeout {
		t.Errorf("Classify timeout+oom: %s, want %s", got, ModeTimeout)
	}
}

func TestClassifyNilSession(t *testing.T) {
	if got := Classify(nil); got != ModeUnknown {
		t.Errorf("Classify(nil) = %s, want %s", got, ModeUnknown)
	}
}

func TestClassifyAPIError(t *testing.T) {
	cases := []struct {
		name  string
		field any
		want  Mode
	}{
		{"nil", nil, ""},
		{"empty string", "", ""},
		{"unknown string", "potato", ""},
		{"credit balance string", "your credit balance is too low", ModeBudget},
		{"authentication string", "authentication failed", ModeAuth},
		{"invalid api key string", "Invalid API Key", ModeAuth},
		{"rate limit string", "rate limit exceeded", ModeRateLimit},
		{"overloaded string", "Overloaded — try again later", ModeModelOverloaded},
		{"object with message", map[string]any{"message": "Rate limit exceeded"}, ModeRateLimit},
		{"object with type only", map[string]any{"type": "authentication_error"}, ModeAuth},
		{"quota_exceeded type", map[string]any{"type": "quota_exceeded"}, ModeQuota},
		{"usage_limit type", map[string]any{"type": "usage_limit_exceeded"}, ModeQuota},
		{"usage limit message", map[string]any{"message": "Usage limit reached"}, ModeQuota},
		{"session_limit type", map[string]any{"type": "session_limit_reached"}, ModeQuota},
		{"object empty", map[string]any{}, ""},
		// Numeric HTTP status (e.g. JSON 429 unmarshals to float64) is
		// intentionally not classified by api_error_status alone — 429
		// is ambiguous between rate-limit and quota, so disambiguation
		// is left to the envelope's "result" text scan in Classify.
		{"numeric 429", float64(429), ""},
		{"numeric 401", float64(401), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classifyAPIError(c.field); got != c.want {
				t.Errorf("classifyAPIError(%v) = %s, want %s", c.field, got, c.want)
			}
		})
	}
}

// TestClassifyEnvelopeResult covers the captured-from-the-wild case
// where the claude CLI emits is_error=true plus a "result" field
// containing the human-readable error, while api_error_status is a
// bare HTTP status code (e.g. 429). Pre-fix, ralph treated these
// envelopes as ModeOK and looped to iter_cap; the IsError-gated
// result scan must produce the right terminal mode instead.
func TestClassifyEnvelopeResult(t *testing.T) {
	cases := []struct {
		name string
		env  *Envelope
		want Mode
	}{
		{
			"monthly usage limit (captured envelope)",
			&Envelope{IsError: true, APIErrorStatus: float64(429), Result: "You've hit your org's monthly usage limit"},
			ModeQuota,
		},
		{
			"weekly limit in result",
			&Envelope{IsError: true, APIErrorStatus: float64(429), Result: "Weekly limit reached"},
			ModeQuota,
		},
		{
			"out of extra usage with reset hint (captured envelope)",
			&Envelope{IsError: true, APIErrorStatus: float64(429), Result: "You're out of extra usage · resets 10:30pm (UTC)"},
			ModeQuota,
		},
		{
			"credit balance in result",
			&Envelope{IsError: true, Result: "your credit balance is too low"},
			ModeBudget,
		},
		{
			"rate limit in result",
			&Envelope{IsError: true, APIErrorStatus: float64(429), Result: "rate limit exceeded"},
			ModeRateLimit,
		},
		{
			"auth in result",
			&Envelope{IsError: true, Result: "Authentication failed"},
			ModeAuth,
		},
		{
			"is_error=false suppresses scan (no false positive)",
			&Envelope{IsError: false, Result: "Here's an example log line: usage limit exceeded"},
			ModeOK,
		},
		{
			"is_error=true with unrecognized text falls through to OK",
			&Envelope{IsError: true, Result: "something else went wrong"},
			ModeOK,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Classify(&Session{Envelope: c.env, ExitCode: 0})
			if got != c.want {
				t.Errorf("Classify = %s, want %s", got, c.want)
			}
		})
	}
}

func TestClassifyEnvelopeSignals(t *testing.T) {
	cases := []struct {
		name string
		env  *Envelope
		exit int
		want Mode
	}{
		{"envelope OK exit 0", &Envelope{}, 0, ModeOK},
		{"envelope api auth", &Envelope{APIErrorStatus: "authentication required"}, 0, ModeAuth},
		{"envelope api budget", &Envelope{APIErrorStatus: "credit balance too low"}, 0, ModeBudget},
		{"envelope error_max_turns", &Envelope{Subtype: "error_max_turns"}, 0, ModeDeadSession},
		{"envelope error_max_turns case-insensitive", &Envelope{Subtype: "Error_Max_Turns"}, 0, ModeDeadSession},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Classify(&Session{Envelope: c.env, ExitCode: c.exit})
			if got != c.want {
				t.Errorf("Classify = %s, want %s", got, c.want)
			}
		})
	}
}

func TestClassifyStderrPatterns(t *testing.T) {
	cases := []struct {
		name   string
		stderr string
		want   Mode
	}{
		{"credit balance", "Error: your credit balance is too low\n", ModeBudget},
		{"insufficient credit", "insufficient credit\n", ModeBudget},
		{"5-hour limit", "You've reached your 5-hour limit. Try again later.\n", ModeQuota},
		{"weekly limit", "Weekly limit reached for your plan\n", ModeQuota},
		{"session limit", "session limit hit\n", ModeQuota},
		{"usage limit", "Usage limit reached on this account\n", ModeQuota},
		{"quota exceeded", "quota exceeded for the current window\n", ModeQuota},
		{"invalid api key", "Authentication failed: Invalid API Key\n", ModeAuth},
		{"unauthorized", "401 Unauthorized\n", ModeAuth},
		{"rate limit", "429 rate limit exceeded\n", ModeRateLimit},
		{"too many requests", "Too Many Requests\n", ModeRateLimit},
		{"overloaded", "Service Overloaded\n", ModeModelOverloaded},
		{"oom killed memory", "process Killed: out of MEMORY\n", ModeOOM},
		{"oom literal", "OOMKilled\n", ModeOOM},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Classify(&Session{Stderr: c.stderr, ExitCode: 1})
			if got != c.want {
				t.Errorf("Classify(stderr=%q) = %s, want %s", c.stderr, got, c.want)
			}
		})
	}
}

func TestClassifyExitCodeFallbacks(t *testing.T) {
	cases := []struct {
		name string
		sess *Session
		want Mode
	}{
		{"137 OOM signal", &Session{ExitCode: 137}, ModeOOM},
		{"non-zero no envelope dead", &Session{ExitCode: 2}, ModeDeadSession},
		{"non-zero with envelope unknown", &Session{ExitCode: 2, Envelope: &Envelope{}}, ModeUnknown},
		{"zero exit no envelope dead", &Session{ExitCode: 0}, ModeDeadSession},
		{"zero exit with envelope ok", &Session{ExitCode: 0, Envelope: &Envelope{}}, ModeOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Classify(c.sess)
			if got != c.want {
				t.Errorf("Classify = %s, want %s", got, c.want)
			}
		})
	}
}

func TestModeTerminal(t *testing.T) {
	terminal := map[Mode]bool{
		ModeAuth:            true,
		ModeBudget:          true,
		ModeQuota:           true,
		ModeOK:              false,
		ModeRateLimit:       false,
		ModeModelOverloaded: false,
		ModeOOM:             false,
		ModeTimeout:         false,
		ModeDeadSession:     false, // escalated by streak, not intrinsic
		ModeUnknown:         false,
	}
	for m, want := range terminal {
		if got := m.Terminal(); got != want {
			t.Errorf("%s.Terminal() = %v, want %v", m, got, want)
		}
	}
}
