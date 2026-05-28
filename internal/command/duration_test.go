package command

import "testing"

func TestParseDurationSeconds(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"30s", 30}, {"5m", 300}, {"2h", 7200}, {"1d", 86400}, {"45", 45},
	}
	for _, c := range cases {
		got, err := ParseDurationSeconds(c.in)
		if err != nil || got != c.want {
			t.Errorf("ParseDurationSeconds(%q) = %d, %v; want %d", c.in, got, err, c.want)
		}
	}
}

func TestParseDurationSecondsRejectsGarbage(t *testing.T) {
	for _, in := range []string{"", "abc", "10x", "3.5m"} {
		if _, err := ParseDurationSeconds(in); err == nil {
			t.Errorf("ParseDurationSeconds(%q) should error", in)
		}
	}
}
