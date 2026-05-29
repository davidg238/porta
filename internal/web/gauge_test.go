package web

import "testing"

func TestCheckinFilling(t *testing.T) {
	g := Checkin(true, 100, 30, 300, 110) // 10s since seen, polls every 30s
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
	g := Checkin(true, 100, 30, 300, 140) // 40s since seen, expected 30s
	if g.Color != "amber" || g.FillPct != 100 || g.Online != true {
		t.Errorf("got %+v", g)
	}
}

func TestCheckinOffline(t *testing.T) {
	g := Checkin(true, 100, 30, 300, 500) // 400s > max-offline 300
	if g.Color != "red" || g.Online != false {
		t.Errorf("got %+v", g)
	}
}

func TestCheckinNeverSeen(t *testing.T) {
	g := Checkin(false, 0, 30, 300, 500)
	if g.Color != "red" || g.Online != false || g.FillPct != 0 {
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
