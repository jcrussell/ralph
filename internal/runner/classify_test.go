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
		{"object empty", map[string]any{}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classifyAPIError(c.field); got != c.want {
				t.Errorf("classifyAPIError(%v) = %s, want %s", c.field, got, c.want)
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
