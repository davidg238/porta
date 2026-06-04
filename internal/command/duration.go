// Copyright (c) 2026 Ekorau LLC

package command

import (
	"fmt"
	"strconv"
)

// ParseDurationSeconds parses a jag-style duration into whole seconds. Accepts
// a bare integer (seconds) or an integer with a unit suffix s/m/h/d.
func ParseDurationSeconds(s string) (int64, error) {
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	mult := int64(1)
	body := s
	switch s[len(s)-1] {
	case 's':
		mult, body = 1, s[:len(s)-1]
	case 'm':
		mult, body = 60, s[:len(s)-1]
	case 'h':
		mult, body = 3600, s[:len(s)-1]
	case 'd':
		mult, body = 86400, s[:len(s)-1]
	}
	n, err := strconv.ParseInt(body, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	return n * mult, nil
}
