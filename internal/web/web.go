// Package web serves porta's htmx operator console on the shared HTTP mux.
// It reads through internal/store and writes through internal/control; it
// holds no node state and pushes nothing — every dynamic region is polled.
package web

import (
	"embed"
	"html/template"
	"net/http"
	"time"

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

func (h *Handler) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	h.render(w, "index", map[string]any{"Title": "Nodes"})
}

// render executes a template and writes 500 on error.
func (h *Handler) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// Temporary stubs (filled in later tasks).
func (h *Handler) handleNode(w http.ResponseWriter, r *http.Request)         { http.NotFound(w, r) }
func (h *Handler) handleLog(w http.ResponseWriter, r *http.Request)          { h.render(w, "index", map[string]any{"Title": "Command Log"}) }
func (h *Handler) handleNodesPartial(w http.ResponseWriter, r *http.Request) { w.Write([]byte("<tbody></tbody>")) }
func (h *Handler) handleLogPartial(w http.ResponseWriter, r *http.Request)   { w.Write([]byte("<tbody></tbody>")) }
