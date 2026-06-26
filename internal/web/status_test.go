// Copyright (c) 2026 Ekorau LLC

package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/davidg238/porta/internal/serverstat"
	"github.com/davidg238/porta/internal/store"
)

// serveStats mounts the console with a stats holder and a fixed clock, so the
// status page can render per-transport counters and a deterministic uptime.
func serveStats(t *testing.T, st *store.Store, stats *serverstat.Stats, clock func() int64) *httptest.Server {
	t.Helper()
	h := New(st).WithStats(stats)
	h.now = clock
	mux := http.NewServeMux()
	h.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestStatusPageRendersBuildAndTransports(t *testing.T) {
	st := testStore(t)
	// A v4 node lights the wifi row; a v6 node lights the thread row.
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	st.TouchNode("001122334455", "[fd00::1]:6970", 1000)

	clock := func() int64 { return 1100 } // 100s uptime
	stats := serverstat.New("0.7.4", "deadbeef", clock)
	stats.Packet(serverstat.WiFi, 512)
	stats.ReportOK()
	stats.ReportRejected()

	srv := serveStats(t, st, stats, clock)

	body := readBody(t, mustGet(t, srv.URL+"/status"))
	for _, want := range []string{"0.7.4", "deadbeef", "wifi", "thread", "espnow", "sqlite", "nodes"} {
		if !strings.Contains(body, want) {
			t.Errorf("status page missing %q in:\n%s", want, body)
		}
	}
}

func TestStatusPartialPolls(t *testing.T) {
	st := testStore(t)
	clock := func() int64 { return 1100 }
	stats := serverstat.New("0.7.4", "deadbeef", clock)
	srv := serveStats(t, st, stats, clock)

	p := readBody(t, mustGet(t, srv.URL+"/partials/status"))
	if !strings.Contains(p, `hx-get="/partials/status"`) {
		t.Errorf("status partial missing self-poll: %s", p)
	}
	// Dense layout: cards tile in a grid and the tables size to content
	// (table.compact) instead of stretching full-width.
	if !strings.Contains(p, `class="status-grid"`) {
		t.Errorf("status partial missing tiled grid container: %s", p)
	}
	if !strings.Contains(p, "compact") {
		t.Errorf("status partial missing compact tables: %s", p)
	}
}

func TestStatusPageRendersWithoutStats(t *testing.T) {
	st := testStore(t)
	srv := serve(t, st) // no stats attached
	resp := mustGet(t, srv.URL+"/status")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status without stats = %d, want 200", resp.StatusCode)
	}
}
