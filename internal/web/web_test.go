package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/davidg238/porta/internal/store"
)

func testStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func serve(t *testing.T, st *store.Store) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	New(st).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestNodeTableRendersRowAndGauge(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	srv := serve(t, st)

	body := readBody(t, mustGet(t, srv.URL+"/"))
	if !strings.Contains(body, "192.168.1.9") || !strings.Contains(body, "gauge") {
		t.Errorf("row/gauge missing: %s", body)
	}
	p := readBody(t, mustGet(t, srv.URL+"/partials/nodes"))
	if !strings.Contains(p, "<tbody") || !strings.Contains(p, "aabbccddeeff") {
		t.Errorf("partial missing tbody/node: %s", p)
	}
}

func mustGet(t *testing.T, url string) *http.Response {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestIndexRendersNavAndAssets(t *testing.T) {
	st := testStore(t)
	srv := serve(t, st)

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, resp)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if !strings.Contains(body, "Nodes") || !strings.Contains(body, "Command Log") {
		t.Errorf("nav missing: %s", body)
	}
	js, _ := http.Get(srv.URL + "/assets/htmx.min.js")
	if js.StatusCode != 200 {
		t.Errorf("htmx asset status %d", js.StatusCode)
	}
	nf, _ := http.Get(srv.URL + "/nope")
	if nf.StatusCode != 404 {
		t.Errorf("unknown path got %d, want 404", nf.StatusCode)
	}
}
