// Copyright (c) 2026 Ekorau LLC

package apisrv

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/davidg238/porta/internal/store"
)

func telemetryHarness(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	st.InsertData("aabbccddeeff", 100, 0, "metric", "pm", int64(13), "", "int", "")
	st.InsertData("aabbccddeeff", 101, 1, "metric", "t", float64(20.5), "", "float", "")
	st.InsertData("aabbccddeeff", 102, 2, "log", "", nil, "hello", "", "")
	mux := http.NewServeMux()
	New(st).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, st
}

// getRows GETs the telemetry endpoint and returns the decoded rows.
func getRows(t *testing.T, srv *httptest.Server, query string) []map[string]any {
	t.Helper()
	resp, err := http.Get(srv.URL + "/api/nodes/aabbccddeeff/telemetry?" + query)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d for query %q", resp.StatusCode, query)
	}
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Rows []map[string]any `json:"rows"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if !env.OK {
		t.Fatalf("ok=false for query %q", query)
	}
	return env.Data.Rows
}

func TestTelemetryWindowMode(t *testing.T) {
	srv, _ := telemetryHarness(t)
	rows := getRows(t, srv, "since=0")
	if len(rows) != 3 {
		t.Fatalf("window since=0 got %d rows, want 3", len(rows))
	}
	if rows[0]["id"].(float64) != 1 || rows[0]["value_type"] != "int" {
		t.Fatalf("row0 = %+v", rows[0])
	}
}

func TestTelemetryCursorModeTakesPrecedence(t *testing.T) {
	srv, _ := telemetryHarness(t)
	// after=1 should skip id=1 even though since=0 would include it.
	rows := getRows(t, srv, "after=1&since=0")
	if len(rows) != 2 || rows[0]["id"].(float64) != 2 {
		t.Fatalf("after=1 got %+v, want ids 2,3", rows)
	}
}

func TestTelemetryKindFilter(t *testing.T) {
	srv, _ := telemetryHarness(t)
	rows := getRows(t, srv, "since=0&kind=log")
	if len(rows) != 1 || rows[0]["text"] != "hello" {
		t.Fatalf("kind=log got %+v", rows)
	}
}

func TestTelemetryBadParam400(t *testing.T) {
	srv, _ := telemetryHarness(t)
	resp, err := http.Get(srv.URL + "/api/nodes/aabbccddeeff/telemetry?since=notanumber")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status %d, want 400", resp.StatusCode)
	}
}

func TestTelemetryNegativeParam400(t *testing.T) {
	srv, _ := telemetryHarness(t)
	resp, err := http.Get(srv.URL + "/api/nodes/aabbccddeeff/telemetry?limit=-5")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status %d, want 400", resp.StatusCode)
	}
}

func TestTelemetryUnknownSelector404(t *testing.T) {
	srv, _ := telemetryHarness(t)
	resp, err := http.Get(srv.URL + "/api/nodes/nosuchnode/telemetry?since=0")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("status %d, want 404", resp.StatusCode)
	}
}

func TestTelemetryRowCarriesLevel(t *testing.T) {
	srv, st := telemetryHarness(t)
	_ = st.InsertData("aabbccddeeff", 10, 0, "log", "", nil, "stall", "", "warn")
	resp, err := http.Get(srv.URL + "/api/nodes/aabbccddeeff/telemetry?since=0")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"level":"warn"`) {
		t.Fatalf("response missing level: %s", body)
	}
}
