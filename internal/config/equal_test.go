// Copyright (c) 2026 Ekorau LLC

package config

import (
	"encoding/json"
	"testing"
)

// num returns a json.Number from a literal (mimics what UseNumber() yields).
func num(s string) json.Number { return json.Number(s) }

func TestEqualScalars(t *testing.T) {
	cases := []struct {
		name string
		a, b any
		want bool
	}{
		{"both int same", num("30"), num("30"), true},
		{"int vs float same value", num("30"), num("30.0"), true},
		{"float vs int same value", num("30.0"), num("30"), true},
		{"different ints", num("30"), num("31"), false},
		{"int vs float different", num("30"), num("30.5"), false},
		{"bool true", true, true, true},
		{"bool false", false, false, true},
		{"bool mismatch", true, false, false},
		{"string equal", "eco", "eco", true},
		{"string differ", "eco", "heat", false},
		{"cross-type string vs num", "30", num("30"), false},
		{"cross-type bool vs num", true, num("1"), false},
		{"large int preserves precision", num("9007199254740993"), num("9007199254740993"), true},
		// 2^53 boundary: both 9007199254740992 and 9007199254740993 collapse to
		// the same float64 (2^53), so a float64-only equality check would call
		// them equal. The text short-circuit must catch this and return false.
		{"large int collision false", num("9007199254740993"), num("9007199254740992"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := EqualScalars(c.a, c.b); got != c.want {
				t.Errorf("EqualScalars(%v, %v) = %v, want %v", c.a, c.b, got, c.want)
			}
		})
	}
}
