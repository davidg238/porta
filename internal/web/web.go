// Package web serves porta's htmx operator console on the shared HTTP mux.
// It reads through internal/store and writes through internal/control; it
// holds no node state and pushes nothing — every dynamic region is polled.
package web

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/davidg238/porta/internal/control"
	"github.com/davidg238/porta/internal/store"
)

//go:embed templates/*.html assets/*
var content embed.FS

// Handler renders the operator console.
type Handler struct {
	st   *store.Store
	now  func() int64
	tmpl *template.Template
}

// New builds a Handler. now defaults to wall-clock epoch seconds.
func New(st *store.Store) *Handler {
	return &Handler{
		st:   st,
		now:  func() int64 { return time.Now().Unix() },
		tmpl: template.Must(template.ParseFS(content, "templates/*.html")),
	}
}

// Register mounts all routes on mux. "/" is the catch-all; it 404s any path
// it does not own so it never shadows sibling routes like /health.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.Handle("/assets/", http.FileServer(http.FS(content)))
	mux.HandleFunc("/", h.handleIndex)
	mux.HandleFunc("/n/", h.handleNode)
	mux.HandleFunc("/log", h.handleLog)
	mux.HandleFunc("/partials/nodes", h.handleNodesPartial)
	mux.HandleFunc("/partials/log", h.handleLogPartial)
}

type nodeRowVM struct {
	ID, Name, Kind, IP, SeenAgo, Summary string
	Gauge                                CheckinState
}

func (h *Handler) nodeRows(now int64) ([]nodeRowVM, error) {
	nodes, err := h.st.ListNodes()
	if err != nil {
		return nil, err
	}
	out := make([]nodeRowVM, 0, len(nodes))
	for _, n := range nodes {
		seen := "never"
		var lastSeen int64
		if n.LastSeen.Valid {
			lastSeen = n.LastSeen.Int64
			seen = control.RelativeAge(lastSeen, now)
		}
		out = append(out, nodeRowVM{
			ID: n.ID, Name: n.Name, Kind: n.Kind, IP: n.SourceAddr, SeenAgo: seen,
			Summary: summarize(n.ObservedState),
			Gauge:   Checkin(n.LastSeen.Valid, lastSeen, n.PollIntervalS, n.MaxOfflineS, now),
		})
	}
	return out, nil
}

// summarize renders the node-list "state summary" cell. Decode errors are
// best-effort: a malformed observed blob degrades to "idle · 0 cfg".
func summarize(observed string) string {
	apps, _ := control.AppsFromObserved(observed)
	cfg := control.ConfigFromObserved(observed)
	keys := 0
	for _, m := range cfg {
		keys += len(m)
	}
	if len(apps) == 0 {
		return fmt.Sprintf("idle · %d cfg", keys)
	}
	names := make([]string, 0, len(apps))
	for _, a := range apps {
		names = append(names, a.Name)
	}
	return fmt.Sprintf("%s · %d cfg", strings.Join(names, ","), keys)
}

func (h *Handler) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	rows, err := h.nodeRows(h.now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.render(w, "index", map[string]any{"Title": "Nodes", "Rows": rows})
}

// render executes a named template into a buffer; only writes to w on success
// so a template error yields a clean 500 instead of a partial body.
func (h *Handler) render(w http.ResponseWriter, name string, data any) {
	var buf bytes.Buffer
	if err := h.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}

func (h *Handler) handleNodesPartial(w http.ResponseWriter, r *http.Request) {
	rows, err := h.nodeRows(h.now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.render(w, "nodes-rows", rows)
}

// Temporary stubs (filled in later tasks).
func (h *Handler) handleLog(w http.ResponseWriter, r *http.Request)        { h.render(w, "index", map[string]any{"Title": "Command Log"}) }
func (h *Handler) handleLogPartial(w http.ResponseWriter, r *http.Request) { w.Write([]byte("<tbody></tbody>")) }
