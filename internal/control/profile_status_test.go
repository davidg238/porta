// Copyright (c) 2026 Ekorau LLC

package control

import (
	"testing"

	"github.com/davidg238/porta/internal/store"
)

func TestDeriveProfileState(t *testing.T) {
	const armedAt = 1000
	sess := func(dur int64) *store.ProfileSession {
		return &store.ProfileSession{DeviceID: "n1", App: "app", StartedAt: armedAt, DurationS: dur}
	}

	cases := []struct {
		name         string
		sess         *store.ProfileSession
		lastReportAt int64
		lastResultTS int64
		now          int64
		want         ProfileState
	}{
		{"no session", nil, 0, 0, 5000, ProfileNone},
		{"armed, node never reported", sess(30), 0, 0, 5000, ProfileAwaiting},
		{"armed, node reported only before arming", sess(30), 900, 0, 5000, ProfileAwaiting},
		{"armed, reported since, within window", sess(30), 2000, 0, 2050, ProfileRunning},
		// deadline = lastReport(2000) + duration(30) + grace(60) = 2090.
		{"armed, reported since, just inside deadline", sess(30), 2000, 0, 2090, ProfileRunning},
		{"armed, reported since, just past deadline", sess(30), 2000, 0, 2091, ProfileStale},
		{"result arrived since arming", sess(30), 2000, 1500, 9999, ProfileFulfilled},
		{"result exactly at arming counts as fulfilled", sess(30), 2000, armedAt, 9999, ProfileFulfilled},
		{"stale result from before arming ignored", sess(30), 2000, 900, 2091, ProfileStale},
		// duration 0 = open/continuous → never auto-stale even far past any window.
		{"open/continuous never stale", sess(0), 2000, 0, 999999, ProfileRunning},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := DeriveProfileState(c.sess, c.lastReportAt, c.lastResultTS, c.now)
			if got != c.want {
				t.Errorf("DeriveProfileState = %q, want %q", got, c.want)
			}
		})
	}
}

func TestProfileSessionStatusLoader(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// No session → ProfileNone, nil session.
	got, err := ProfileSessionStatus(st, "n1", 5000)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != ProfileNone || got.Session != nil {
		t.Fatalf("no-session want {none,nil}, got %+v", got)
	}

	// Arm at t=1000, dur=30. Node reported at t=2000 (since arming), no result.
	if err := st.EnsureNode("n1", 1000); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertProfileSession("n1", "app", "lbl", 30, 1000); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertReport("n1", "{}", "", 2000); err != nil {
		t.Fatal(err)
	}

	// now well past deadline (2000+30+60=2090) → stale.
	got, err = ProfileSessionStatus(st, "n1", 3000)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != ProfileStale {
		t.Fatalf("want stale, got %q", got.State)
	}
	if got.Session == nil || got.Session.DurationS != 30 {
		t.Fatalf("session not carried through: %+v", got.Session)
	}

	// A result arriving since arming flips it to fulfilled.
	if _, err := st.InsertProfileResult("n1", "app", "lbl", 2100, []byte{1}); err != nil {
		t.Fatal(err)
	}
	got, err = ProfileSessionStatus(st, "n1", 3000)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != ProfileFulfilled {
		t.Fatalf("want fulfilled after result, got %q", got.State)
	}
}

func TestProfileStateLabel(t *testing.T) {
	if ProfileStale.Label() == "" || ProfileAwaiting.Label() == "" {
		t.Fatal("stale/awaiting must have non-empty labels")
	}
	if ProfileNone.Label() != "" {
		t.Fatalf("none label should be empty, got %q", ProfileNone.Label())
	}
}
