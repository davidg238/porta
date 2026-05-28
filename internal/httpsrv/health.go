// internal/httpsrv/health.go
package httpsrv

import (
	"encoding/json"
	"net/http"

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
func healthHandler(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		nodes := -1
		if rows, err := st.ListNodes(); err == nil {
			nodes = len(rows)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "ok",
			"nodes":  nodes,
		})
	}
}
