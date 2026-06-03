package apisrv

import (
	"encoding/json"
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

func TestPatchNodeUnknownNode(t *testing.T) {
	h, _ := newTestHandler(t)
	rec := patchNode(t, h, "ghost", `{"name":"x"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestPatchNodeBothFields(t *testing.T) {
	h, st := newTestHandler(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	rec := patchNode(t, h, "aabbccddeeff", `{"name":"both","max_offline_s":300}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	n, _ := st.GetNode("aabbccddeeff")
	if n.Name != "both" {
		t.Errorf("name=%q, want %q", n.Name, "both")
	}
	if n.MaxOfflineS != 300 {
		t.Errorf("max_offline_s=%d, want 300", n.MaxOfflineS)
	}
}

func TestGetNodeDetail(t *testing.T) {
	h, st := newTestHandler(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	st.SetNodeName("aabbccddeeff", "blinky")
	st.UpdateNodeIdentity("aabbccddeeff", "esp32", "v2.0.0-alpha.192")

	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest("GET", "/api/nodes/blinky", nil) // resolve by name
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			ID, Name, Chip, Sdk string
			PollIntervalS       int64 `json:"poll_interval_s"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.ID != "aabbccddeeff" || env.Data.Name != "blinky" || env.Data.Chip != "esp32" || env.Data.Sdk != "v2.0.0-alpha.192" {
		t.Errorf("detail=%+v", env.Data)
	}
}

func TestGetNodeDetailUnknown(t *testing.T) {
	h, _ := newTestHandler(t)
	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest("GET", "/api/nodes/ghost", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestGetNodesList(t *testing.T) {
	h, st := newTestHandler(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	st.SetNodeName("aabbccddeeff", "blinky")
	st.UpdateNodeIdentity("aabbccddeeff", "esp32c6", "v2.0.0-alpha.192")

	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest("GET", "/api/nodes", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Nodes []struct {
				ID, Name, Kind, IP, Chip, Sdk string
			} `json:"nodes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Data.Nodes) != 1 {
		t.Fatalf("nodes=%+v", env.Data.Nodes)
	}
	n := env.Data.Nodes[0]
	if n.ID != "aabbccddeeff" || n.Name != "blinky" || n.Chip != "esp32c6" || n.Sdk != "v2.0.0-alpha.192" {
		t.Errorf("node=%+v", n)
	}
}
