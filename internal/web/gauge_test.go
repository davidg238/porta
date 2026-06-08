// Copyright (c) 2026 Ekorau LLC

package web

import "testing"

// Checkin keys off the node's derived cadence (from its node_config echo) and a
// derived offline threshold (3×cadence). Green while within one cadence, amber
// while overdue but inside the offline window, red past it.

func TestCheckinFilling(t *testing.T) {
	g := Checkin(true, 100, 30, 90, 110) // 10s since seen, cadence 30s
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
	g := Checkin(true, 100, 30, 90, 140) // 40s since seen, cadence 30s, offline 90s
	if g.Color != "amber" || g.FillPct != 100 || g.Online != true {
		t.Errorf("got %+v", g)
	}
}

func TestCheckinOffline(t *testing.T) {
	g := Checkin(true, 100, 30, 90, 200) // 100s > offline threshold 90s
	if g.Color != "red" || g.Online != false {
		t.Errorf("got %+v", g)
	}
}

func TestCheckinNeverSeen(t *testing.T) {
	g := Checkin(false, 0, 30, 90, 500)
	if g.Color != "red" || g.Online != false || g.FillPct != 0 {
		t.Errorf("got %+v", g)
	}
}

// An always-on node with a 60s cadence stays green at 42s and only goes amber
// past its cadence (keyed off the echoed cadence, not a poll-interval guess).
func TestCheckinCadenceWindow(t *testing.T) {
	g := Checkin(true, 100, 60, 180, 142) // 42s since seen, cadence 60s
	if g.Color != "green" || g.Online != true {
		t.Errorf("expected green, got %+v", g)
	}
	if g.FillPct < 65 || g.FillPct > 75 { // 42/60 = 70%
		t.Errorf("fill=%d, want ~70", g.FillPct)
	}
	g2 := Checkin(true, 100, 60, 180, 175) // 75s since seen → overdue
	if g2.Color != "amber" || g2.FillPct != 100 || g2.Online != true {
		t.Errorf("got %+v", g2)
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
