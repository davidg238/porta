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
}
