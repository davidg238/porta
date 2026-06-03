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

func TestInstallBuildsMultipart(t *testing.T) {
	var rec recordedReq
	srv := stubServer(t, http.StatusOK,
		`{"ok":true,"data":{"command_id":6,"node_id":"aabbccddeeff","size":16},"error":""}`, &rec)
	c := New(srv.URL)

	img := strings.NewReader("fake-image-bytes")
	cmdID, nodeID, size, err := c.Install("blinky", "blink", img, InstallOpts{
		Lifecycle: "run-loop", Runlevel: 3, IntervalS: 30, Triggers: []string{"boot", "gpio-high=21"},
	})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if cmdID != 6 || nodeID != "aabbccddeeff" || size != 16 {
		t.Fatalf("cmdID=%d nodeID=%q size=%d", cmdID, nodeID, size)
	}
	if rec.method != "POST" || rec.path != "/api/nodes/blinky/containers" {
		t.Fatalf("request = %s %s", rec.method, rec.path)
	}
	if !strings.HasPrefix(rec.contentType, "multipart/form-data") {
		t.Fatalf("content-type=%q", rec.contentType)
	}
	// The body must carry the image file part and the form fields.
	for _, want := range []string{
		`name="image"`, `filename="blink.bin"`, "fake-image-bytes",
		`name="name"`, "blink",
		`name="lifecycle"`, "run-loop",
		`name="runlevel"`, "3",
		`name="interval"`, "30",
		`name="trigger"`, "boot", "gpio-high=21",
	} {
		if !strings.Contains(rec.body, want) {
			t.Errorf("multipart body missing %q", want)
		}
	}
}

func TestInstallOmitsZeroInterval(t *testing.T) {
	var rec recordedReq
	srv := stubServer(t, http.StatusOK,
		`{"ok":true,"data":{"command_id":1,"node_id":"x","size":1},"error":""}`, &rec)
	c := New(srv.URL)
	if _, _, _, err := c.Install("n", "app", strings.NewReader("x"),
		InstallOpts{Lifecycle: "run-once", Runlevel: 3}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rec.body, `name="interval"`) {
		t.Error("interval field should be omitted when IntervalS == 0")
	}
}

func TestPatchNodeSendsOnlyPresentFields(t *testing.T) {
	var rec recordedReq
	srv := stubServer(t, http.StatusOK,
		`{"ok":true,"data":{"node_id":"aabbccddeeff"},"error":""}`, &rec)
	c := New(srv.URL)

	name := "rename"
	nodeID, err := c.PatchNode("blinky", &name, nil)
	if err != nil {
		t.Fatalf("PatchNode: %v", err)
	}
	if nodeID != "aabbccddeeff" {
		t.Fatalf("nodeID=%q", nodeID)
	}
	if rec.method != "PATCH" || rec.path != "/api/nodes/blinky" {
		t.Fatalf("request = %s %s", rec.method, rec.path)
	}
	var body map[string]interface{}
	if err := json.Unmarshal([]byte(rec.body), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["name"] != "rename" {
		t.Errorf("name not sent: %v", body)
	}
	if _, ok := body["max_offline_s"]; ok {
		t.Errorf("max_offline_s must be omitted when nil: %v", body)
	}
}

func TestPatchNodeMaxOffline(t *testing.T) {
	var rec recordedReq
	srv := stubServer(t, http.StatusOK,
		`{"ok":true,"data":{"node_id":"x"},"error":""}`, &rec)
	c := New(srv.URL)
	secs := int64(600)
	if _, err := c.PatchNode("n", nil, &secs); err != nil {
		t.Fatal(err)
	}
	var body map[string]interface{}
	json.Unmarshal([]byte(rec.body), &body)
	if body["max_offline_s"].(float64) != 600 {
		t.Errorf("max_offline_s=%v", body["max_offline_s"])
	}
	if _, ok := body["name"]; ok {
		t.Errorf("name must be omitted when nil: %v", body)
	}
}
