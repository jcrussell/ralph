package backoff

import (
	"testing"
	"time"

	"github.com/jcrussell/ralph/internal/config"
	"github.com/jcrussell/ralph/internal/runner"
)

// defaults returns a BackoffConfig identical to config.Defaults().Backoff
// so the matrix below doesn't need to chase config drift if a default
// changes — the test fails loudly and points at this fixture.
func defaults() *config.BackoffConfig {
	d := config.Defaults().Backoff
	return &d
}

func TestComputeMatrix(t *testing.T) {
	cfg := defaults()
	base := time.Duration(cfg.RateLimitDefault) * time.Second // 900s

	cases := []struct {
		name string
		in   Input
		want time.Duration
	}{
		// Terminal modes: 0 (loop has already routed to failed{}).
		{"auth terminal", Input{Mode: runner.ModeAuth}, 0},
		{"budget terminal", Input{Mode: runner.ModeBudget}, 0},

		// Rate limit: explicit reset hint wins.
		{
			name: "rate_limit with reset under cap",
			in:   Input{Mode: runner.ModeRateLimit, RateLimitReset: 17 * time.Minute},
			want: 17 * time.Minute,
		},
		{
			name: "rate_limit with reset over cap is clamped",
			in:   Input{Mode: runner.ModeRateLimit, RateLimitReset: 5 * time.Hour},
			want: MaxBackoff,
		},
		// Rate limit: exponential fallback when no hint.
		{
			name: "rate_limit no hint, streak=0 -> base",
			in:   Input{Mode: runner.ModeRateLimit, Streaks: Streaks{ConsecutiveFailures: 0}},
			want: base,
		},
		{
			name: "rate_limit no hint, streak=2 -> base*4",
			in:   Input{Mode: runner.ModeRateLimit, Streaks: Streaks{ConsecutiveFailures: 2}},
			want: capDurOrBase(base * 4),
		},
		{
			name: "rate_limit no hint, streak=99 -> capped at MaxBackoff",
			in:   Input{Mode: runner.ModeRateLimit, Streaks: Streaks{ConsecutiveFailures: 99}},
			want: MaxBackoff,
		},

		// Model overloaded: flat base.
		{"model_overloaded -> base", Input{Mode: runner.ModeModelOverloaded}, base},

		// OOM / timeout: config-driven, defaults match Python.
		{"oom -> cfg.OOMSecs", Input{Mode: runner.ModeOOM}, time.Duration(cfg.OOMSecs) * time.Second},
		{"timeout -> cfg.TimeoutSecs", Input{Mode: runner.ModeTimeout}, time.Duration(cfg.TimeoutSecs) * time.Second},

		// Dead session: below threshold + clean exit -> OK sleep.
		{
			name: "dead_session below threshold, exit=0 -> OKBackoff",
			in: Input{
				Mode:    runner.ModeDeadSession,
				Session: &runner.Session{ExitCode: 0},
				Streaks: Streaks{DeadSession: 0},
			},
			want: OKBackoff,
		},
		// Dead session: below threshold + non-zero exit -> UnknownSecs.
		{
			name: "dead_session below threshold, exit!=0 -> UnknownSecs",
			in: Input{
				Mode:    runner.ModeDeadSession,
				Session: &runner.Session{ExitCode: 1},
				Streaks: Streaks{DeadSession: 1},
			},
			want: time.Duration(cfg.UnknownSecs) * time.Second,
		},
		// Dead session: at threshold -> base * 2^0 = base.
		{
			name: "dead_session at threshold -> base",
			in: Input{
				Mode:    runner.ModeDeadSession,
				Streaks: Streaks{DeadSession: cfg.DeadSessionThreshold},
			},
			want: base,
		},
		// Dead session: well past threshold -> capped.
		{
			name: "dead_session 9 past threshold -> capped",
			in: Input{
				Mode:    runner.ModeDeadSession,
				Streaks: Streaks{DeadSession: cfg.DeadSessionThreshold + 9},
			},
			want: MaxBackoff,
		},

		// Unknown shares the dead-session ladder.
		{
			name: "unknown at threshold -> base",
			in: Input{
				Mode:    runner.ModeUnknown,
				Streaks: Streaks{DeadSession: cfg.DeadSessionThreshold},
			},
			want: base,
		},
		{
			name: "unknown low streak, exit!=0 -> UnknownSecs",
			in: Input{
				Mode:    runner.ModeUnknown,
				Session: &runner.Session{ExitCode: 137},
			},
			want: time.Duration(cfg.UnknownSecs) * time.Second,
		},
		{
			name: "unknown low streak, exit=0 -> OKBackoff",
			in:   Input{Mode: runner.ModeUnknown, Session: &runner.Session{ExitCode: 0}},
			want: OKBackoff,
		},
		{
			name: "unknown low streak, nil session -> OKBackoff",
			in:   Input{Mode: runner.ModeUnknown},
			want: OKBackoff,
		},

		// OK: post-success.
		{"ok -> OKBackoff", Input{Mode: runner.ModeOK}, OKBackoff},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Compute(c.in, cfg)
			if got != c.want {
				t.Errorf("Compute(%s) = %v, want %v", c.name, got, c.want)
			}
		})
	}
}

// capDurOrBase mirrors capDur for fixture math (so the table reads
// naturally without manual ceiling computations).
func capDurOrBase(d time.Duration) time.Duration {
	if d > MaxBackoff {
		return MaxBackoff
	}
	return d
}

func TestComputeNilCfg(t *testing.T) {
	// Nil cfg falls back to documented defaults (base=300s, OOM=30s, …).
	got := Compute(Input{Mode: runner.ModeModelOverloaded}, nil)
	if got != 300*time.Second {
		t.Errorf("nil cfg base = %v, want 300s", got)
	}
	if got := Compute(Input{Mode: runner.ModeOOM}, nil); got != 30*time.Second {
		t.Errorf("nil cfg OOM = %v, want 30s", got)
	}
	if got := Compute(Input{Mode: runner.ModeTimeout}, nil); got != 60*time.Second {
		t.Errorf("nil cfg timeout = %v, want 60s", got)
	}
}

func TestComputeZeroCfgFallsBackToDefaults(t *testing.T) {
	// Zero-value Backoff cfg should behave like nil.
	cfg := &config.BackoffConfig{}
	if got := Compute(Input{Mode: runner.ModeOOM}, cfg); got != 30*time.Second {
		t.Errorf("zero cfg OOM = %v, want 30s default", got)
	}
	if got := Compute(Input{Mode: runner.ModeRateLimit}, cfg); got != 300*time.Second {
		t.Errorf("zero cfg rate_limit base = %v, want 300s default", got)
	}
}

func TestExpBackoffCap(t *testing.T) {
	base := time.Second
	cases := []struct {
		n    int
		want time.Duration
	}{
		{-3, base},                   // negative clamps to 0
		{0, base},                    // 2^0
		{4, base * 16},               // 2^4
		{99, base * (1 << expCap)},   // clamps to expCap
	}
	for _, c := range cases {
		got := expBackoff(base, c.n)
		if got != c.want {
			t.Errorf("expBackoff(%v, %d) = %v, want %v", base, c.n, got, c.want)
		}
	}
}

func TestParseRateLimitReset(t *testing.T) {
	now := time.Date(2026, 5, 13, 10, 0, 0, 0, time.UTC)

	cases := []struct {
		name   string
		stderr string
		want   time.Duration
	}{
		{
			name:   "no hint",
			stderr: "Error: something else",
			want:   0,
		},
		{
			name:   "resets 4am UTC (next day, since 10am > 4am)",
			stderr: "Rate limit reached. Limit resets 4am (UTC)",
			want:   18*time.Hour + time.Minute, // 4am next day = +18h, plus 60s buffer
		},
		{
			name:   "resets 11am UTC (later today)",
			stderr: "limit resets 11am (UTC)",
			want:   time.Hour + time.Minute,
		},
		{
			name:   "resets 12pm UTC (noon, +2h)",
			stderr: "resets 12pm (UTC)",
			want:   2*time.Hour + time.Minute,
		},
		{
			name:   "resets 12am UTC (midnight, +14h)",
			stderr: "resets 12am (UTC)",
			want:   14*time.Hour + time.Minute,
		},
		{
			name:   "case insensitive",
			stderr: "RESETS 11AM (UTC)",
			want:   time.Hour + time.Minute,
		},
		{
			name:   "no UTC marker = no match",
			stderr: "resets 4am",
			want:   0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ParseRateLimitReset(c.stderr, now)
			if got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestComputeIsPure(t *testing.T) {
	// Same inputs -> same output, every time. Guards against accidental
	// reads of time.Now() or other ambient state.
	in := Input{
		Mode:           runner.ModeRateLimit,
		RateLimitReset: 5 * time.Minute,
	}
	cfg := defaults()
	first := Compute(in, cfg)
	for range 10 {
		if got := Compute(in, cfg); got != first {
			t.Fatalf("Compute returned %v then %v for same input", first, got)
		}
	}
}
