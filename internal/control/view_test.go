// Copyright (c) 2026 Ekorau LLC

package control

import "testing"

func TestDesiredVsObservedMarksPendingAndConverged(t *testing.T) {
	st := newStore(t)
	st.EnsureNode("n1", 100)
	Set(st, "n1", "demo", "gain", int64(2), "cli", 100)
	Set(st, "n1", "demo", "mode", "fast", "cli", 101)
	st.InsertReport("n1", `{"config":{"demo":{"gain":2}}}`, "", 102)

	rows, err := DesiredVsObserved(st, "n1", "demo")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]ConfigRow{}
	for _, r := range rows {
		got[r.Key] = r
	}
	if got["gain"].Marker != "" {
		t.Errorf("gain should be converged, got marker %q", got["gain"].Marker)
	}
	if got["mode"].Marker == "" {
		t.Errorf("mode should be pending")
	}
}

func TestRelativeAge(t *testing.T) {
	if RelativeAge(0, 100) != "never" {
		t.Error("zero ts = never")
	}
	if RelativeAge(95, 100) != "5s ago" {
		t.Errorf("got %q", RelativeAge(95, 100))
	}
}

func TestConfigFromObserved(t *testing.T) {
	if len(ConfigFromObserved("")) != 0 {
		t.Error("empty string should yield empty map")
	}
	if len(ConfigFromObserved("{not json")) != 0 {
		t.Error("malformed JSON should yield empty map")
	}
	if len(ConfigFromObserved(`{"config":null}`)) != 0 {
		t.Error(`"config":null should yield empty map`)
	}
	cfg := ConfigFromObserved(`{"config":{"demo":{"gain":2}}}`)
	if cfg["demo"] == nil {
		t.Fatal("demo app missing")
	}
	if _, ok := cfg["demo"]["gain"]; !ok {
		t.Error("demo.gain missing")
	}
}

func TestAppsFromObservedSorted(t *testing.T) {
	apps, err := AppsFromObserved(`{"apps":{"zeta":{"crc":1,"runlevel":3},"alpha":{"crc":2,"runlevel":3}}}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 2 || apps[0].Name != "alpha" || apps[1].Name != "zeta" {
		t.Fatalf("want sorted [alpha zeta], got %+v", apps)
	}
}

func TestRenderReset(t *testing.T) {
	code := int64(6)
	cases := []struct {
		cat  string
		code *int64
		want string
	}{
		{"watchdog", &code, "watchdog (6)"},
		{"watchdog", nil, "watchdog"},
		{"", nil, "—"},
		{"", &code, "—"}, // no category → dash regardless of code
	}
	for _, c := range cases {
		if got := RenderReset(c.cat, c.code); got != c.want {
			t.Errorf("RenderReset(%q,%v) = %q, want %q", c.cat, c.code, got, c.want)
		}
	}
}
