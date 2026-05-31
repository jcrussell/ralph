package cmdutil

import (
	"fmt"
	"time"
)

// ParseSince parses a --since spec shared by the read-side commands
// (timeline, report). A value that parses as a Go duration ("1h",
// "30m") is interpreted relative to now; otherwise it must be an
// RFC3339 timestamp. Anything else is an error.
func ParseSince(spec string) (time.Time, error) {
	if d, err := time.ParseDuration(spec); err == nil {
		return time.Now().Add(-d), nil
	}
	if t, err := time.Parse(time.RFC3339, spec); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("not a duration or RFC3339 timestamp: %q", spec)
}
