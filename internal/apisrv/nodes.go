package apisrv

import (
	"encoding/json"
	"net/http"

	"github.com/davidg238/porta/internal/control"
)

// nodePatch carries optional node-management settings; pointer fields let the
// handler apply only what was sent.
type nodePatch struct {
	Name        *string `json:"name"`
	MaxOfflineS *int64  `json:"max_offline_s"`
}

// handlePatchNode applies rename / max-offline node settings (gateway-side,
// not device commands).
func (h *Handler) handlePatchNode(w http.ResponseWriter, r *http.Request) {
	id, ok := h.resolveSel(w, r.PathValue("sel"))
	if !ok {
		return
	}
	var p nodePatch
	if err := json.Unmarshal(readBody(r), &p); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if p.Name != nil {
		if err := control.Rename(h.st, id, *p.Name); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if p.MaxOfflineS != nil {
		if err := control.SetMaxOffline(h.st, id, *p.MaxOfflineS); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	writeOK(w, map[string]any{})
}
