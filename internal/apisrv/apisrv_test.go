package apisrv

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/davidg238/porta/internal/store"
)

// newTestHandler builds an apisrv.Handler over a fresh temp store with a fixed
// clock, returning both so tests can seed nodes and assert queued commands.
func newTestHandler(t *testing.T) (*Handler, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/d.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	h := New(st)
	h.now = func() int64 { return 1000 }
	return h, st
}

func TestWriteOK(t *testing.T) {
	rec := httptest.NewRecorder()
	writeOK(rec, map[string]any{"command_id": 7})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type=%q", ct)
	}
	var env struct {
		OK    bool           `json:"ok"`
		Data  map[string]int `json:"data"`
		Error string         `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if !env.OK || env.Data["command_id"] != 7 || env.Error != "" {
		t.Errorf("env=%+v", env)
	}
}

func TestWriteErr(t *testing.T) {
	rec := httptest.NewRecorder()
	writeErr(rec, http.StatusNotFound, "no such node")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d", rec.Code)
	}
	var env struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	json.Unmarshal(rec.Body.Bytes(), &env)
	if env.OK || env.Error != "no such node" {
		t.Errorf("env=%+v", env)
	}
}

func TestResolveSel(t *testing.T) {
	h, st := newTestHandler(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	st.SetNodeName("aabbccddeeff", "blinky")
	// By id and by name both resolve.
	for _, sel := range []string{"aabbccddeeff", "blinky"} {
		rec := httptest.NewRecorder()
		id, ok := h.resolveSel(rec, sel)
		if !ok || id != "aabbccddeeff" {
			t.Errorf("sel %q → id=%q ok=%v", sel, id, ok)
		}
	}
	// Unknown selector writes a 404 envelope and returns ok=false.
	rec := httptest.NewRecorder()
	if _, ok := h.resolveSel(rec, "ghost"); ok || rec.Code != http.StatusNotFound {
		t.Errorf("unknown sel: ok=%v code=%d", ok, rec.Code)
	}
}
