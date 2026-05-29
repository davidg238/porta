package web

import (
	"net/http"
	"strings"

	"github.com/davidg238/porta/internal/control"
	"github.com/davidg238/porta/internal/store"
)

// detailVM is the view model for the per-node detail page. Every polled
// section re-emits its own wrapper element so an outerHTML swap is idempotent.
type detailVM struct {
	Title    string
	ID       string
	Name     string
	Kind     string
	IP       string
	EUI      string
	PollIntv string
	Gauge    CheckinState
	Config   []control.ConfigRow
	ConfApp  string
	Telem    []store.DataRow
	Pending  []store.Command
	Apps     []control.App
}

// handleNode serves /n/<id> (full page) and /n/<id>/<section> (polled partial).
func (h *Handler) handleNode(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/n/")
	parts := strings.SplitN(rest, "/", 2)
	idArg := parts[0]
	if idArg == "" {
		http.NotFound(w, r)
		return
	}
	id, err := control.ResolveNodeID(h.st, idArg)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	n, err := h.st.GetNode(id)
	if err != nil || n == nil {
		http.NotFound(w, r)
		return
	}
	if len(parts) == 2 && parts[1] != "" {
		h.handleNodeSub(w, r, n, parts[1])
		return
	}
	h.render(w, "node", h.detailVM(n))
}

// handleNodeSub dispatches the polled detail partials. Tasks 6-8 extend this
// switch with POST actions (set / install / trigger).
func (h *Handler) handleNodeSub(w http.ResponseWriter, r *http.Request, n *store.Node, sub string) {
	vm := h.detailVM(n)
	switch sub {
	case "header":
		h.render(w, "node-header", vm)
	case "config":
		h.render(w, "node-config", vm)
	case "telemetry":
		h.render(w, "node-telemetry", vm)
	case "pending":
		h.render(w, "node-pending", vm)
	case "containers":
		h.render(w, "node-containers", vm)
	default:
		http.NotFound(w, r)
	}
}

// detailVM builds the per-node view model from store + control projections.
func (h *Handler) detailVM(n *store.Node) detailVM {
	now := h.now()
	app := firstApp(n)
	cfg, _ := control.DesiredVsObserved(h.st, n.ID, app)
	telem, _ := h.st.RecentData(n.ID, 10)
	pending, _ := h.st.UndeliveredCommands(n.ID)
	apps, _ := control.AppsFromObserved(n.ObservedState)
	var lastSeen int64
	if n.LastSeen.Valid {
		lastSeen = n.LastSeen.Int64
	}
	return detailVM{
		Title:    n.Name,
		ID:       n.ID,
		Name:     n.Name,
		Kind:     n.Kind,
		IP:       n.SourceAddr,
		EUI:      n.ID,
		PollIntv: humanizeDur(n.PollIntervalS),
		Gauge:    Checkin(n.LastSeen.Valid, lastSeen, n.PollIntervalS, n.MaxOfflineS, now),
		Config:   cfg,
		ConfApp:  app,
		Telem:    telem,
		Pending:  pending,
		Apps:     apps,
	}
}

// firstApp picks the app whose config panel is shown: the first observed app
// name, else the first observed-config app key, else "".
func firstApp(n *store.Node) string {
	if apps, _ := control.AppsFromObserved(n.ObservedState); len(apps) > 0 {
		return apps[0].Name
	}
	cfg := control.ConfigFromObserved(n.ObservedState)
	first := ""
	for k := range cfg {
		if first == "" || k < first {
			first = k
		}
	}
	return first
}
