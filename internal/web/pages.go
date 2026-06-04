// Copyright (c) 2026 Ekorau LLC

package web

import (
	"net/http"
	"strings"

	"github.com/davidg238/porta/internal/control"
	"github.com/davidg238/porta/internal/store"
)

// recentRowVM is one row in the node page's Recent commands timeline.
type recentRowVM struct {
	ID    int64
	Verb  string
	Args  string
	State string // queued | delivered | converged | expired
}

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
	Chip      string
	Sdk       string
	LastReset string
	Gauge     CheckinState
	Config   []control.ConfigRow
	ConfApp  string
	Recent   []recentRowVM
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
	case "recent":
		h.render(w, "node-recent", vm)
	case "containers":
		h.render(w, "node-containers", vm)
	case "set", "console", "poll-interval", "max-offline", "rename", "containers/install", "containers/uninstall":
		// Write actions mutate state (enqueue commands / update the node row),
		// so they must never be reachable by a GET — r.FormValue also reads the
		// query string, so a GET with ?value=… would otherwise enqueue.
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		switch sub {
		case "set":
			h.postSet(w, r, n)
		case "console":
			h.postConsole(w, r, n)
		case "poll-interval":
			h.postPollInterval(w, r, n)
		case "max-offline":
			h.postMaxOffline(w, r, n)
		case "rename":
			h.postRename(w, r, n)
		case "containers/install":
			h.postInstall(w, r, n)
		case "containers/uninstall":
			h.postUninstall(w, r, n)
		}
	default:
		http.NotFound(w, r)
	}
}

// detailVM builds the per-node view model from store + control projections.
func (h *Handler) detailVM(n *store.Node) detailVM {
	now := h.now()
	app := firstApp(n)
	cfg, _ := control.DesiredVsObserved(h.st, n.ID, app)
	recentCmds, _ := h.st.RecentCommandsForDevice(n.ID, 10)
	obsConfig := control.ConfigFromObserved(n.ObservedState)
	recent := make([]recentRowVM, 0, len(recentCmds))
	for _, c := range recentCmds {
		recent = append(recent, recentRowVM{
			ID:    c.ID,
			Verb:  c.Verb,
			Args:  c.Args,
			State: string(control.LifecycleOf(c, obsConfig, n.MaxOfflineS, now)),
		})
	}
	apps, _ := control.AppsFromObserved(n.ObservedState)
	var resetCode *int64
	if n.LastResetCode.Valid {
		c := n.LastResetCode.Int64
		resetCode = &c
	}
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
		Chip:      n.Chip,
		Sdk:       n.Sdk,
		LastReset: control.RenderReset(n.LastReset, resetCode),
		Gauge:    Checkin(n.LastSeen.Valid, lastSeen, n.PollIntervalS, n.MaxOfflineS, now),
		Config:   cfg,
		ConfApp:  app,
		Recent:   recent,
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
