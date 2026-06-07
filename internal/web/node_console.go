// Copyright (c) 2026 Ekorau LLC

// node_console.go is part of porta's OPTIONAL telemetry surface: the per-node
// Prints/Logs console panels on the node detail page. It is self-contained so
// telemetry can be excised in one bounded change (see telemetry.go's recipe):
// delete this file + node_console.html, the two cases in pages.go's
// handleNodeSub, and the telemetry:node-console block in node.html.
package web

import (
	"net/http"

	"github.com/davidg238/porta/internal/store"
	"github.com/davidg238/porta/internal/telemetry"
)

// consoleVM backs the node-prints / node-logs templates. Lines are pre-formatted
// console rows (via telemetry.FormatLine), in chronological order (oldest→newest)
// so the newest line sits at the bottom, like a terminal tail.
type consoleVM struct {
	ID    string
	Title string
	Lines []string
	Empty string
}

// renderNodeConsole renders one console panel (def is "node-prints"/"node-logs")
// for the node, showing the newest 50 rows of the given kinds.
func (h *Handler) renderNodeConsole(w http.ResponseWriter, n *store.Node, def, title, empty string, kinds []string) {
	rows, err := h.st.RecentByKinds(n.ID, kinds, 50)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	lines := make([]string, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- { // newest-first → chronological
		lines = append(lines, telemetry.FormatLine(rows[i]))
	}
	h.render(w, def, consoleVM{ID: n.ID, Title: title, Lines: lines, Empty: empty})
}
