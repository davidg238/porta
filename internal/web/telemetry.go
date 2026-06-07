// Copyright (c) 2026 Ekorau LLC

package web

import (
	"fmt"
	"net/http"

	"github.com/davidg238/porta/internal/control"
)

// --- Global telemetry page. Self-contained so telemetry can be excised in
// one bounded change: delete this file, telemetry.html, the two routes in
// web.go, the nav link in base.html, and the "Telemetry →" link in node.html.

type telemRowVM struct {
	Time, Node, Name, Value, Type string
}

type nodeOpt struct {
	ID, Name string
	Selected bool
}

type telemVM struct {
	Title  string
	Node   string    // friendly name when filtered, else ""
	NodeID string    // node id when filtered, else ""
	Nodes  []nodeOpt // filter dropdown options (ListNodes order)
	Rows   []telemRowVM
}

// fmtMetric renders a NUMERIC value for display. nil (a degraded metric)
// shows as empty; int64/float64 print directly.
func fmtMetric(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func (h *Handler) nodeNames() (map[string]string, error) {
	nodes, err := h.st.ListNodes()
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(nodes))
	for _, n := range nodes {
		m[n.ID] = n.Name
	}
	return m, nil
}

func (h *Handler) telemVM(nodeID string, now int64) (telemVM, error) {
	rows, err := h.st.RecentMetrics(nodeID, 200)
	if err != nil {
		return telemVM{}, err
	}
	names, err := h.nodeNames()
	if err != nil {
		return telemVM{}, err
	}
	vm := telemVM{Title: "Telemetry", NodeID: nodeID}
	if nodeID != "" {
		vm.Node = names[nodeID]
	}
	nodes, err := h.st.ListNodes()
	if err != nil {
		return telemVM{}, err
	}
	for _, n := range nodes {
		vm.Nodes = append(vm.Nodes, nodeOpt{ID: n.ID, Name: n.Name, Selected: n.ID == nodeID})
	}
	for _, r := range rows {
		vm.Rows = append(vm.Rows, telemRowVM{
			Time:  control.RelativeAge(r.TS, now),
			Node:  names[r.DeviceID],
			Name:  r.Name,
			Value: fmtMetric(r.Value),
			Type:  r.ValueType,
		})
	}
	return vm, nil
}

func (h *Handler) handleTelemetry(w http.ResponseWriter, r *http.Request) {
	vm, err := h.telemVM(r.URL.Query().Get("node"), h.now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.render(w, "telemetry", vm)
}

func (h *Handler) handleTelemetryPartial(w http.ResponseWriter, r *http.Request) {
	vm, err := h.telemVM(r.URL.Query().Get("node"), h.now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.render(w, "telem-rows", vm)
}
