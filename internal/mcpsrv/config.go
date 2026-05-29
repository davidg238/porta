package mcpsrv

import (
	"context"
	"fmt"

	"github.com/davidg238/porta/internal/control"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ContainerInfo is one installed-container row from observed state.
type ContainerInfo struct {
	Name     string `json:"name"`
	CRC      int64  `json:"crc"`
	Runlevel int64  `json:"runlevel"`
}

// ContainerListOutput is the structured result of container_list.
type ContainerListOutput struct {
	Containers []ContainerInfo `json:"containers"`
}

// containerList returns all containers installed on a node from observed state.
func (s *Server) containerList(_ context.Context, _ *mcp.CallToolRequest, in DeviceInput) (*mcp.CallToolResult, ContainerListOutput, error) {
	id, errRes := s.resolve(in.Device)
	if errRes != nil {
		return errRes, ContainerListOutput{}, nil
	}
	n, err := s.st.GetNode(id)
	if err != nil {
		return errorResultf("get node %q: %v", id, err), ContainerListOutput{}, nil
	}
	if n == nil {
		return errorResultf("no node %q", id), ContainerListOutput{}, nil
	}
	apps, err := control.AppsFromObserved(n.ObservedState)
	if err != nil {
		return errorResultf("decode observed apps for %q: %v", id, err), ContainerListOutput{}, nil
	}
	out := ContainerListOutput{Containers: make([]ContainerInfo, 0, len(apps))}
	for _, a := range apps {
		out.Containers = append(out.Containers, ContainerInfo{Name: a.Name, CRC: a.CRC, Runlevel: a.Runlevel})
	}
	return textResult(fmt.Sprintf("%s: %d container(s)", id, len(out.Containers))), out, nil
}

// DeviceConfigInput selects a node and optionally one app.
type DeviceConfigInput struct {
	Device string `json:"device" jsonschema:"node MAC (12 lowercase hex) or friendly name"`
	App    string `json:"app,omitempty" jsonschema:"app name; omit for all observed apps"`
}

// ConfigRowOut is one desired-vs-observed config key.
type ConfigRowOut struct {
	App          string `json:"app"`
	Key          string `json:"key"`
	Desired      any    `json:"desired"`
	Observed     any    `json:"observed"`
	Marker       string `json:"marker"`
	ReissueCount int    `json:"reissue_count"`
}

// DeviceConfigOutput is the structured result of device_get_config.
type DeviceConfigOutput struct {
	Rows []ConfigRowOut `json:"rows"`
}

// deviceGetConfig returns desired-vs-observed config rows for one app or all observed apps.
func (s *Server) deviceGetConfig(_ context.Context, _ *mcp.CallToolRequest, in DeviceConfigInput) (*mcp.CallToolResult, DeviceConfigOutput, error) {
	id, errRes := s.resolve(in.Device)
	if errRes != nil {
		return errRes, DeviceConfigOutput{}, nil
	}
	n, err := s.st.GetNode(id)
	if err != nil {
		return errorResultf("get node %q: %v", id, err), DeviceConfigOutput{}, nil
	}
	if n == nil {
		return errorResultf("no node %q", id), DeviceConfigOutput{}, nil
	}

	var apps []string
	if in.App != "" {
		apps = []string{in.App}
	} else {
		// Enumerate observed installed apps (same source the web detail config
		// panel uses); see internal/web/pages.go.
		installed, err := control.AppsFromObserved(n.ObservedState)
		if err != nil {
			return errorResultf("decode observed apps for %q: %v", id, err), DeviceConfigOutput{}, nil
		}
		apps = make([]string, 0, len(installed))
		for _, a := range installed {
			apps = append(apps, a.Name)
		}
	}

	out := DeviceConfigOutput{Rows: []ConfigRowOut{}}
	for _, app := range apps {
		rows, err := control.DesiredVsObserved(s.st, id, app)
		if err != nil {
			return errorResultf("config for %q app %q: %v", id, app, err), DeviceConfigOutput{}, nil
		}
		for _, r := range rows {
			out.Rows = append(out.Rows, ConfigRowOut{
				App:          app,
				Key:          r.Key,
				Desired:      r.Desired,
				Observed:     r.Observed,
				Marker:       r.Marker,
				ReissueCount: r.ReissueCount,
			})
		}
	}
	return textResult(fmt.Sprintf("%s: %d config row(s)", id, len(out.Rows))), out, nil
}
