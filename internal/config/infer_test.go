// Copyright (c) 2026 Ekorau LLC

package config

import "testing"

func TestInferScalar(t *testing.T) {
	cases := []struct {
		in   string
		want any
	}{
		{"true", true},
		{"false", false},
		{"30", int64(30)},
		{"-7", int64(-7)},
		{"21.5", 21.5},
		{"-0.25", -0.25},
		{"eco", "eco"},
		{"", ""},
		{"+30", int64(30)},   // strconv.ParseInt accepts a leading +
		{"3e2", 300.0},        // exponent form parses as float
		{"007", "007"},        // leading-zero non-numeric-shaped → string (matches reference)
	}
	for _, c := range cases {
		got := InferScalar(c.in)
		if got != c.want {
			t.Errorf("InferScalar(%q) = %v (%T), want %v (%T)", c.in, got, got, c.want, c.want)
		}
	}
}
