package apiclient

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubServer returns an httptest server whose handler records the last request
// (method, path, body) and replies with the given status + JSON envelope body.
func stubServer(t *testing.T, status int, respBody string, rec *recordedReq) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		rec.path = r.URL.EscapedPath()
		rec.contentType = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		rec.body = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, respBody)
	}))
	t.Cleanup(srv.Close)
	return srv
}

type recordedReq struct {
	method, path, contentType, body string
}

func TestCommandPostsEnvelopeAndDecodes(t *testing.T) {
	var rec recordedReq
	srv := stubServer(t, http.StatusOK,
		`{"ok":true,"data":{"command_id":7,"node_id":"aabbccddeeff"},"error":""}`, &rec)
	c := New(srv.URL)

	cmdID, nodeID, err := c.Command("blinky", "set",
		map[string]any{"app": "sampler", "key": "interval", "value": 30})
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	if cmdID != 7 || nodeID != "aabbccddeeff" {
		t.Fatalf("cmdID=%d nodeID=%q", cmdID, nodeID)
	}
	if rec.method != "POST" || rec.path != "/api/nodes/blinky/commands" {
		t.Fatalf("request = %s %s", rec.method, rec.path)
	}
	if !strings.Contains(rec.contentType, "application/json") {
		t.Errorf("content-type=%q", rec.contentType)
	}
	// Body is a {verb,args} envelope.
	var got struct {
		Verb string                 `json:"verb"`
		Args map[string]interface{} `json:"args"`
	}
	if err := json.Unmarshal([]byte(rec.body), &got); err != nil {
		t.Fatalf("decode sent body: %v — %s", err, rec.body)
	}
	if got.Verb != "set" || got.Args["app"] != "sampler" || got.Args["key"] != "interval" {
		t.Errorf("sent body = %+v", got)
	}
}

func TestCommandServerErrorBecomesError(t *testing.T) {
	var rec recordedReq
	srv := stubServer(t, http.StatusBadRequest,
		`{"ok":false,"data":null,"error":"set-power-mode: invalid mode"}`, &rec)
	c := New(srv.URL)
	_, _, err := c.Command("n", "set-power-mode", map[string]any{"mode": "turbo"})
	if err == nil || !strings.Contains(err.Error(), "invalid mode") {
		t.Fatalf("want server error string, got %v", err)
	}
}

func TestCommandTransportErrorWrap(t *testing.T) {
	// Start a server, capture its URL, then close it so the connection is refused.
	var rec recordedReq
	srv := stubServer(t, http.StatusOK, `{"ok":true,"data":{},"error":""}`, &rec)
	url := srv.URL
	srv.Close()
	c := New(url)
	_, _, err := c.Command("n", "set-console", map[string]any{"state": "on"})
	if err == nil || !strings.Contains(err.Error(), "porta serve") {
		t.Fatalf("want friendly 'porta serve' wrap, got %v", err)
	}
}

func TestSelectorIsPathEscaped(t *testing.T) {
	var rec recordedReq
	srv := stubServer(t, http.StatusOK,
		`{"ok":true,"data":{"command_id":1,"node_id":"x"},"error":""}`, &rec)
	c := New(srv.URL)
	if _, _, err := c.Command("a b/c", "stop", map[string]any{"name": "app"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rec.path, "a%20b") {
		t.Errorf("selector not path-escaped: %q", rec.path)
	}
}

func TestCommandNonJSONResponse(t *testing.T) {
	var rec recordedReq
	srv := stubServer(t, http.StatusBadGateway, "<html>502 Bad Gateway</html>", &rec)
	c := New(srv.URL)
	_, _, err := c.Command("n", "stop", map[string]any{"name": "app"})
	if err == nil || !strings.Contains(err.Error(), "invalid response") {
		t.Fatalf("want 'invalid response' diagnostic, got %v", err)
	}
}
