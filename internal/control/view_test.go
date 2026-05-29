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
