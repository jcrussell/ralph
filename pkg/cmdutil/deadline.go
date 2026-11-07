package cmdutil

import (
	"fmt"
	"strings"
	"time"
)

// zonedLayouts carry their own zone ("Z" or "-08:00"), with or without
// seconds; deadlineLayouts have none and are read in local time.
var zonedLayouts = []string{
	time.RFC3339,
	"2006-01-02T15:04Z07:00",
	"2006-01-02 15:04:05Z07:00",
	"2006-01-02 15:04Z07:00",
}

var deadlineLayouts = []string{
	"2006-01-02T15:04:05",
	"2006-01-02T15:04",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006-01-02",
}

// ParseDeadline parses a --until value: an absolute date-time, either
// with a zone ("2026-11-14T18:00Z", "2026-11-14T18:00:00-08:00") or a
// local "2026-11-14T18:00", "2026-11-14 18:00" or "2026-11-14"
// (midnight). Durations are deliberately not accepted: the flag must
// mean the same instant every time a supervisor restarts the run. ""
// returns the zero time (no deadline).
func ParseDeadline(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	for _, layout := range zonedLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	for _, layout := range deadlineLayouts {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid --until %q: want a date-time such as 2026-11-14T18:00 or 2026-11-14T18:00:00Z (durations are not accepted; use $(date -d '+7 days' -Iseconds))", s)
}
