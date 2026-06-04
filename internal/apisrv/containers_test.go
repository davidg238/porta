// Copyright (c) 2026 Ekorau LLC

package apisrv

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

// postContainer builds a multipart install request with the given image bytes
// and form fields, and returns the recorder.
func postContainer(t *testing.T, h *Handler, sel string, img []byte, fields map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("image", "app.bin")
	fw.Write(img)
	for k, v := range fields {
		mw.WriteField(k, v)
	}
	mw.Close()

	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest("POST", "/api/nodes/"+sel+"/containers", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestPostContainerInstall(t *testing.T) {
	h, st := newTestHandler(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	rec := postContainer(t, h, "aabbccddeeff", []byte("IMAGEBYTES"),
		map[string]string{"name": "blink", "lifecycle": "run-loop", "runlevel": "3"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	cmd, err := st.NextUndelivered("aabbccddeeff")
	if err != nil || cmd == nil || cmd.Verb != "run" {
		t.Fatalf("expected queued run, got %+v (err %v)", cmd, err)
	}
}

// TestPostContainerInstallRunlevelAndTriggers verifies that the runlevel and
// trigger fields sent in a multipart install request actually land in the queued
// run command's args JSON.  This exercises the plumbing from
// handleContainerInstall → control.Install → command.Run.
func TestPostContainerInstallRunlevelAndTriggers(t *testing.T) {
	h, st := newTestHandler(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)

	// Build a multipart request with runlevel=5, lifecycle=run-loop, and two
	// trigger entries (boot and interval=60).
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("image", "widget.bin")
	fw.Write([]byte("IMAGEBYTES"))
	mw.WriteField("name", "widget")
	mw.WriteField("runlevel", "5")
	mw.WriteField("lifecycle", "run-loop")
	mw.WriteField("trigger", "boot")
	mw.WriteField("trigger", "interval=60")
	mw.Close()

	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest("POST", "/api/nodes/aabbccddeeff/containers", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	cmd, err := st.NextUndelivered("aabbccddeeff")
	if err != nil || cmd == nil {
		t.Fatalf("no queued command (err=%v)", err)
	}
	if cmd.Verb != "run" {
		t.Fatalf("verb=%q, want run", cmd.Verb)
	}

	// Decode the args JSON to verify runlevel, lifecycle, and triggers.
	var args struct {
		Runlevel  int                `json:"runlevel"`
		Lifecycle string             `json:"lifecycle"`
		Triggers  map[string]float64 `json:"triggers"` // json.Unmarshal defaults to float64
	}
	if err := json.Unmarshal([]byte(cmd.Args), &args); err != nil {
		t.Fatalf("decode args %q: %v", cmd.Args, err)
	}
	if args.Runlevel != 5 {
		t.Errorf("runlevel=%d, want 5", args.Runlevel)
	}
	if args.Lifecycle != "run-loop" {
		t.Errorf("lifecycle=%q, want run-loop", args.Lifecycle)
	}
	if _, hasBoot := args.Triggers["boot"]; !hasBoot {
		t.Errorf("triggers=%v, want boot key", args.Triggers)
	}
	if _, hasInterval := args.Triggers["interval"]; !hasInterval {
		t.Errorf("triggers=%v, want interval key", args.Triggers)
	}
	if args.Triggers["interval"] != 60 {
		t.Errorf("triggers[interval]=%v, want 60", args.Triggers["interval"])
	}
}

func TestPostContainerBadRunlevel(t *testing.T) {
	h, st := newTestHandler(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	rec := postContainer(t, h, "aabbccddeeff", []byte("IMAGEBYTES"),
		map[string]string{"name": "blink", "runlevel": "abc"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad runlevel: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if cmd, _ := st.NextUndelivered("aabbccddeeff"); cmd != nil {
		t.Fatalf("bad runlevel must not queue a command, got %+v", cmd)
	}
}

// TestPostContainerEmptyName verifies that a filename that strips to "" (e.g. ".bin")
// with no explicit name field yields a 400 and no queued command.
func TestPostContainerEmptyName(t *testing.T) {
	h, st := newTestHandler(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)

	// Build multipart inline so we can use filename ".bin" (strips to "").
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("image", ".bin") // TrimSuffix(".bin", ".bin") == ""
	fw.Write([]byte("IMAGEBYTES"))
	// no "name" field
	mw.Close()

	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest("POST", "/api/nodes/aabbccddeeff/containers", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty name: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if cmd, _ := st.NextUndelivered("aabbccddeeff"); cmd != nil {
		t.Fatalf("empty name must not queue a command, got %+v", cmd)
	}
}

// TestInstallEchoesNodeID asserts the install response carries the resolved id.
func TestInstallEchoesNodeID(t *testing.T) {
	h, st := newTestHandler(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	st.SetNodeName("aabbccddeeff", "blinky")

	rec := postContainer(t, h, "blinky", []byte("fake-image-bytes"), map[string]string{
		"name": "blink", "lifecycle": "run-once", "runlevel": "3",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			NodeID string `json:"node_id"`
			Size   int64  `json:"size"`
		} `json:"data"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Data.NodeID != "aabbccddeeff" {
		t.Errorf("node_id=%q", resp.Data.NodeID)
	}
}

func TestPostContainerOversizeRejected(t *testing.T) {
	h, st := newTestHandler(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	// An image one byte over the cap trips MaxBytesReader → ParseMultipartForm
	// fails → 400 (never reaches control.Install).
	big := make([]byte, maxUpload+1)
	rec := postContainer(t, h, "aabbccddeeff", big, map[string]string{"name": "blink"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("oversize upload status=%d, want 400", rec.Code)
	}
	if cmd, _ := st.NextUndelivered("aabbccddeeff"); cmd != nil {
		t.Fatalf("oversize upload must not queue a command, got %+v", cmd)
	}
}
