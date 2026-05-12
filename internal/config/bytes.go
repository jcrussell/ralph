package config

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseBytes converts a size string like "7G", "512m", "100KB", or
// "1048576" to a byte count. Suffixes K/M/G/T use 1024 multipliers; a
// trailing "B" or "iB" is permitted and ignored. Whitespace is
// trimmed. An empty input returns 0 with no error.
func ParseBytes(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	up := strings.ToUpper(s)
	up = strings.TrimSuffix(up, "IB")
	up = strings.TrimSuffix(up, "B")

	var mul int64 = 1
	if n := len(up); n > 0 {
		switch up[n-1] {
		case 'K':
			mul = 1 << 10
		case 'M':
			mul = 1 << 20
		case 'G':
			mul = 1 << 30
		case 'T':
			mul = 1 << 40
		}
		if mul > 1 {
			up = up[:n-1]
		}
	}
	up = strings.TrimSpace(up)
	v, err := strconv.ParseInt(up, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("config: parse bytes %q: %w", s, err)
	}
	if v < 0 {
		return 0, fmt.Errorf("config: parse bytes %q: negative", s)
	}
	return v * mul, nil
}
