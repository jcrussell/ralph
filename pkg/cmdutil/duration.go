package cmdutil

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// ParseDuration parses a human-friendly age/retention spec into a
// time.Duration. It extends time.ParseDuration with day ("d") and week
// ("w") suffixes — "30d", "2w" — which Go's parser rejects. A plain Go
// duration ("72h", "90m", "1h30m") still parses. The result must be
// positive; empty, zero, negative, or unrecognized specs are errors.
//
// This is the sibling of ParseSince: ParseSince returns an instant
// (now-d) for the read-side --since commands, whereas ParseDuration
// returns the span itself, which is what age thresholds like
// `ralph gc --older-than` need.
func ParseDuration(spec string) (time.Duration, error) {
	s := strings.TrimSpace(spec)
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}

	var d time.Duration
	switch unit := s[len(s)-1]; unit {
	case 'd', 'w':
		mult := 24 * time.Hour
		if unit == 'w' {
			mult = 7 * 24 * time.Hour
		}
		n, err := strconv.ParseFloat(s[:len(s)-1], 64)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q (want e.g. 30d, 2w, 72h)", spec)
		}
		// Guard the float→int64 conversion: Go leaves an out-of-range
		// result implementation-defined, so a huge spec like "1e10d" could
		// otherwise wrap to a bogus value that slips past the d<=0 check.
		ns := n * float64(mult)
		if math.IsNaN(ns) || ns < 1 || ns > math.MaxInt64 {
			return 0, fmt.Errorf("duration out of range %q (want e.g. 30d, 2w, 72h)", spec)
		}
		d = time.Duration(ns)
	default:
		var err error
		d, err = time.ParseDuration(s)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q (want e.g. 30d, 2w, 72h)", spec)
		}
	}

	if d <= 0 {
		return 0, fmt.Errorf("duration must be positive, got %q", spec)
	}
	return d, nil
}
