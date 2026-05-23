// Package mcpserver exposes jast-gw device operations as MCP tools.
package mcpserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/davidg238/jast-gw/debug"
	"github.com/davidg238/jast-gw/debugui"
	"github.com/davidg238/jast-gw/helpers"
	"github.com/davidg238/jast-gw/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mustSchema returns a json.RawMessage from a raw JSON string.
func mustSchema(s string) json.RawMessage { return json.RawMessage(s) }

// textResult returns a successful CallToolResult with text content.
func textResult(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

// errorResult returns an error CallToolResult.
func errorResult(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}, IsError: true}
}

// args is a convenience map for parsed tool arguments.
type args map[string]any

func parseArgs(req *mcp.CallToolRequest) args {
	var a args
	if req.Params.Arguments != nil {
		_ = json.Unmarshal(req.Params.Arguments, &a)
	}
	if a == nil {
		a = args{}
	}
	return a
}

func (a args) str(key string) string {
	v, ok := a[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

func (a args) boolOrDefault(key string, def bool) bool {
	v, ok := a[key]
	if !ok {
		return def
	}
	b, ok := v.(bool)
	if !ok {
		return def
	}
	return b
}

func (a args) intOrDefault(key string, def int) int {
	v, ok := a[key]
	if !ok {
		return def
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return def
		}
		return int(i)
	}
	return def
}

// compileClient bounds compile-service calls so a hung service can't block MCP
// requests indefinitely.
var compileClient = &http.Client{Timeout: 30 * time.Second}

type compileReq struct {
	Source  string `json:"source"`
	Symbols bool   `json:"symbols"`
	Name    string `json:"name"`
}

// compileResult holds the output of a Smalltalk compilation.
type compileResult struct {
	BEC   []byte // compiled bytecode
	STMap []byte // source map JSON (may be nil if compilation didn't produce one)
}

// compileST compiles Smalltalk source to .bec (+ source map) by POSTing to the
// compile service. Decouples the gateway from the Python transpiler — see
// docs/superpowers/plans/2026-05-20-gateway-compile-service.md.
func compileST(compileURL, source, name string, symbols bool) (*compileResult, error) {
	reqBody, err := json.Marshal(compileReq{Source: source, Symbols: symbols, Name: name})
	if err != nil {
		return nil, fmt.Errorf("marshal compile request: %w", err)
	}
	resp, err := compileClient.Post(compileURL+"/compile", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("compile service unreachable at %s: %w", compileURL, err)
	}
	defer resp.Body.Close()

	var out struct {
		BEC   string `json:"bec"`
		STMap string `json:"stmap"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode compile response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		if out.Error != "" {
			return nil, fmt.Errorf("compile failed: %s", out.Error)
		}
		return nil, fmt.Errorf("compile service returned %d", resp.StatusCode)
	}
	bec, err := base64.StdEncoding.DecodeString(out.BEC)
	if err != nil {
		return nil, fmt.Errorf("decode bec: %w", err)
	}
	var stmap []byte
	if out.STMap != "" {
		stmap = []byte(out.STMap)
	}
	return &compileResult{BEC: bec, STMap: stmap}, nil
}

// debugHub may be nil (no browser viewers connected).
var debugHub *debugui.Hub

// SetDebugHub sets the SSE hub for browser debug viewer broadcasts.
func SetDebugHub(hub *debugui.Hub) {
	debugHub = hub
}

// New creates an MCP server with tools backed by the given store and debug manager.
func New(st *store.Store, dbg *debug.Manager, compileURL string) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "jast-gw", Version: "1.0.0"}, nil)

	// 1. list_devices
	srv.AddTool(&mcp.Tool{
		Name:        "list_devices",
		Description: "List all known Thread devices",
		InputSchema: mustSchema(`{"type":"object"}`),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		devs, err := st.ListDevices()
		if err != nil {
			return errorResult(err), nil
		}
		if len(devs) == 0 {
			return textResult("No devices registered."), nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%-20s %-12s %-8s %-6s %s\n", "EUI64", "NAME", "ROLE", "RLOC16", "LAST SEEN")
		for _, d := range devs {
			name := d.Name
			if name == "" {
				name = "-"
			}
			fmt.Fprintf(&b, "%-20s %-12s %-8s 0x%04X %s\n",
				d.EUI64, name, d.Role, d.RLOC16, d.LastSeen.Format(time.RFC3339))
		}
		return textResult(b.String()), nil
	})

	// 2. device_status
	srv.AddTool(&mcp.Tool{
		Name:        "device_status",
		Description: "Get status of a Thread device",
		InputSchema: mustSchema(`{"type":"object","properties":{"device":{"type":"string","description":"Device EUI-64 or name"}},"required":["device"]}`),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		a := parseArgs(req)
		device := a.str("device")
		if device == "" {
			return errorResult(fmt.Errorf("device is required")), nil
		}
		result, err := helpers.WaitForVerb(st, device, "status")
		if err != nil {
			return errorResult(err), nil
		}
		return textResult(result), nil
	})

	// 3. queue_command
	srv.AddTool(&mcp.Tool{
		Name:        "queue_command",
		Description: "Queue a command for a device",
		InputSchema: mustSchema(`{"type":"object","properties":{"device":{"type":"string","description":"Device EUI-64 or name"},"verb":{"type":"string","description":"Command verb"},"payload":{"type":"string","description":"Optional payload"}},"required":["device","verb"]}`),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		a := parseArgs(req)
		device := a.str("device")
		verb := a.str("verb")
		if device == "" || verb == "" {
			return errorResult(fmt.Errorf("device and verb are required")), nil
		}
		eui, err := st.ResolveDevice(device)
		if err != nil {
			return errorResult(err), nil
		}
		var payload []byte
		if p := a.str("payload"); p != "" {
			payload = []byte(p)
		}
		if err := st.QueueCommand(eui, verb, payload); err != nil {
			return errorResult(err), nil
		}
		return textResult(fmt.Sprintf("queued %s for %s", verb, eui)), nil
	})

	// 4. run_st
	srv.AddTool(&mcp.Tool{
		Name:        "run_st",
		Description: "Compile and run a Smalltalk script on a device, wait for result",
		InputSchema: mustSchema(`{"type":"object","properties":{"device":{"type":"string","description":"Device EUI-64 or name"},"source":{"type":"string","description":"Smalltalk source code"},"symbols":{"type":"boolean","description":"Include symbol table in bytecode"}},"required":["device","source"]}`),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		a := parseArgs(req)
		device := a.str("device")
		source := a.str("source")
		if device == "" || source == "" {
			return errorResult(fmt.Errorf("device and source are required")), nil
		}
		eui, err := st.ResolveDevice(device)
		if err != nil {
			return errorResult(err), nil
		}
		symbols := a.boolOrDefault("symbols", false)
		cr, err := compileST(compileURL, source, "run.st", symbols)
		if err != nil {
			return errorResult(err), nil
		}
		before := time.Now()
		if err := st.QueueCommand(eui, "run", cr.BEC); err != nil {
			return errorResult(err), nil
		}
		for i := 0; i < 10; i++ {
			time.Sleep(500 * time.Millisecond)
			rows, _ := st.QueryData(eui, before, time.Now())
			if len(rows) > 0 {
				return textResult(string(rows[len(rows)-1].Payload)), nil
			}
		}
		return textResult("queued (timeout waiting for result)"), nil
	})

	// 5. compile_and_push
	srv.AddTool(&mcp.Tool{
		Name:        "compile_and_push",
		Description: "Compile Smalltalk source and push as a named module to a device",
		InputSchema: mustSchema(`{"type":"object","properties":{"device":{"type":"string","description":"Device EUI-64 or name"},"name":{"type":"string","description":"Module name"},"source":{"type":"string","description":"Smalltalk source code"},"symbols":{"type":"boolean","description":"Include symbol table in bytecode"}},"required":["device","name","source"]}`),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		a := parseArgs(req)
		device := a.str("device")
		name := a.str("name")
		source := a.str("source")
		if device == "" || name == "" || source == "" {
			return errorResult(fmt.Errorf("device, name, and source are required")), nil
		}
		eui, err := st.ResolveDevice(device)
		if err != nil {
			return errorResult(err), nil
		}
		symbols := a.boolOrDefault("symbols", false)
		cr, err := compileST(compileURL, source, name, symbols)
		if err != nil {
			return errorResult(err), nil
		}
		verb := "load:" + name
		if err := st.QueueCommand(eui, verb, cr.BEC); err != nil {
			return errorResult(err), nil
		}
		// Store source map for debug line translation
		if cr.STMap != nil && dbg != nil {
			_ = dbg.StoreSourceMap(eui, name, cr.STMap, source)
		}
		// Broadcast source panel to debug viewer
		if debugHub != nil {
			bps, _ := st.ListDebugBreakpoints(eui)
			_, cl, bpLines := debugui.BuildSourceData(source, name, 0, bps)
			debugHub.Broadcast(eui, debugui.Event{
				Name: "debug-source",
				HTML: debugui.RenderSource(name, source, cl, bpLines),
			})
		}
		return textResult(fmt.Sprintf("compiled %d bytes, queued %s for %s", len(cr.BEC), verb, eui)), nil
	})

	// 6. get_console
	srv.AddTool(&mcp.Tool{
		Name:        "get_console",
		Description: "Get recent console output from a device",
		InputSchema: mustSchema(`{"type":"object","properties":{"device":{"type":"string","description":"Device EUI-64 or name"},"lines":{"type":"integer","description":"Number of lines (default 10)"}},"required":["device"]}`),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		a := parseArgs(req)
		device := a.str("device")
		if device == "" {
			return errorResult(fmt.Errorf("device is required")), nil
		}
		eui, err := st.ResolveDevice(device)
		if err != nil {
			return errorResult(err), nil
		}
		lines := a.intOrDefault("lines", 10)
		rows, err := st.QueryData(eui, time.Time{}, time.Now())
		if err != nil {
			return errorResult(err), nil
		}
		// Take last N entries.
		if len(rows) > lines {
			rows = rows[len(rows)-lines:]
		}
		if len(rows) == 0 {
			return textResult("(no console output)"), nil
		}
		var b strings.Builder
		for _, r := range rows {
			fmt.Fprintf(&b, "[%s] %s\n", r.Timestamp.Format(time.RFC3339), string(r.Payload))
		}
		return textResult(b.String()), nil
	})

	// 7. network_status
	srv.AddTool(&mcp.Tool{
		Name:        "network_status",
		Description: "Summary of the Thread network: device count and last-seen times",
		InputSchema: mustSchema(`{"type":"object"}`),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		devs, err := st.ListDevices()
		if err != nil {
			return errorResult(err), nil
		}
		if len(devs) == 0 {
			return textResult("No devices on the network."), nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Devices: %d\n", len(devs))
		for _, d := range devs {
			name := d.Name
			if name == "" {
				name = d.EUI64
			}
			fmt.Fprintf(&b, "  %s  last seen %s\n", name, d.LastSeen.Format(time.RFC3339))
		}
		return textResult(b.String()), nil
	})

	// 8. query_data
	srv.AddTool(&mcp.Tool{
		Name:        "query_data",
		Description: "Query logged data from a device with optional time range",
		InputSchema: mustSchema(`{"type":"object","properties":{"device":{"type":"string","description":"Device EUI-64 or name"},"since":{"type":"string","description":"Start time (RFC3339)"},"until":{"type":"string","description":"End time (RFC3339)"}},"required":["device"]}`),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		a := parseArgs(req)
		device := a.str("device")
		if device == "" {
			return errorResult(fmt.Errorf("device is required")), nil
		}
		eui, err := st.ResolveDevice(device)
		if err != nil {
			return errorResult(err), nil
		}
		since := time.Time{}
		until := time.Now()
		if s := a.str("since"); s != "" {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				since = t
			}
		}
		if s := a.str("until"); s != "" {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				until = t
			}
		}
		rows, err := st.QueryData(eui, since, until)
		if err != nil {
			return errorResult(err), nil
		}
		if len(rows) == 0 {
			return textResult("(no data)"), nil
		}
		var b strings.Builder
		for _, r := range rows {
			fmt.Fprintf(&b, "[%s] %s\n", r.Timestamp.Format(time.RFC3339), string(r.Payload))
		}
		return textResult(b.String()), nil
	})

	// 9. name_device
	srv.AddTool(&mcp.Tool{
		Name:        "name_device",
		Description: "Assign a human-readable name to a device",
		InputSchema: mustSchema(`{"type":"object","properties":{"eui64":{"type":"string","description":"Device EUI-64"},"name":{"type":"string","description":"Human-readable name"}},"required":["eui64","name"]}`),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		a := parseArgs(req)
		eui64 := a.str("eui64")
		name := a.str("name")
		if eui64 == "" || name == "" {
			return errorResult(fmt.Errorf("eui64 and name are required")), nil
		}
		if err := st.SetDeviceName(eui64, name); err != nil {
			return errorResult(err), nil
		}
		return textResult(fmt.Sprintf("named %s as %q", eui64, name)), nil
	})

	// --- Debug tools ---

	// 10. debug_set_breakpoint
	srv.AddTool(&mcp.Tool{
		Name:        "debug_set_breakpoint",
		Description: "Set a breakpoint at a Smalltalk source line. Requires a prior compile_and_push for the module.",
		InputSchema: mustSchema(`{"type":"object","properties":{"device":{"type":"string","description":"Device EUI-64 or name"},"module":{"type":"string","description":"Module name"},"line":{"type":"integer","description":"ST source line number"}},"required":["device","module","line"]}`),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		a := parseArgs(req)
		device := a.str("device")
		module := a.str("module")
		line := a.intOrDefault("line", 0)
		if device == "" || module == "" || line == 0 {
			return errorResult(fmt.Errorf("device, module, and line are required")), nil
		}
		eui, err := st.ResolveDevice(device)
		if err != nil {
			return errorResult(err), nil
		}
		if err := dbg.SetBreakpoint(eui, module, line); err != nil {
			return errorResult(err), nil
		}
		if err := dbg.QueueBreakpointToDevice(eui, module, line); err != nil {
			return errorResult(err), nil
		}
		broadcastDebugState(st, dbg, eui)
		return textResult(fmt.Sprintf("breakpoint set at %s:%d", module, line)), nil
	})

	// 11. debug_clear_breakpoint
	srv.AddTool(&mcp.Tool{
		Name:        "debug_clear_breakpoint",
		Description: "Clear a breakpoint at a Smalltalk source line",
		InputSchema: mustSchema(`{"type":"object","properties":{"device":{"type":"string","description":"Device EUI-64 or name"},"module":{"type":"string","description":"Module name"},"line":{"type":"integer","description":"ST source line number"}},"required":["device","module","line"]}`),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		a := parseArgs(req)
		device := a.str("device")
		module := a.str("module")
		line := a.intOrDefault("line", 0)
		if device == "" || module == "" || line == 0 {
			return errorResult(fmt.Errorf("device, module, and line are required")), nil
		}
		eui, err := st.ResolveDevice(device)
		if err != nil {
			return errorResult(err), nil
		}
		bps, err := st.ListDebugBreakpoints(eui)
		if err != nil {
			return errorResult(err), nil
		}
		for _, bp := range bps {
			if bp.Module == module && bp.STLine == line {
				cmd := fmt.Sprintf("dbg:clear %d %d", bp.PCStart, bp.PCEnd)
				_ = dbg.QueueDebugCommand(eui, cmd)
				break
			}
		}
		if err := dbg.ClearBreakpoint(eui, module, line); err != nil {
			return errorResult(err), nil
		}
		broadcastDebugState(st, dbg, eui)
		return textResult(fmt.Sprintf("breakpoint cleared at %s:%d", module, line)), nil
	})

	// 12. debug_list_breakpoints
	srv.AddTool(&mcp.Tool{
		Name:        "debug_list_breakpoints",
		Description: "List all active breakpoints for a device",
		InputSchema: mustSchema(`{"type":"object","properties":{"device":{"type":"string","description":"Device EUI-64 or name"}},"required":["device"]}`),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		a := parseArgs(req)
		device := a.str("device")
		if device == "" {
			return errorResult(fmt.Errorf("device is required")), nil
		}
		eui, err := st.ResolveDevice(device)
		if err != nil {
			return errorResult(err), nil
		}
		bps, err := st.ListDebugBreakpoints(eui)
		if err != nil {
			return errorResult(err), nil
		}
		if len(bps) == 0 {
			return textResult("No breakpoints set."), nil
		}
		var b strings.Builder
		for _, bp := range bps {
			fmt.Fprintf(&b, "%s:%d (PC %d-%d)\n", bp.Module, bp.STLine, bp.PCStart, bp.PCEnd)
		}
		return textResult(b.String()), nil
	})

	// 13. debug_continue
	srv.AddTool(&mcp.Tool{
		Name:        "debug_continue",
		Description: "Resume execution of a paused device",
		InputSchema: mustSchema(`{"type":"object","properties":{"device":{"type":"string","description":"Device EUI-64 or name"}},"required":["device"]}`),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		a := parseArgs(req)
		device := a.str("device")
		if device == "" {
			return errorResult(fmt.Errorf("device is required")), nil
		}
		eui, err := st.ResolveDevice(device)
		if err != nil {
			return errorResult(err), nil
		}
		broadcastDebugState(st, dbg, eui)
		return textResult("continue queued"), dbg.QueueDebugCommand(eui, "dbg:continue")
	})

	// 14. debug_step
	srv.AddTool(&mcp.Tool{
		Name:        "debug_step",
		Description: "Step execution: 'line' (next line), 'over' (step over calls), 'out' (step out of current method)",
		InputSchema: mustSchema(`{"type":"object","properties":{"device":{"type":"string","description":"Device EUI-64 or name"},"mode":{"type":"string","description":"Step mode: line, over, or out","enum":["line","over","out"]}},"required":["device","mode"]}`),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		a := parseArgs(req)
		device := a.str("device")
		mode := a.str("mode")
		if device == "" || mode == "" {
			return errorResult(fmt.Errorf("device and mode are required")), nil
		}
		eui, err := st.ResolveDevice(device)
		if err != nil {
			return errorResult(err), nil
		}
		var cmd string
		switch mode {
		case "line":
			cmd = "dbg:step"
		case "over":
			cmd = "dbg:over"
		case "out":
			cmd = "dbg:out"
		default:
			return errorResult(fmt.Errorf("invalid step mode: %s", mode)), nil
		}
		if err := dbg.QueueDebugCommand(eui, cmd); err != nil {
			return errorResult(err), nil
		}
		// Wait for device to report new paused state
		for i := 0; i < 50; i++ {
			time.Sleep(100 * time.Millisecond)
			ds, err := st.GetDebugState(eui)
			if err == nil && ds.Status == "paused" {
				line, _ := dbg.PCToSTLine(eui, ds.CurrentModule, ds.CurrentFunction, ds.CurrentPC)
				broadcastDebugState(st, dbg, eui)
				return textResult(fmt.Sprintf("paused at %s:%d (function %s, PC %d)",
					ds.CurrentModule, line, ds.CurrentFunction, ds.CurrentPC)), nil
			}
		}
		return textResult("step command sent, waiting for device response"), nil
	})

	// 15. debug_inspect
	srv.AddTool(&mcp.Tool{
		Name:        "debug_inspect",
		Description: "Inspect call stack and local variables of a paused device",
		InputSchema: mustSchema(`{"type":"object","properties":{"device":{"type":"string","description":"Device EUI-64 or name"}},"required":["device"]}`),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		a := parseArgs(req)
		device := a.str("device")
		if device == "" {
			return errorResult(fmt.Errorf("device is required")), nil
		}
		eui, err := st.ResolveDevice(device)
		if err != nil {
			return errorResult(err), nil
		}
		if err := dbg.QueueDebugCommand(eui, "dbg:inspect"); err != nil {
			return errorResult(err), nil
		}
		before := time.Now()
		for i := 0; i < 50; i++ {
			time.Sleep(100 * time.Millisecond)
			rows, err := st.QueryData(eui, before, time.Now())
			if err != nil {
				continue
			}
			for _, r := range rows {
				payload := string(r.Payload)
				if strings.HasPrefix(payload, "{\"stack\":") {
					broadcastInspectResult(eui, payload)
					return textResult(payload), nil
				}
			}
		}
		return errorResult(fmt.Errorf("timeout waiting for inspect response")), nil
	})

	// 16. debug_source
	srv.AddTool(&mcp.Tool{
		Name:        "debug_source",
		Description: "Show Smalltalk source for a module with breakpoint and current-line markers",
		InputSchema: mustSchema(`{"type":"object","properties":{"device":{"type":"string","description":"Device EUI-64 or name"},"module":{"type":"string","description":"Module name"}},"required":["device","module"]}`),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		a := parseArgs(req)
		device := a.str("device")
		module := a.str("module")
		if device == "" || module == "" {
			return errorResult(fmt.Errorf("device and module are required")), nil
		}
		eui, err := st.ResolveDevice(device)
		if err != nil {
			return errorResult(err), nil
		}
		source := dbg.GetSource(eui, module)
		if source == "" {
			return errorResult(fmt.Errorf("no source stored for %s/%s", device, module)), nil
		}

		bps, _ := st.ListDebugBreakpoints(eui)
		ds, _ := st.GetDebugState(eui)

		bpLines := make(map[int]bool)
		for _, bp := range bps {
			if bp.Module == module {
				bpLines[bp.STLine] = true
			}
		}

		var currentLine int
		if ds != nil && ds.Status == "paused" && ds.CurrentModule == module {
			currentLine = ds.CurrentSTLine
		}

		lines := strings.Split(source, "\n")
		var sb strings.Builder
		for i, ln := range lines {
			lineNum := i + 1
			marker := "  "
			if lineNum == currentLine {
				marker = "→ "
			} else if bpLines[lineNum] {
				marker = "● "
			}
			fmt.Fprintf(&sb, "%s%3d: %s\n", marker, lineNum, ln)
		}
		return textResult(sb.String()), nil
	})

	// 17. debug_pause
	srv.AddTool(&mcp.Tool{
		Name:        "debug_pause",
		Description: "Pause a running device at the next Smalltalk line",
		InputSchema: mustSchema(`{"type":"object","properties":{"device":{"type":"string","description":"Device EUI-64 or name"}},"required":["device"]}`),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		a := parseArgs(req)
		device := a.str("device")
		if device == "" {
			return errorResult(fmt.Errorf("device is required")), nil
		}
		eui, err := st.ResolveDevice(device)
		if err != nil {
			return errorResult(err), nil
		}
		if err := dbg.QueueDebugCommand(eui, "dbg:pause"); err != nil {
			return errorResult(err), nil
		}
		broadcastDebugState(st, dbg, eui)
		return textResult("pause requested"), nil
	})

	return srv
}

// broadcastDebugState reads current debug state and broadcasts status + source panels.
func broadcastDebugState(st *store.Store, dbg *debug.Manager, eui string) {
	if debugHub == nil {
		return
	}
	ds, err := st.GetDebugState(eui)
	if err != nil || ds == nil {
		return
	}

	// Status
	deviceName := eui
	devs, _ := st.ListDevices()
	for _, d := range devs {
		if d.EUI64 == eui && d.Name != "" {
			deviceName = d.Name
			break
		}
	}
	location := debugui.FormatLocation(ds.CurrentFunction, ds.CurrentModule, ds.CurrentSTLine)
	debugHub.Broadcast(eui, debugui.Event{
		Name: "debug-status",
		HTML: debugui.RenderStatus(debugui.StatusData{DeviceName: deviceName, Status: ds.Status, Location: location}),
	})

	// Source
	if ds.CurrentModule != "" {
		source := dbg.GetSource(eui, ds.CurrentModule)
		bps, _ := st.ListDebugBreakpoints(eui)
		_, cl, bpLines := debugui.BuildSourceData(source, ds.CurrentModule, ds.CurrentSTLine, bps)
		debugHub.Broadcast(eui, debugui.Event{
			Name: "debug-source",
			HTML: debugui.RenderSource(ds.CurrentModule, source, cl, bpLines),
		})
	}
}

// broadcastInspectResult parses inspect JSON and broadcasts stack + locals panels.
func broadcastInspectResult(eui, payload string) {
	if debugHub == nil {
		return
	}
	var result struct {
		Stack []struct {
			Function string `json:"function"`
			Module   string `json:"module"`
			Line     int    `json:"line"`
		} `json:"stack"`
		Locals map[string]any `json:"locals"`
	}
	if err := json.Unmarshal([]byte(payload), &result); err != nil {
		return
	}

	var frames []debugui.StackFrame
	for _, f := range result.Stack {
		frames = append(frames, debugui.StackFrame{
			Function: f.Function,
			Module:   f.Module,
			Line:     f.Line,
		})
	}
	debugHub.Broadcast(eui, debugui.Event{
		Name: "debug-stack",
		HTML: debugui.RenderStack(frames),
	})

	var vars []debugui.LocalVar
	for k, v := range result.Locals {
		vars = append(vars, debugui.LocalVar{
			Name:  k,
			Value: fmt.Sprintf("%v", v),
		})
	}
	debugHub.Broadcast(eui, debugui.Event{
		Name: "debug-locals",
		HTML: debugui.RenderLocals(vars),
	})
}
