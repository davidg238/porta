// Copyright (c) 2026 Ekorau LLC

package apisrv

import (
	"encoding/base64"
	"net/http"
	"strconv"

	"github.com/davidg238/porta/internal/control"
)

// handleProfileList: GET /api/nodes/{sel}/profile?after=N — result rows, no blob.
func (h *Handler) handleProfileList(w http.ResponseWriter, r *http.Request) {
	id, ok := h.resolveSel(w, r.PathValue("sel"))
	if !ok {
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	rows, err := control.ProfileResults(h.st, id, after, 0)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, x := range rows {
		out = append(out, map[string]any{
			"seq": x.Seq, "ts": x.TS, "app": x.App, "label": x.Label, "byte_len": x.ByteLen,
		})
	}
	writeOK(w, map[string]any{"node_id": id, "results": out})
}

// handleProfileGet: GET /api/nodes/{sel}/profile/{seq} — one result with blob (base64).
func (h *Handler) handleProfileGet(w http.ResponseWriter, r *http.Request) {
	id, ok := h.resolveSel(w, r.PathValue("sel"))
	if !ok {
		return
	}
	seq, err := strconv.ParseInt(r.PathValue("seq"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad seq")
		return
	}
	res, err := control.ProfileResult(h.st, id, seq)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if res == nil {
		writeErr(w, http.StatusNotFound, "no such profile result")
		return
	}
	writeOK(w, map[string]any{
		"node_id": id, "seq": res.Seq, "ts": res.TS, "app": res.App, "label": res.Label,
		"byte_len": res.ByteLen, "blob": base64.StdEncoding.EncodeToString(res.Blob),
	})
}
