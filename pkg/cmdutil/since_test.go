package cmdutil

import (
	"testing"
	"time"
)

func TestParseSinceDuration(t *testing.T) {
	got, err := ParseSince("1h")
	if err != nil {
		t.Fatalf("ParseSince: %v", err)
	}
	if delta := time.Since(got); delta < 50*time.Minute || delta > 70*time.Minute {
		t.Errorf("1h yielded since=%v (delta=%v)", got, delta)
	}
}

func TestParseSinceRFC3339(t *testing.T) {
	got, err := ParseSince("2026-01-02T03:04:05Z")
	if err != nil {
		t.Fatalf("ParseSince: %v", err)
	}
	want := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseSinceInvalid(t *testing.T) {
	for _, spec := range []string{"eleven days ago", "invalid", ""} {
		if _, err := ParseSince(spec); err == nil {
			t.Errorf("ParseSince(%q): nil err, want failure", spec)
		}
	}
}
