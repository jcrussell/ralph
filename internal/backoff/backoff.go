// Package backoff is the pure mapping from (runner mode, session,
// retry streaks, config) to sleep duration. Ported verbatim from
// calculate_backoff in run_optimization_loop.py — the math is
// battle-tested upstream so the Go version preserves it line-for-line.
//
// Compute itself takes no clock and reads no external state; callers
// pass in any rate-limit-reset hint they parsed from runner output via
// ParseRateLimitReset.
package backoff

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jcrussell/ralph/internal/config"
	"github.com/jcrussell/ralph/internal/runner"
)

// MaxBackoff caps every computed duration. Matches Python MAX_BACKOFF
// (4800s). Generous enough to ride out a multi-hour rate-limit window
// without locking the operator out for a full day.
const MaxBackoff = 80 * time.Minute

// OKBackoff is the post-success sleep. Matches Python's clean-exit
// return of 10s. Kept distinct from cfg.Loop.SleepBetweenSecs to keep
// backoff dependent on Backoff config only.
const OKBackoff = 10 * time.Second

// expCap is the maximum exponent applied to the base in rate-limit and
// dead-session exponential backoff. Matches Python's min(n, 4).
const expCap = 4

// Streaks counts consecutive runner outcomes the loop has observed.
// The loop owns the counters; backoff just reads them.
type Streaks struct {
	// ConsecutiveFailures is the count of any-mode non-OK runs in a
	// row. Used as the exponent for rate-limit exponential backoff.
	ConsecutiveFailures int
	// DeadSession is the count of ModeDeadSession runs in a row. Once
	// it crosses cfg.DeadSessionThreshold the loop escalates to the
	// long-backoff path.
	DeadSession int
}

// Input bundles everything Compute needs. Session may be nil — Compute
// treats that as ExitCode == 0 (no extra signal).
type Input struct {
	Mode    runner.Mode
	Session *runner.Session
	Streaks Streaks
	// RateLimitReset, when non-zero, is the duration until the upstream
	// limit lifts. Populated by ParseRateLimitReset on a hit. Compute
	// prefers this over the exponential fallback.
	RateLimitReset time.Duration
}

// Compute returns how long to sleep before the next iteration.
// Terminal modes (auth, budget) return 0 — the loop routes those to
// failed{} via the FSM and won't call us, but returning 0 keeps the
// function total.
func Compute(in Input, cfg *config.BackoffConfig) time.Duration {
	if cfg == nil {
		cfg = &config.BackoffConfig{}
	}
	if in.Mode.Terminal() {
		return 0
	}

	base := dur(cfg.RateLimitDefault, 300*time.Second)
	threshold := cfg.DeadSessionThreshold
	if threshold <= 0 {
		threshold = 3
	}

	switch in.Mode {
	case runner.ModeRateLimit:
		if in.RateLimitReset > 0 {
			return capDur(in.RateLimitReset)
		}
		return capDur(expBackoff(base, in.Streaks.ConsecutiveFailures))

	case runner.ModeModelOverloaded:
		return base

	case runner.ModeOOM:
		return dur(cfg.OOMSecs, 30*time.Second)

	case runner.ModeTimeout:
		return dur(cfg.TimeoutSecs, 60*time.Second)

	case runner.ModeDeadSession, runner.ModeUnknown:
		if in.Streaks.DeadSession >= threshold {
			return capDur(expBackoff(base, in.Streaks.DeadSession-threshold))
		}
		if in.Session != nil && in.Session.ExitCode != 0 {
			return dur(cfg.UnknownSecs, 60*time.Second)
		}
		return OKBackoff

	case runner.ModeOK:
		return OKBackoff
	}
	return OKBackoff
}

// expBackoff returns base * 2^min(n, expCap). n < 0 is treated as 0.
func expBackoff(base time.Duration, n int) time.Duration {
	if n < 0 {
		n = 0
	}
	if n > expCap {
		n = expCap
	}
	return base * (1 << n)
}

func capDur(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	if d > MaxBackoff {
		return MaxBackoff
	}
	return d
}

func dur(secs int, fallback time.Duration) time.Duration {
	if secs > 0 {
		return time.Duration(secs) * time.Second
	}
	return fallback
}

// resetRE matches "resets 4am (UTC)" style hints in runner stderr.
// Anthropic surfaces this in the API-key-rate-limit error body.
var resetRE = regexp.MustCompile(`(?i)resets\s+(\d{1,2})\s*(am|pm)\s*\(UTC\)`)

// ParseRateLimitReset extracts a "resets Nam/pm (UTC)" hint from
// stderr and returns the duration from now until that wall-clock
// instant, plus a 60s safety buffer (matching Python). Returns 0 when
// no hint is present so callers can fall through to exponential.
//
// now is the reference clock; callers in production pass
// time.Now().UTC(). Splitting now out keeps the function deterministic
// for tests.
func ParseRateLimitReset(stderr string, now time.Time) time.Duration {
	m := resetRE.FindStringSubmatch(stderr)
	if m == nil {
		return 0
	}
	hour, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	switch strings.ToLower(m[2]) {
	case "pm":
		if hour != 12 {
			hour += 12
		}
	case "am":
		if hour == 12 {
			hour = 0
		}
	}
	now = now.UTC()
	reset := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, time.UTC)
	if !reset.After(now) {
		reset = reset.Add(24 * time.Hour)
	}
	return reset.Sub(now) + time.Minute
}
