package cmdutil

import (
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	for _, tc := range []struct {
		spec string
		want time.Duration
	}{
		{"30d", 30 * 24 * time.Hour},
		{"1w", 7 * 24 * time.Hour},
		{"2w", 14 * 24 * time.Hour},
		{"72h", 72 * time.Hour},
		{"90m", 90 * time.Minute},
		{"1h30m", 90 * time.Minute},
		{"1.5d", 36 * time.Hour},
		{" 30d ", 30 * 24 * time.Hour}, // surrounding whitespace trimmed
	} {
		got, err := ParseDuration(tc.spec)
		if err != nil {
			t.Errorf("ParseDuration(%q): unexpected error %v", tc.spec, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseDuration(%q) = %v, want %v", tc.spec, got, tc.want)
		}
	}
}

func TestParseDurationInvalid(t *testing.T) {
	for _, spec := range []string{"", "  ", "abc", "d", "w", "-5d", "0d", "0h", "30x", "30dd"} {
		if _, err := ParseDuration(spec); err == nil {
			t.Errorf("ParseDuration(%q): nil err, want failure", spec)
		}
	}
}
