// Package config implements the porta gateway's per-app config plane:
// scalar inference, desired-vs-observed projection, and the self-heal
// reconcile algorithm. It is pure logic — no I/O, no globals.
package config

import (
	"regexp"
	"strconv"
)

// integerShaped matches strings that strconv.ParseInt accepts AND that look
// like an integer literal (no leading zeros except "0"/"-0"). The leading-zero
// exclusion keeps things like "007" rendering as a string, matching the
// reference impl's intent (preserve operator's literal intent for opaque ids).
var integerShaped = regexp.MustCompile(`^[+-]?(0|[1-9][0-9]*)$`)

// leadingZeroInt matches strings with a leading zero followed by more digits
// (e.g. "007", "-007"). These are treated as opaque strings, not numbers.
var leadingZeroInt = regexp.MustCompile(`^[+-]?0[0-9]+$`)

// InferScalar parses an operator-supplied CLI string into a typed scalar:
// "true"/"false" → bool; integer-shaped → int64; float-shaped → float64;
// anything else → the original string. Matches the reference's infer-scalar
// in examples/toit-gateway/command.toit.
func InferScalar(s string) any {
	switch s {
	case "true":
		return true
	case "false":
		return false
	}
	if integerShaped.MatchString(s) {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n
		}
	}
	if !leadingZeroInt.MatchString(s) {
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f
		}
	}
	return s
}
