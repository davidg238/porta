// Copyright (c) 2026 Ekorau LLC

package web

import (
	"net/http"
	"strings"
	"testing"
)

// The web console is a read-only dashboard: it observes the fleet but never
// queues commands or uploads images (those go through the CLI / nodus). This
// walks the read paths and confirms the removed write routes 404.
func TestEndToEndOperatorFlow(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	if err := st.InsertReport("aabbccddeeff",
		`{"config":{},"apps":{"demo":{"crc":99,"runlevel":3}}}`, "", 1001); err != nil {
		t.Fatal(err)
	}
	// A pre-existing command (issued elsewhere) shows up read-only.
	st.EnqueueCommand("aabbccddeeff", "set", `{"app":"demo","key":"gain","value":7}`, "cli", 1000)
	srv := serve(t, st)

	// 1. fleet page lists the node
	if !strings.Contains(readBody(t, mustGet(t, srv.URL+"/")), "aabbccddeeff") {
		t.Fatal("node missing from fleet page")
	}
	// 2. detail page renders the read-only sections + the observed container
	detail := readBody(t, mustGet(t, srv.URL+"/n/aabbccddeeff"))
	for _, want := range []string{"Containers", "demo", "Recent commands", "set"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail page missing %q", want)
		}
	}
	// 3. the write routes are gone and enqueue nothing
	for _, sub := range []string{"set", "console", "poll-interval", "containers/install", "containers/uninstall"} {
		resp, err := http.Post(srv.URL+"/n/aabbccddeeff/"+sub, "application/x-www-form-urlencoded", strings.NewReader("name=demo"))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("POST /%s got %d, want 404", sub, resp.StatusCode)
		}
	}
	// 4. only the pre-existing command is in the queue — the web added nothing
	cmds, _ := st.CommandLog("aabbccddeeff")
	if len(cmds) != 1 || cmds[0].Verb != "set" {
		t.Fatalf("web must not enqueue; want the single pre-existing set, got %+v", cmds)
	}
	// 5. /log shows the queued set (its args carry "demo")
	if !strings.Contains(readBody(t, mustGet(t, srv.URL+"/log")), "demo") {
		t.Error("/log missing the queued set")
	}
}
