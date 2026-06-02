package iostreams

import (
	"os"
	"strings"
	"testing"
)

func TestColorSchemeDisabledIsIdentity(t *testing.T) {
	cs := NewColorScheme(false)
	cases := map[string]func(string) string{
		"Red":    cs.Red,
		"Green":  cs.Green,
		"Yellow": cs.Yellow,
		"Cyan":   cs.Cyan,
		"Bold":   cs.Bold,
		"Faint":  cs.Faint,
	}
	for name, fn := range cases {
		if got := fn("hello"); got != "hello" {
			t.Errorf("%s(disabled) = %q, want %q", name, got, "hello")
		}
	}
	if cs.Enabled() {
		t.Errorf("Enabled() = true, want false")
	}
}

func TestColorSchemeEnabledWrapsAnsi(t *testing.T) {
	cs := NewColorScheme(true)
	cases := map[string]struct {
		fn   func(string) string
		code string
	}{
		"Red":    {cs.Red, "\x1b[31m"},
		"Green":  {cs.Green, "\x1b[32m"},
		"Yellow": {cs.Yellow, "\x1b[33m"},
		"Cyan":   {cs.Cyan, "\x1b[36m"},
		"Bold":   {cs.Bold, "\x1b[1m"},
		"Faint":  {cs.Faint, "\x1b[2m"},
	}
	for name, tc := range cases {
		got := tc.fn("hi")
		if !strings.HasPrefix(got, tc.code) {
			t.Errorf("%s prefix = %q, want %q", name, got, tc.code)
		}
		if !strings.HasSuffix(got, "\x1b[0m") {
			t.Errorf("%s suffix = %q, want reset", name, got)
		}
		if !strings.Contains(got, "hi") {
			t.Errorf("%s = %q, missing payload", name, got)
		}
	}
	if !cs.Enabled() {
		t.Errorf("Enabled() = false, want true")
	}
}

func TestEnvAllowsColor(t *testing.T) {
	cases := []struct {
		name  string
		set   bool
		value string
		want  bool
	}{
		{"unset", false, "", true},
		{"empty", true, "", true},
		{"one", true, "1", false},
		{"zero", true, "0", false},
		{"yes", true, "yes", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// t.Setenv first so the original value is restored after
			// the test, even when we Unsetenv below.
			t.Setenv("NO_COLOR", "sentinel")
			if tc.set {
				if err := os.Setenv("NO_COLOR", tc.value); err != nil {
					t.Fatalf("set NO_COLOR: %v", err)
				}
			} else {
				if err := os.Unsetenv("NO_COLOR"); err != nil {
					t.Fatalf("unset NO_COLOR: %v", err)
				}
			}
			if got := EnvAllowsColor(); got != tc.want {
				t.Errorf("EnvAllowsColor() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTestStreamsHaveDisabledColor(t *testing.T) {
	ios, _ := Test()
	if ios.ColorScheme() == nil {
		t.Fatalf("ColorScheme() = nil")
	}
	if ios.ColorScheme().Enabled() {
		t.Errorf("Test() ColorScheme enabled; want disabled")
	}
}
