package cmdutil

import (
	"testing"
	"time"
)

func TestParseDeadline(t *testing.T) {
	local := func(y int, mo time.Month, d, h, mi, sec int) time.Time {
		return time.Date(y, mo, d, h, mi, sec, 0, time.Local)
	}
	for in, want := range map[string]time.Time{
		"":                          {},
		"2026-11-14T18:00:00Z":      time.Date(2026, 11, 14, 18, 0, 0, 0, time.UTC),
		"2026-11-14T18:00:00-08:00": time.Date(2026, 11, 15, 2, 0, 0, 0, time.UTC),
		"2026-11-14T18:00Z":         time.Date(2026, 11, 14, 18, 0, 0, 0, time.UTC),
		"2026-11-14T18:00+01:00":    time.Date(2026, 11, 14, 17, 0, 0, 0, time.UTC),
		"2026-11-14 18:00:00Z":      time.Date(2026, 11, 14, 18, 0, 0, 0, time.UTC),
		"2026-11-14T18:00:30":       local(2026, 11, 14, 18, 0, 30),
		"2026-11-14T18:00":          local(2026, 11, 14, 18, 0, 0),
		"2026-11-14 18:00":          local(2026, 11, 14, 18, 0, 0),
		" 2026-11-14 ":              local(2026, 11, 14, 0, 0, 0),
	} {
		got, err := ParseDeadline(in)
		if err != nil {
			t.Errorf("ParseDeadline(%q): %v", in, err)
			continue
		}
		if !got.Equal(want) {
			t.Errorf("ParseDeadline(%q) = %v, want %v", in, got, want)
		}
	}
	for _, bad := range []string{"7d", "36h", "tomorrow", "2026-13-01"} {
		if _, err := ParseDeadline(bad); err == nil {
			t.Errorf("ParseDeadline(%q) = nil error, want rejection", bad)
		}
	}
}
