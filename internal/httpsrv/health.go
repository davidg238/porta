// Copyright (c) 2026 Ekorau LLC

// internal/httpsrv/health.go
package httpsrv

import (
	"encoding/json"
	"net/http"

	"github.com/davidg238/porta/internal/serverstat"
	"github.com/davidg238/porta/internal/store"
)

// healthHandler returns 200 with a small JSON body summarizing the
// gateway's state for uptime checks and the future ops dashboard:
//
//   {"status":"ok","nodes":<int>}
//
// The endpoint reports the listener itself is healthy. A transient
// store.ListNodes failure renders nodes:-1 but still 200 — the HTTP
// listener is up even if the DB blip is in progress.
// statsFn returns the live stats holder (or nil) at request time, so a stats
// holder attached after New still enriches the response.
func healthHandler(st *store.Store, statsFn func() *serverstat.Stats) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		nodes := -1
		if rows, err := st.ListNodes(); err == nil {
			nodes = len(rows)
		}
		out := map[string]any{"status": "ok", "nodes": nodes}
		// Keep /health a cheap liveness probe: only the in-memory stats (version,
		// uptime, reject count) — no PRAGMA/stat. The heavy db metrics live in
		// /api/status.
		if statsFn != nil {
			if s := statsFn(); s != nil {
				snap := s.Snapshot()
				out["version"] = snap.Version
				out["uptime_s"] = snap.UptimeSeconds
				out["reports_rejected"] = snap.ReportsRejected
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(out)
	}
}
