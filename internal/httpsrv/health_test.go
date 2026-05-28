// internal/httpsrv/health_test.go
package httpsrv

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/davidg238/porta/internal/store"
)

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/h.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestHealthHandlerReturnsOK(t *testing.T) {
	st := openTestStore(t)
	h := healthHandler(st)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/health", nil))
	if w.Code != http.StatusOK {
		t.Errorf("got %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var body struct {
		Status string `json:"status"`
		Nodes  int    `json:"nodes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body not valid JSON: %v (body=%s)", err, w.Body.String())
	}
	if body.Status != "ok" {
		t.Errorf("status=%q, want ok", body.Status)
	}
	if body.Nodes != 0 {
		t.Errorf("nodes=%d, want 0 (fresh store)", body.Nodes)
	}
}

func TestHealthHandlerCountsNodes(t *testing.T) {
	st := openTestStore(t)
	st.EnsureNode("dev1", 1000)
	st.EnsureNode("dev2", 1000)
	st.EnsureNode("dev3", 1000)
	h := healthHandler(st)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/health", nil))
	var body struct {
		Nodes int `json:"nodes"`
	}
	json.Unmarshal(w.Body.Bytes(), &body)
	if body.Nodes != 3 {
		t.Errorf("nodes=%d, want 3", body.Nodes)
	}
}
