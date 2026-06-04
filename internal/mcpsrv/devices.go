// Copyright (c) 2026 Ekorau LLC

package mcpsrv

import (
	"context"
	"fmt"

	"github.com/davidg238/porta/internal/control"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ListDevicesInput has no fields; list_devices takes no arguments.
type ListDevicesInput struct{}

// DeviceSummary is one fleet-list row.
type DeviceSummary struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	SourceAddr string `json:"source_addr"`
	LastSeenS  int64  `json:"last_seen_s"`
	Age        string `json:"age"`
	Online     bool   `json:"online"`
}

// ListDevicesOutput is the structured result of list_devices.
type ListDevicesOutput struct {
	Devices []DeviceSummary `json:"devices"`
}

func (s *Server) listDevices(_ context.Context, _ *mcp.CallToolRequest, _ ListDevicesInput) (*mcp.CallToolResult, ListDevicesOutput, error) {
	now := s.now()
	nodes, err := s.st.ListNodes()
	if err != nil {
		return errorResultf("list nodes: %v", err), ListDevicesOutput{}, nil
	}
	out := ListDevicesOutput{Devices: make([]DeviceSummary, 0, len(nodes))}
	for _, n := range nodes {
		out.Devices = append(out.Devices, DeviceSummary{
			ID:         n.ID,
			Name:       n.Name,
			Kind:       n.Kind,
			SourceAddr: n.SourceAddr,
			LastSeenS:  n.LastSeen.Int64,
			Age:        control.RelativeAge(n.LastSeen.Int64, now),
			Online:     n.Online(now),
		})
	}
	return textResult(fmt.Sprintf("%d device(s)", len(out.Devices))), out, nil
}

// DeviceInput identifies a node by MAC (12 hex) or friendly name.
type DeviceInput struct {
	Device string `json:"device" jsonschema:"node MAC (12 lowercase hex) or friendly name"`
}

// DeviceStatusOutput is the structured result of device_status.
type DeviceStatusOutput struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Kind             string `json:"kind"`
	SourceAddr       string `json:"source_addr"`
	LastSeenS        int64  `json:"last_seen_s"`
	Age              string `json:"age"`
	Online           bool   `json:"online"`
	ObservedState    string `json:"observed_state"`
	UndeliveredCount int    `json:"undelivered_count"`
}

func (s *Server) deviceStatus(_ context.Context, _ *mcp.CallToolRequest, in DeviceInput) (*mcp.CallToolResult, DeviceStatusOutput, error) {
	n, errRes := s.resolveNode(in.Device)
	if errRes != nil {
		return errRes, DeviceStatusOutput{}, nil
	}
	now := s.now()
	undelivered, err := s.st.UndeliveredCommands(n.ID)
	if err != nil {
		return errorResultf("undelivered for %q: %v", n.ID, err), DeviceStatusOutput{}, nil
	}
	out := DeviceStatusOutput{
		ID:               n.ID,
		Name:             n.Name,
		Kind:             n.Kind,
		SourceAddr:       n.SourceAddr,
		LastSeenS:        n.LastSeen.Int64,
		Age:              control.RelativeAge(n.LastSeen.Int64, now),
		Online:           n.Online(now),
		ObservedState:    n.ObservedState,
		UndeliveredCount: len(undelivered),
	}
	return textResult(fmt.Sprintf("%s: %d undelivered, online=%v", n.ID, out.UndeliveredCount, out.Online)), out, nil
}
