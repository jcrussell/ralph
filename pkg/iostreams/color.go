package iostreams

import "os"

// ColorScheme renders styled output. When disabled, every method returns
// its input unchanged so call sites never need an `if isTTY` guard.
type ColorScheme struct{ enabled bool }

// NewColorScheme returns a ColorScheme that emits ANSI codes when enabled
// is true and passes through unchanged otherwise.
func NewColorScheme(enabled bool) *ColorScheme {
	return &ColorScheme{enabled: enabled}
}

// EnvAllowsColor reports whether the NO_COLOR env var permits color.
// Per the NO_COLOR spec (no-color.org), any non-empty value disables color
// regardless of what the value is.
func EnvAllowsColor() bool {
	v, ok := os.LookupEnv("NO_COLOR")
	return !ok || v == ""
}

const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiFaint  = "\x1b[2m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiCyan   = "\x1b[36m"
)

func (c *ColorScheme) wrap(code, s string) string {
	if !c.enabled {
		return s
	}
	return code + s + ansiReset
}

// Enabled reports whether this scheme emits ANSI codes.
func (c *ColorScheme) Enabled() bool { return c.enabled }

// Red wraps s in red when enabled, otherwise returns s.
func (c *ColorScheme) Red(s string) string { return c.wrap(ansiRed, s) }

// Green wraps s in green when enabled, otherwise returns s.
func (c *ColorScheme) Green(s string) string { return c.wrap(ansiGreen, s) }

// Yellow wraps s in yellow when enabled, otherwise returns s.
func (c *ColorScheme) Yellow(s string) string { return c.wrap(ansiYellow, s) }

// Cyan wraps s in cyan when enabled, otherwise returns s.
func (c *ColorScheme) Cyan(s string) string { return c.wrap(ansiCyan, s) }

// Bold wraps s in bold when enabled, otherwise returns s.
func (c *ColorScheme) Bold(s string) string { return c.wrap(ansiBold, s) }

// Faint wraps s in the dim/faint SGR when enabled, otherwise returns s.
func (c *ColorScheme) Faint(s string) string { return c.wrap(ansiFaint, s) }
