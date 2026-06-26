// Copyright (c) 2026 Ekorau LLC

package apisrv

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/davidg238/porta/internal/control"
)

// handleDebugSend: POST /api/nodes/{sel}/debug/send  body {"line":"dbg:methods"}
func (h *Handler) handleDebugSend(w http.ResponseWriter, r *http.Request) {
	id, ok := h.resolveSel(w, r.PathValue("sel"))
	if !ok {
		return
	}
	var body struct {
		Line string `json:"line"`
	}
	if err := json.Unmarshal(readBody(r), &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if body.Line == "" {
		writeErr(w, http.StatusBadRequest, "line must not be empty")
		return
	}
	cid, err := control.DebugSend(h.st, id, body.Line, "api", h.now())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeOK(w, map[string]any{"request_id": cid, "node_id": id})
}

// handleDebugResponses: GET /api/nodes/{sel}/debug/responses?after=N
func (h *Handler) handleDebugResponses(w http.ResponseWriter, r *http.Request) {
	id, ok := h.resolveSel(w, r.PathValue("sel"))
	if !ok {
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	rows, err := control.DebugResponses(h.st, id, after, 0)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, x := range rows {
		out = append(out, map[string]any{"id": x.ID, "line": x.Line})
	}
	writeOK(w, map[string]any{"node_id": id, "responses": out})
}
