package apisrv

import (
	"bytes"
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
