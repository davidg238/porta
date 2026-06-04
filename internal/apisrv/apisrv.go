// Copyright (c) 2026 Ekorau LLC

// Package apisrv exposes porta's control plane as an authenticated JSON HTTP
// API on the shared operator listener, so a CLI (and future language tooling)
// can drive the gateway over the network instead of opening the store directly.
// It is a thin adapter over internal/control + internal/store; control/store
// stays the single writer. Every response is a {ok,data,error} envelope plus a
// meaningful HTTP status.
package apisrv

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/davidg238/porta/internal/control"
	"github.com/davidg238/porta/internal/store"
)

// Handler holds the store and a clock. now is injectable for tests.
type Handler struct {
	st  *store.Store
	now func() int64
}

// New builds a Handler over st with a wall-clock now (Unix seconds).
func New(st *store.Store) *Handler {
	return &Handler{st: st, now: func() int64 { return time.Now().Unix() }}
}

// recoverer wraps h so a panic in any API handler becomes a 500 {ok:false}
// envelope instead of aborting the connection (spec §6 panic-safety).
func recoverer(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				writeErr(w, http.StatusInternalServerError, "internal error")
			}
		}()
		h(w, r)
	}
}

// Register mounts the API routes on mux. Routes use Go 1.22+ method patterns;
// the shared mux's CIDR allowlist middleware (applied by httpsrv) covers them.
// Every handler is wrapped with recoverer so panics produce a 500 envelope
// rather than aborting the connection (spec §6).
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/nodes", recoverer(h.handleListNodes))
	mux.HandleFunc("GET /api/nodes/{sel}", recoverer(h.handleNodeDetail))
	mux.HandleFunc("POST /api/nodes/{sel}/commands", recoverer(h.handleCommand))
	mux.HandleFunc("POST /api/nodes/{sel}/containers", recoverer(h.handleContainerInstall))
	mux.HandleFunc("PATCH /api/nodes/{sel}", recoverer(h.handlePatchNode))
	mux.HandleFunc("GET /api/nodes/{sel}/commands", recoverer(h.handleNodeCommands))
	mux.HandleFunc("GET /api/nodes/{sel}/config", recoverer(h.handleNodeConfig))
	mux.HandleFunc("GET /api/nodes/{sel}/telemetry", recoverer(h.handleTelemetry))
}

// envelope is the uniform response shape, echoing jast-gw's Response.
type envelope struct {
	OK    bool   `json:"ok"`
	Data  any    `json:"data"`
	Error string `json:"error"`
}

// writeOK emits a 200 {ok:true,data,error:""} response.
func writeOK(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, envelope{OK: true, Data: data})
}

// writeErr emits a non-2xx {ok:false,data:null,error} response.
func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, envelope{OK: false, Error: msg})
}

// writeJSON sets Content-Type, writes the status, and JSON-encodes env.
func writeJSON(w http.ResponseWriter, status int, env envelope) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(env)
}

// readBody reads the full request body. Network/read errors are intentionally
// ignored: the truncated bytes will cause a JSON parse failure downstream,
// which produces a clear 400 response without needing a separate error path.
func readBody(r *http.Request) []byte {
	b, _ := io.ReadAll(r.Body)
	return b
}

// resolveSel resolves a {sel} path value (node id or name) to a node id.
// On failure it writes a 404 envelope and returns ok=false.
func (h *Handler) resolveSel(w http.ResponseWriter, sel string) (string, bool) {
	id, err := control.ResolveNodeID(h.st, sel)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return "", false
	}
	return id, true
}
