// Copyright (c) 2026 Ekorau LLC

package web

import (
	"strings"
	"testing"
)

func TestNodeProfilesPanelListsAndLinks(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	if _, err := st.InsertProfileResult("aabbccddeeff", "myapp", "run1", 1001, []byte{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	srv := serve(t, st)

	p := readBody(t, mustGet(t, srv.URL+"/n/aabbccddeeff/profiles"))
	for _, want := range []string{`id="profiles"`, "myapp", "run1", "nodus://profile?", "seq=1", "[decode"} {
		if !strings.Contains(p, want) {
			t.Errorf("profiles panel missing %q in:\n%s", want, p)
		}
	}
	if !strings.Contains(p, `hx-get="/n/aabbccddeeff/profiles"`) {
		t.Errorf("profiles panel must self-poll: %s", p)
	}
	// Partial must not carry "load" in its trigger (would cause rapid-fire on swap).
	if strings.Contains(p, `hx-trigger="load`) {
		t.Errorf("profiles partial must not include 'load' in hx-trigger: %s", p)
	}
}

func TestNodeProfilesPanelNewestFirst(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	// Insert 3 results; panel must show seq 3 before seq 1.
	for i := 1; i <= 3; i++ {
		if _, err := st.InsertProfileResult("aabbccddeeff", "myapp", "lbl", int64(1000+i), []byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	srv := serve(t, st)

	p := readBody(t, mustGet(t, srv.URL+"/n/aabbccddeeff/profiles"))
	idx3 := strings.Index(p, "seq=3")
	idx1 := strings.Index(p, "seq=1")
	if idx3 < 0 || idx1 < 0 {
		t.Fatalf("expected seq=3 and seq=1 in panel: %s", p)
	}
	if idx3 > idx1 {
		t.Errorf("newest result (seq=3) should appear before oldest (seq=1)")
	}
}
