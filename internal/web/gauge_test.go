// Copyright (c) 2026 Ekorau LLC

package web

import "testing"

func TestCheckinFilling(t *testing.T) {
	g := Checkin(true, 100, 30, 0, 300, 110) // 10s since seen, polls every 30s
	if g.Color != "green" {
		t.Errorf("color=%s", g.Color)
	}
	if g.FillPct < 30 || g.FillPct > 40 { // 10/30 ≈ 33%
		t.Errorf("fill=%d", g.FillPct)
	}
	if g.Label == "" || g.Online != true {
		t.Errorf("label=%q online=%v", g.Label, g.Online)
	}
}

func TestCheckinOverdue(t *testing.T) {
	g := Checkin(true, 100, 30, 0, 300, 140) // 40s since seen, expected 30s
	if g.Color != "amber" || g.FillPct != 100 || g.Online != true {
		t.Errorf("got %+v", g)
	}
}

func TestCheckinOffline(t *testing.T) {
	g := Checkin(true, 100, 30, 0, 300, 500) // 400s > max-offline 300
	if g.Color != "red" || g.Online != false {
		t.Errorf("got %+v", g)
	}
}

func TestCheckinNeverSeen(t *testing.T) {
	g := Checkin(false, 0, 30, 0, 300, 500)
	if g.Color != "red" || g.Online != false || g.FillPct != 0 {
		t.Errorf("got %+v", g)
	}
}

// An always-on node reports every 60s but its poll-interval is 30s. The gauge
// must key off the reported cadence and stay green at 42s (issue #14), not flip
// amber for the 30→60s half of every window.
func TestCheckinReportedIntervalOverridesPoll(t *testing.T) {
	g := Checkin(true, 100, 30, 60, 300, 142) // 42s since seen, reported every 60s
	if g.Color != "green" || g.Online != true {
		t.Errorf("expected green, got %+v", g)
	}
	if g.FillPct < 65 || g.FillPct > 75 { // 42/60 = 70%
		t.Errorf("fill=%d, want ~70", g.FillPct)
	}
}

// Past the reported cadence it still goes amber (overdue keyed off 60s, not 30s).
func TestCheckinReportedIntervalOverdue(t *testing.T) {
	g := Checkin(true, 100, 30, 60, 300, 175) // 75s since seen, reported every 60s
	if g.Color != "amber" || g.FillPct != 100 || g.Online != true {
		t.Errorf("got %+v", g)
	}
}

func TestHumanizeDur(t *testing.T) {
	cases := map[int64]string{5: "5s", 90: "1m", 1800: "30m", 7200: "2h"}
	for in, want := range cases {
		if got := humanizeDur(in); got != want {
			t.Errorf("humanizeDur(%d)=%q want %q", in, got, want)
		}
	}
}
