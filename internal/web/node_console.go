// Copyright (c) 2026 Ekorau LLC

// node_console.go is part of porta's OPTIONAL telemetry surface: the per-node
// Prints/Logs console panels on the node detail page. It is self-contained so
// telemetry can be excised in one bounded change (see telemetry.go's recipe):
// delete this file + node_console.html, the two cases in pages.go's
// handleNodeSub, and the telemetry:node-console block in node.html.
package web

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"

	"github.com/davidg238/porta/internal/store"
	"github.com/davidg238/porta/internal/telemetry"
)

// consoleVM backs the node-prints template. Lines are pre-formatted console rows
// (via telemetry.FormatLine), in chronological order (oldest→newest) so the
// newest line sits at the bottom, like a terminal tail.
type consoleVM struct {
	ID    string
	Title string
	Lines []string
	Empty string
}

// renderNodeConsole renders the Prints panel (def "node-prints") for the node,
// showing the newest 50 rows of the given kinds.
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

// logLine is one row of the Logs panel. A panic row is split so a decode link
// can sit between the "panic" column and the still-visible raw blob; every other
// row is the flat FormatLine string in Text (DecodeHref empty).
type logLine struct {
	Text string // full FormatLine output; used when DecodeHref == ""
	Pre  string // panic only: "<ts>  panic   " (matches FormatLine spacing)
	// DecodeHref is the panic decode link (nodus://decode?node=&blob=). Typed
	// template.URL so html/template does not neutralize the non-http scheme;
	// safe because it is built from url.Values.Encode with a fixed prefix.
	DecodeHref template.URL
	Blob       string // panic only: the raw base64 panic message
}

// logsVM backs the node-logs template.
type logsVM struct {
	ID    string
	Title string
	Lines []logLine
	Empty string
}

// renderNodeLogs renders the Logs panel (kinds log+panic). panic rows carry a
// nodus://decode link; all other rows render exactly as FormatLine produces them.
func (h *Handler) renderNodeLogs(w http.ResponseWriter, n *store.Node) {
	rows, err := h.st.RecentByKinds(n.ID, []string{"log", "panic"}, 50)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	lines := make([]logLine, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- { // newest-first → chronological
		r := rows[i]
		if r.Kind == "panic" {
			href := "nodus://decode?" + url.Values{"node": {n.ID}, "blob": {r.Text}}.Encode()
			lines = append(lines, logLine{
				Pre:        telemetry.FormatTS(r.TS) + "  " + fmt.Sprintf("%-7s ", "panic"),
				DecodeHref: template.URL(href),
				Blob:       r.Text,
			})
			continue
		}
		lines = append(lines, logLine{Text: telemetry.FormatLine(r)})
	}
	h.render(w, "node-logs", logsVM{
		ID:    n.ID,
		Title: "Logs",
		Lines: lines,
		Empty: "no logs — forwarding may be off (set-forward --log on)",
	})
}
