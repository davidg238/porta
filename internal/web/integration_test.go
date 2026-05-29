package web

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/davidg238/porta/internal/command"
)

func TestEndToEndOperatorFlow(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	srv := serve(t, st)

	// 1. fleet page lists the node
	if !strings.Contains(readBody(t, mustGet(t, srv.URL+"/")), "aabbccddeeff") {
		t.Fatal("node missing from fleet page")
	}
	// 2. detail page renders
	if mustGet(t, srv.URL+"/n/aabbccddeeff").StatusCode != 200 {
		t.Fatal("detail page not 200")
	}
	// 3. queue a set via the form
	if _, err := http.PostForm(srv.URL+"/n/aabbccddeeff/set",
		url.Values{"app": {"demo"}, "key": {"gain"}, "value": {"7"}}); err != nil {
		t.Fatal(err)
	}
	// 4. upload an image
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("name", "demo")
	mw.WriteField("interval", "1m")
	fw, _ := mw.CreateFormFile("image", "demo.bin")
	fw.Write([]byte("BYTES"))
	mw.Close()
	if _, err := http.Post(srv.URL+"/n/aabbccddeeff/containers/install", mw.FormDataContentType(), &buf); err != nil {
		t.Fatal(err)
	}

	// 5. both land in the queue, tagged web
	cmds, _ := st.CommandLog("aabbccddeeff")
	if len(cmds) != 2 {
		t.Fatalf("want 2 queued commands, got %d: %+v", len(cmds), cmds)
	}
	for _, c := range cmds {
		if c.IssuedBy != "web" {
			t.Errorf("cmd #%d issued_by=%q want web", c.ID, c.IssuedBy)
		}
	}
	// 6. payload registered under the CRC of the uploaded bytes
	if ok, _ := st.PayloadExists(int64(command.CRC32([]byte("BYTES")))); !ok {
		t.Error("payload not registered")
	}
	// 7. /log shows the queued set (its args carry "demo")
	if !strings.Contains(readBody(t, mustGet(t, srv.URL+"/log")), "demo") {
		t.Error("/log missing the queued set")
	}
}
