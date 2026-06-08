// Copyright (c) 2026 Ekorau LLC

package web

import (
	"net/http"

	"github.com/davidg238/porta/internal/control"
	"github.com/davidg238/porta/internal/store"
)

// The web console is a read-only dashboard for node state; the only write it
// keeps is the gateway-side rename below. It never queues a command to the node
// — node-command writes go through the CLI / nodus. (max-offline is retired:
// the offline window is now derived as 3×cadence from the node_config echo.)

func (h *Handler) postRename(w http.ResponseWriter, r *http.Request, n *store.Node) {
	name := r.FormValue("name")
	if name == "" {
		http.Error(w, "empty name", http.StatusBadRequest)
		return
	}
	if err := control.Rename(h.st, n.ID, name); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	n2, err := h.st.GetNode(n.ID)
	if err != nil || n2 == nil {
		http.Error(w, "node lookup failed", http.StatusInternalServerError)
		return
	}
	h.render(w, "node-header", h.detailVM(n2))
}
