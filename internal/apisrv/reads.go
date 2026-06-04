// Copyright (c) 2026 Ekorau LLC

package apisrv

import (
	"net/http"

	"github.com/davidg238/porta/internal/control"
)

// commandLogItem is one row of GET /api/nodes/{sel}/commands.
type commandLogItem struct {
	ID        int64  `json:"id"`
	Verb      string `json:"verb"`
	Args      string `json:"args"`
	IssuedAt  int64  `json:"issued_at"`
	IssuedBy  string `json:"issued_by"`
	Delivered bool   `json:"delivered"`
}

// commandLogLimit bounds how many recent commands the log read returns.
const commandLogLimit = 50

// handleNodeCommands returns the recent command log for one node.
func (h *Handler) handleNodeCommands(w http.ResponseWriter, r *http.Request) {
	id, ok := h.resolveSel(w, r.PathValue("sel"))
	if !ok {
		return
	}
	cmds, err := h.st.RecentCommandsForDevice(id, commandLogLimit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]commandLogItem, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, commandLogItem{
			ID: c.ID, Verb: c.Verb, Args: c.Args,
			IssuedAt: c.IssuedAt, IssuedBy: c.IssuedBy,
			Delivered: c.DeliveredAt.Valid,
		})
	}
	writeOK(w, map[string]any{"commands": out})
}

// configRow is one desired-vs-observed row of GET /api/nodes/{sel}/config. It
// mirrors control.ConfigRow's exported fields on the wire (so apiclient need
// not import control). Desired/Observed are arbitrary scalars (the *Present
// flags say whether the side was set); Marker is "", "(drift)", "(pending)",
// "(converged)", etc., and ReissueCount drives the self-heal warning.
type configRow struct {
	Key             string `json:"key"`
	Desired         any    `json:"desired"`
	Observed        any    `json:"observed"`
	DesiredPresent  bool   `json:"desired_present"`
	ObservedPresent bool   `json:"observed_present"`
	Marker          string `json:"marker"`
	ReissueCount    int    `json:"reissue_count"`
}

// handleNodeConfig returns the desired-vs-observed config rows for ?app=<app>
// (control.DesiredVsObserved), backing `porta device get`. app is required; the
// selector is resolved server-side (read-only, no EnsureNode).
func (h *Handler) handleNodeConfig(w http.ResponseWriter, r *http.Request) {
	id, ok := h.resolveSel(w, r.PathValue("sel"))
	if !ok {
		return
	}
	app := r.URL.Query().Get("app")
	if app == "" {
		writeErr(w, http.StatusBadRequest, "app query parameter is required")
		return
	}
	rows, err := control.DesiredVsObserved(h.st, id, app)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]configRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, configRow{
			Key: r.Key, Desired: r.Desired, Observed: r.Observed,
			DesiredPresent: r.DesiredPresent, ObservedPresent: r.ObservedPresent,
			Marker: r.Marker, ReissueCount: r.ReissueCount,
		})
	}
	writeOK(w, map[string]any{"config": out})
}
