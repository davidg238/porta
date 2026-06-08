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
	Title     string
	ID        string
	Name      string
	Kind      string
	IP        string
	EUI       string
	Mode      string
	Cadence   string
	Chip      string
	Sdk       string
	LastReset string
	Gauge     CheckinState
	Config    []control.ConfigRow
	ConfApp   string
	Recent    []recentRowVM
	Apps      []control.App
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

// handleNodeSub dispatches the polled read-only detail partials, plus the two
// surviving gateway-setting POSTs (max-offline / rename).
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
	// telemetry (optional): per-node console panels — see node_console.go
	case "prints":
		h.renderNodeConsole(w, n, "node-prints", "Prints",
			"no prints — forwarding may be off (set-forward --print on)", []string{"print"})
	case "logs":
		h.renderNodeLogs(w, n)
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
			State: string(control.LifecycleOf(c, obsConfig, n.OfflineThresholdS(), now)),
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
		Title:     n.Name,
		ID:        n.ID,
		Name:      n.Name,
		Kind:      n.Kind,
		IP:        n.SourceAddr,
		EUI:       n.ID,
		Mode:      n.Mode(),
		Cadence:   humanizeDur(n.EffectiveCadenceS()),
		Chip:      n.Chip,
		Sdk:       n.Sdk,
		LastReset: control.RenderReset(n.LastReset, resetCode),
		Gauge:     Checkin(n.LastSeen.Valid, lastSeen, n.EffectiveCadenceS(), n.OfflineThresholdS(), now),
		Config:    cfg,
		ConfApp:   app,
		Recent:    recent,
		Apps:      apps,
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
