// Copyright (c) 2026 Ekorau LLC

package web

import (
	"net/http"

	"github.com/davidg238/porta/internal/command"
	"github.com/davidg238/porta/internal/control"
	"github.com/davidg238/porta/internal/store"
)

// The web console is a read-only dashboard for node state; the only writes it
// keeps are the gateway-side node settings below (friendly name, offline
// threshold). They never queue a command to the node — node-command writes go
// through the CLI / nodus.

func (h *Handler) postMaxOffline(w http.ResponseWriter, r *http.Request, n *store.Node) {
	secs, err := command.ParseDurationSeconds(r.FormValue("dur"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := control.SetMaxOffline(h.st, n.ID, secs); err != nil {
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
