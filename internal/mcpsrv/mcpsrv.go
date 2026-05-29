// Package mcpsrv exposes porta's read surface as read-only MCP tools over
// Streamable HTTP. It is a thin adapter over internal/control + internal/store;
// it owns no query logic and performs no writes.
package mcpsrv

import (
	"fmt"
	"net/http"
	"time"

	"github.com/davidg238/porta/internal/control"
	"github.com/davidg238/porta/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Server adapts porta's read surface to MCP tools.
type Server struct {
	st  *store.Store
	now func() int64
	mcp *mcp.Server
}

// New builds an MCP server with porta's read tools registered.
func New(st *store.Store) *Server {
	s := &Server{
		st:  st,
		now: func() int64 { return time.Now().Unix() },
	}
	s.mcp = mcp.NewServer(&mcp.Implementation{Name: "porta", Version: "0.1.0"}, nil)
	s.registerTools()
	return s
}

// Register mounts the Streamable HTTP MCP endpoint at /mcp on mux. It uses
// Handle (not a catch-all) so it never shadows sibling routes.
func (s *Server) Register(mux *http.ServeMux) {
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s.mcp }, nil)
	mux.Handle("/mcp", h)
}

// registerTools wires the read tools. It grows one group per implementation task.
func (s *Server) registerTools() {
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "list_devices",
		Description: "List all known nodes with online status and last-seen age.",
	}, s.listDevices)
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "device_status",
		Description: "Show one node's status: kind, source addr, last seen, observed state, undelivered command count.",
	}, s.deviceStatus)
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "device_get_config",
		Description: "Show desired-vs-observed config rows for one app, or all observed apps when app is omitted.",
	}, s.deviceGetConfig)
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "container_list",
		Description: "List a node's installed containers (name, crc, runlevel) from observed state.",
	}, s.containerList)
}

// textResult returns a non-error result carrying only a human-readable summary.
// When a handler also returns a typed Out, the SDK fills StructuredContent and
// preserves this Content.
func textResult(summary string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: summary}}}
}

// errorResultf returns an IsError result; the LLM sees the message as the tool
// output rather than a transport error.
func errorResultf(format string, a ...any) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(format, a...)}},
		IsError: true,
	}
}

// clampLimit defaults absent/non-positive limits to 100 and caps at 1000.
func clampLimit(n int) int {
	switch {
	case n <= 0:
		return 100
	case n > 1000:
		return 1000
	default:
		return n
	}
}

// resolveNode resolves a device arg to its node, fetching the row. On any
// failure (unresolvable arg, store error, absent node) it returns a nil node
// and an IsError result for the caller to return directly.
func (s *Server) resolveNode(device string) (*store.Node, *mcp.CallToolResult) {
	id, err := control.ResolveNodeID(s.st, device)
	if err != nil {
		return nil, errorResultf("resolve device %q: %v", device, err)
	}
	n, err := s.st.GetNode(id)
	if err != nil {
		return nil, errorResultf("get node %q: %v", id, err)
	}
	if n == nil {
		return nil, errorResultf("no node %q", id)
	}
	return n, nil
}

