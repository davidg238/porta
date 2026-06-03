package apisrv

import (
	"encoding/json"
	"net/http"

	"github.com/davidg238/porta/internal/control"
)

// nodeListItem is one row of GET /api/nodes.
type nodeListItem struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	IP       string `json:"ip"`
	LastSeen int64  `json:"last_seen"`
	Online   bool   `json:"online"`
	Chip     string `json:"chip"`
	Sdk      string `json:"sdk"`
}

// handleListNodes returns the fleet list, including self-reported identity.
func (h *Handler) handleListNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := h.st.ListNodes()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	now := h.now()
	out := make([]nodeListItem, 0, len(nodes))
	for i := range nodes {
		n := &nodes[i]
		out = append(out, nodeListItem{
			ID: n.ID, Name: n.Name, Kind: n.Kind, IP: n.SourceAddr,
			LastSeen: n.LastSeen.Int64, Online: n.Online(now),
			Chip: n.Chip, Sdk: n.Sdk,
		})
	}
	writeOK(w, map[string]any{"nodes": out})
}

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
