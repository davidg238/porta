package web

import (
	"bytes"
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"github.com/davidg238/porta/internal/command"
	"github.com/davidg238/porta/internal/config"
	"github.com/davidg238/porta/internal/control"
	"github.com/davidg238/porta/internal/store"
)

const maxUpload = 8 << 20 // hard cap on an uploaded image, enforced via http.MaxBytesReader

// confirm renders the post-write confirmation + the refreshed recent panel,
// so a single swap shows both "queued #N" and the updated command timeline.
// The node-recent partial re-emits the #recent wrapper, so an hx-swap=outerHTML
// targeting #recent replaces the right element.
func (h *Handler) confirm(w http.ResponseWriter, n *store.Node, msg string) {
	vm := h.detailVM(n)
	var buf bytes.Buffer
	fmt.Fprintf(&buf, `<p class="confirm">%s — delivers on next check-in (%s)</p>`,
		template.HTMLEscapeString(msg), template.HTMLEscapeString(vm.Gauge.Label))
	if err := h.tmpl.ExecuteTemplate(&buf, "node-recent", vm); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
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
	id, err := control.SetPollInterval(h.st, n.ID, secs, "web", h.now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.confirm(w, n, fmt.Sprintf("queued #%d set-poll-interval %ds", id, secs))
}

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

func (h *Handler) postInstall(w http.ResponseWriter, r *http.Request, n *store.Node) {
	// MaxBytesReader is the real size cap: ParseMultipartForm's argument is only
	// the in-memory threshold (larger parts spill to temp files), so on its own
	// it would let an arbitrarily large image through to control.Install's
	// io.ReadAll. MaxBytesReader makes ParseMultipartForm fail past the limit.
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
	if err := r.ParseMultipartForm(maxUpload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	file, hdr, err := r.FormFile("image")
	if err != nil {
		http.Error(w, "image file required", http.StatusBadRequest)
		return
	}
	defer file.Close()
	name := r.FormValue("name")
	if name == "" {
		name = strings.TrimSuffix(hdr.Filename, ".bin")
	}
	if name == "" {
		http.Error(w, "container name required", http.StatusBadRequest)
		return
	}
	opts := control.InstallOpts{Lifecycle: r.FormValue("lifecycle"), Runlevel: 3}
	if iv := r.FormValue("interval"); iv != "" {
		secs, err := command.ParseDurationSeconds(iv)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		opts.IntervalS = secs
	}
	id, err := control.Install(h.st, n.ID, name, file, opts, "web", h.now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.confirm(w, n, fmt.Sprintf("queued #%d run %s (uploaded %d B)", id, name, hdr.Size))
}

func (h *Handler) postUninstall(w http.ResponseWriter, r *http.Request, n *store.Node) {
	name := r.FormValue("name")
	if name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	id, err := control.Uninstall(h.st, n.ID, name, "web", h.now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.confirm(w, n, fmt.Sprintf("queued #%d stop %s", id, name))
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
