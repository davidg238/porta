// Copyright (c) 2026 Ekorau LLC

package apisrv

import (
	"encoding/json"
	"net/http"

	"github.com/davidg238/porta/internal/control"
)

// nodeListItem is one row of GET /api/nodes.
type nodeListItem struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	IP       string `json:"ip"`
	LastSeen int64  `json:"last_seen"`
	Online   bool   `json:"online"`
	Chip     string `json:"chip"`
	Sdk      string `json:"sdk"`
}

// handleListNodes returns the fleet list, including self-reported identity.
func (h *Handler) handleListNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := h.st.ListNodes()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	now := h.now()
	out := make([]nodeListItem, 0, len(nodes))
	for i := range nodes {
		n := &nodes[i]
		out = append(out, nodeListItem{
			ID: n.ID, Name: n.Name, Kind: n.Kind, IP: n.SourceAddr,
			LastSeen: n.LastSeen.Int64, Online: n.Online(now),
			Chip: n.Chip, Sdk: n.Sdk,
		})
	}
	writeOK(w, map[string]any{"nodes": out})
}

// nodePatch carries optional node-management settings; pointer fields let the
// handler apply only what was sent.
type nodePatch struct {
	Name        *string `json:"name"`
	MaxOfflineS *int64  `json:"max_offline_s"`
}

// nodeDetail is GET /api/nodes/{sel}: identity + observed apps + config
// (desired-vs-observed for the first app, mirroring the web detail page) +
// timings. The SDK guard in `porta run` reads chip/sdk from here.
type nodeDetail struct {
	ID            string              `json:"id"`
	Name          string              `json:"name"`
	Kind          string              `json:"kind"`
	IP            string              `json:"ip"`
	Online        bool                `json:"online"`
	Chip          string              `json:"chip"`
	Sdk           string              `json:"sdk"`
	Reset         string              `json:"reset"`
	ResetCode     *int64              `json:"reset_code"`
	PollIntervalS int64               `json:"poll_interval_s"`
	MaxOfflineS   int64               `json:"max_offline_s"`
	LastSeen      int64               `json:"last_seen"`
	LastReportAt  int64               `json:"last_report_at"`
	Apps          []control.App       `json:"apps"`
	ConfigApp     string              `json:"config_app"`
	Config        []control.ConfigRow `json:"config"`
	// ObservedRaw is the node's cached observed_state JSON blob verbatim, and
	// Undelivered is the count of queued-but-not-delivered commands. Both back
	// `porta device show`'s store-equivalent output over the API.
	ObservedRaw string `json:"observed_raw"`
	Undelivered int    `json:"undelivered"`
	// NodeConfig is the node's last echoed effective-config block, decoded from
	// the cached blob (nil until the node first echoes). nodus-cli polls this to
	// confirm a config command converged.
	NodeConfig map[string]any `json:"node_config"`
}

// decodeNodeConfig parses a cached node_config blob into a map, or nil when the
// node has never echoed one (or the blob is unparseable).
func decodeNodeConfig(raw string) map[string]any {
	if raw == "" {
		return nil
	}
	var m map[string]any
	if json.Unmarshal([]byte(raw), &m) != nil {
		return nil
	}
	return m
}

// handleNodeDetail returns one node's full detail.
func (h *Handler) handleNodeDetail(w http.ResponseWriter, r *http.Request) {
	id, ok := h.resolveSel(w, r.PathValue("sel"))
	if !ok {
		return
	}
	n, err := h.st.GetNode(id)
	if err != nil || n == nil {
		writeErr(w, http.StatusNotFound, "node not found")
		return
	}
	apps, _ := control.AppsFromObserved(n.ObservedState)
	confApp := firstAppName(apps)
	var cfg []control.ConfigRow
	if confApp != "" {
		cfg, _ = control.DesiredVsObserved(h.st, id, confApp)
	}
	un, _ := h.st.UndeliveredCommands(id)
	var resetCode *int64
	if n.LastResetCode.Valid {
		c := n.LastResetCode.Int64
		resetCode = &c
	}
	writeOK(w, nodeDetail{
		ID: n.ID, Name: n.Name, Kind: n.Kind, IP: n.SourceAddr,
		Online: n.Online(h.now()), Chip: n.Chip, Sdk: n.Sdk,
		Reset: n.LastReset, ResetCode: resetCode,
		PollIntervalS: n.PollIntervalS, MaxOfflineS: n.MaxOfflineS,
		LastSeen: n.LastSeen.Int64, LastReportAt: n.LastReportAt.Int64,
		Apps: apps, ConfigApp: confApp, Config: cfg,
		ObservedRaw: n.ObservedState, Undelivered: len(un),
		NodeConfig: decodeNodeConfig(n.NodeConfig),
	})
}

// firstAppName returns the lexically-first observed app name (the app whose
// config the detail view surfaces), or "" if none.
func firstAppName(apps []control.App) string {
	first := ""
	for _, a := range apps {
		if first == "" || a.Name < first {
			first = a.Name
		}
	}
	return first
}

// handlePatchNode applies rename / max-offline node settings (gateway-side,
// not device commands).
func (h *Handler) handlePatchNode(w http.ResponseWriter, r *http.Request) {
	id, ok := h.resolveSel(w, r.PathValue("sel"))
	if !ok {
		return
	}
	if err := h.st.EnsureNode(id, h.now()); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var p nodePatch
	if err := json.Unmarshal(readBody(r), &p); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if p.Name != nil {
		if err := control.Rename(h.st, id, *p.Name); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if p.MaxOfflineS != nil {
		if err := control.SetMaxOffline(h.st, id, *p.MaxOfflineS); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	writeOK(w, map[string]any{"node_id": id})
}
