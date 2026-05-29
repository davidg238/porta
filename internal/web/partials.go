package web

import (
	"fmt"
	"net/http"

	"github.com/davidg238/porta/internal/command"
	"github.com/davidg238/porta/internal/config"
	"github.com/davidg238/porta/internal/control"
	"github.com/davidg238/porta/internal/store"
)

// confirm renders the post-write confirmation + the refreshed pending panel,
// so a single swap shows both "queued #N" and the new queue state. The
// node-pending partial re-emits the #pending wrapper, so an hx-swap=outerHTML
// targeting #pending replaces the right element.
func (h *Handler) confirm(w http.ResponseWriter, n *store.Node, msg string) {
	vm := h.detailVM(n)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<p class="confirm">%s — delivers on next check-in (%s)</p>`, msg, vm.Gauge.Label)
	_ = h.tmpl.ExecuteTemplate(w, "node-pending", vm)
}

func (h *Handler) postSet(w http.ResponseWriter, r *http.Request, n *store.Node) {
	app, key, val := r.FormValue("app"), r.FormValue("key"), r.FormValue("value")
	id, err := control.Set(h.st, n.ID, app, key, config.InferScalar(val), "web", h.now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.confirm(w, n, fmt.Sprintf("queued #%d set %s.%s=%s", id, app, key, val))
}

func (h *Handler) postConsole(w http.ResponseWriter, r *http.Request, n *store.Node) {
	on := r.FormValue("state") == "on"
	id, err := control.SetConsole(h.st, n.ID, on, "web", h.now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.confirm(w, n, fmt.Sprintf("queued #%d set-console %s", id, r.FormValue("state")))
}

func (h *Handler) postPollInterval(w http.ResponseWriter, r *http.Request, n *store.Node) {
	secs, err := command.ParseDurationSeconds(r.FormValue("dur"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id, _ := control.SetPollInterval(h.st, n.ID, secs, "web", h.now())
	h.confirm(w, n, fmt.Sprintf("queued #%d set-poll-interval %ds", id, secs))
}

func (h *Handler) postMaxOffline(w http.ResponseWriter, r *http.Request, n *store.Node) {
	secs, err := command.ParseDurationSeconds(r.FormValue("dur"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = control.SetMaxOffline(h.st, n.ID, secs)
	n2, _ := h.st.GetNode(n.ID)
	h.render(w, "node-header", h.detailVM(n2))
}

func (h *Handler) postRename(w http.ResponseWriter, r *http.Request, n *store.Node) {
	name := r.FormValue("name")
	if name == "" {
		http.Error(w, "empty name", http.StatusBadRequest)
		return
	}
	_ = control.Rename(h.st, n.ID, name)
	n2, _ := h.st.GetNode(n.ID)
	h.render(w, "node-header", h.detailVM(n2))
}
