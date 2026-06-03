package apisrv

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func patchNode(t *testing.T, h *Handler, sel, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest("PATCH", "/api/nodes/"+sel, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestPatchNodeRename(t *testing.T) {
	h, st := newTestHandler(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	rec := patchNode(t, h, "aabbccddeeff", `{"name":"newname"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	n, _ := st.GetNode("aabbccddeeff")
	if n.Name != "newname" {
		t.Errorf("name=%q", n.Name)
	}
}

func TestPatchNodeMaxOffline(t *testing.T) {
	h, st := newTestHandler(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	rec := patchNode(t, h, "aabbccddeeff", `{"max_offline_s":600}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	n, _ := st.GetNode("aabbccddeeff")
	if n.MaxOfflineS != 600 {
		t.Errorf("max_offline_s=%d", n.MaxOfflineS)
	}
}
