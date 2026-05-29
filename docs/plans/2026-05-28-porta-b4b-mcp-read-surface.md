# porta B4b — MCP read surface Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expose porta's existing read surface as 6 read-only MCP tools over Streamable HTTP at `/mcp`, mounted on the B4a allowlisted HTTP listener.

**Architecture:** New `internal/mcpsrv` package mirrors `internal/web`: `mcpsrv.New(st)` builds an `mcp.Server` with the tools registered, `.Register(mux)` mounts a `StreamableHTTPHandler` at `/mcp`. Every tool handler is a thin adapter over `internal/control` + `internal/store` reads — no new query logic, no writes, no schema/wire change.

**Tech Stack:** Go, `github.com/modelcontextprotocol/go-sdk v1.4.1` (already in `go.mod`), stdlib `net/http`, sqlite store.

---

## Spec

`docs/specs/2026-05-28-porta-b4b-mcp-read-surface-design.md`

## SDK reference (verified against v1.4.1 source)

- `mcp.NewServer(&mcp.Implementation{Name, Version}, nil) *mcp.Server`
- `mcp.AddTool(server, &mcp.Tool{Name, Description}, handler)` where the typed handler is
  `func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error)`.
  The SDK auto-derives input+output JSON schemas from `In`/`Out`. The returned typed `Out` is
  marshalled into `result.StructuredContent`; if the returned `*mcp.CallToolResult.Content` is
  non-nil it is preserved (this is how we attach the text summary).
- `mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, nil) http.Handler`
- Text content: `&mcp.TextContent{Text: "..."}`; error result: `&mcp.CallToolResult{Content: [...], IsError: true}`.
- In-memory test transport: `t1, t2 := mcp.NewInMemoryTransports(); server.Connect(ctx, t1, nil); client := mcp.NewClient(&mcp.Implementation{Name:"test",Version:"v0"}, nil); cs, _ := client.Connect(ctx, t2, nil)`. Then `cs.ListTools(ctx, nil)` and `cs.CallTool(ctx, &mcp.CallToolParams{Name, Arguments})`.

## File structure

- Create `internal/mcpsrv/mcpsrv.go` — `Server`, `New`, `Register`, `registerTools` (grows per task), shared helpers (`textResult`, `errorResultf`, `clampLimit`, `resolve`).
- Create `internal/mcpsrv/devices.go` — `list_devices`, `device_status` (Task 2).
- Create `internal/mcpsrv/config.go` — `device_get_config`, `container_list` (Task 3).
- Create `internal/mcpsrv/telemetry.go` — `query_telemetry` (Task 4).
- Create `internal/mcpsrv/commands.go` — `command_log` (Task 5).
- Create `internal/mcpsrv/mcpsrv_test.go` — package tests (skeleton + per-handler, grows per task).
- Create `internal/mcpsrv/integration_test.go` — in-memory client round-trip (Task 6).
- Modify `internal/portacli/serve.go` — call `mcpsrv.New(st).Register(srv.Mux)` after `web.New(st).Register(srv.Mux)` (Task 1).

## Store/control reads used (verified signatures)

- `(*store.Store).ListNodes() ([]store.Node, error)`
- `(*store.Store).GetNode(id string) (*store.Node, error)` — returns `nil, nil` when absent.
- `(*store.Store).UndeliveredCommands(deviceID string) ([]store.Command, error)`
- `(*store.Store).QueryData(deviceID string, since, until int64, kind string) ([]store.DataRow, error)`
- `(*store.Store).RecentData(deviceID string, limit int) ([]store.DataRow, error)`
- `(*store.Store).CommandLog(deviceID string) ([]store.Command, error)`
- `(*store.Store).RecentCommands(limit int) ([]store.LoggedCommand, error)`
- `control.ResolveNodeID(st, arg) (string, error)` — MAC passthrough or name lookup.
- `control.RelativeAge(ts, now int64) string`
- `control.AppsFromObserved(observed string) ([]control.App, error)` — `App{Name string; CRC, Runlevel int64}`.
- `control.DesiredVsObserved(st, id, app string) ([]control.ConfigRow, error)` — `ConfigRow{Key string; Desired, Observed any; DesiredPresent, ObservedPresent bool; Marker string; ReissueCount int}`.
- `store.Node{ID, Name, SourceAddr, Kind string; LastSeen sql.NullInt64; ObservedState string; ...}` with method `(*Node).Online(now int64) bool`.
- `store.DataRow{TS, Seq int64; Kind, Name string; Value any; Text, ValueType string}`.
- `store.Command{ID int64; Verb, Args string; IssuedAt int64; IssuedBy string; DeliveredAt sql.NullInt64}`; `store.LoggedCommand{Command; DeviceID string}`.

---

## Task 1: Package skeleton + `/mcp` mount + serve wiring

**Files:**
- Create: `internal/mcpsrv/mcpsrv.go`
- Create: `internal/mcpsrv/mcpsrv_test.go`
- Modify: `internal/portacli/serve.go:76`

- [ ] **Step 1: Write the failing test**

`internal/mcpsrv/mcpsrv_test.go`:

```go
package mcpsrv

import (
	"context"
	"testing"

	"github.com/davidg238/porta/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// newTestStore opens an in-memory store for tests.
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// dialTestClient connects an in-memory MCP client to the server under test.
func dialTestClient(t *testing.T, s *Server) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := s.mcp.Connect(ctx, t1, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil)
	cs, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func TestNewServerHasNoToolsYet(t *testing.T) {
	s := New(newTestStore(t))
	cs := dialTestClient(t, s)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(res.Tools) != 0 {
		t.Fatalf("expected 0 tools, got %d", len(res.Tools))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcpsrv/ -run TestNewServerHasNoToolsYet -v`
Expected: FAIL — build error, `New`/`Server` undefined.

- [ ] **Step 3: Write minimal implementation**

`internal/mcpsrv/mcpsrv.go`:

```go
// Package mcpsrv exposes porta's read surface as read-only MCP tools over
// Streamable HTTP. It is a thin adapter over internal/control + internal/store;
// it owns no query logic and performs no writes.
package mcpsrv

import (
	"context"
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
func (s *Server) registerTools() {}

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

// resolve turns a device arg (MAC or friendly name) into a node id, or returns
// an IsError result to hand straight back to the caller.
func (s *Server) resolve(device string) (string, *mcp.CallToolResult) {
	id, err := control.ResolveNodeID(s.st, device)
	if err != nil {
		return "", errorResultf("resolve device %q: %v", device, err)
	}
	return id, nil
}

var _ = context.Background // keep context imported until tools land
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mcpsrv/ -run TestNewServerHasNoToolsYet -v`
Expected: PASS.

- [ ] **Step 5: Wire into serve**

In `internal/portacli/serve.go`, immediately after line 76 (`web.New(st).Register(srv.Mux)`), add:

```go
				mcpsrv.New(st).Register(srv.Mux)
```

Add the import to the import block (alphabetical, alongside the other `internal/...` imports):

```go
	"github.com/davidg238/porta/internal/mcpsrv"
```

- [ ] **Step 6: Verify build + full package tests**

Run: `go build ./... && go test ./internal/mcpsrv/ ./internal/portacli/`
Expected: build clean; tests PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/mcpsrv/mcpsrv.go internal/mcpsrv/mcpsrv_test.go internal/portacli/serve.go
git commit -m "feat(porta): mcp — internal/mcpsrv skeleton, mount /mcp on http listener (B4b task 1)"
```

---

## Task 2: `list_devices` + `device_status`

**Files:**
- Create: `internal/mcpsrv/devices.go`
- Modify: `internal/mcpsrv/mcpsrv.go` (registerTools)
- Modify: `internal/mcpsrv/mcpsrv_test.go` (add tests)

- [ ] **Step 1: Write the failing tests**

Append to `internal/mcpsrv/mcpsrv_test.go`:

```go
func TestListDevices(t *testing.T) {
	st := newTestStore(t)
	if err := st.EnsureNode("aabbccddeeff", 1000); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchNode("aabbccddeeff", "192.168.1.5:6970", 1000); err != nil {
		t.Fatal(err)
	}
	if err := st.SetNodeName("aabbccddeeff", "sensor-1"); err != nil {
		t.Fatal(err)
	}

	s := New(st)
	s.now = func() int64 { return 1000 }

	res, out, err := s.listDevices(context.Background(), nil, ListDevicesInput{})
	if err != nil {
		t.Fatalf("listDevices: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %v", res.Content)
	}
	if len(out.Devices) != 1 {
		t.Fatalf("expected 1 device, got %d", len(out.Devices))
	}
	d := out.Devices[0]
	if d.ID != "aabbccddeeff" || d.Name != "sensor-1" || d.Kind != "toit" {
		t.Fatalf("unexpected device: %+v", d)
	}
	if !d.Online {
		t.Fatalf("expected online at now==last_seen")
	}
	if d.Age != "0s ago" {
		t.Fatalf("expected age %q, got %q", "0s ago", d.Age)
	}
}

func TestDeviceStatusResolvesByName(t *testing.T) {
	st := newTestStore(t)
	if err := st.EnsureNode("aabbccddeeff", 1000); err != nil {
		t.Fatal(err)
	}
	if err := st.SetNodeName("aabbccddeeff", "sensor-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnqueueCommand("aabbccddeeff", "stop", `{"name":"x"}`, "cli", 1001); err != nil {
		t.Fatal(err)
	}

	s := New(st)
	s.now = func() int64 { return 1002 }

	res, out, err := s.deviceStatus(context.Background(), nil, DeviceInput{Device: "sensor-1"})
	if err != nil {
		t.Fatalf("deviceStatus: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %v", res.Content)
	}
	if out.ID != "aabbccddeeff" {
		t.Fatalf("expected resolved id, got %q", out.ID)
	}
	if out.UndeliveredCount != 1 {
		t.Fatalf("expected 1 undelivered, got %d", out.UndeliveredCount)
	}
}

func TestDeviceStatusUnknownIsError(t *testing.T) {
	s := New(newTestStore(t))
	res, _, err := s.deviceStatus(context.Background(), nil, DeviceInput{Device: "nope"})
	if err != nil {
		t.Fatalf("handler should not return a Go error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError result for unknown device")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/mcpsrv/ -run 'TestListDevices|TestDeviceStatus' -v`
Expected: FAIL — build error, `listDevices`/`deviceStatus`/types undefined.

- [ ] **Step 3: Write the implementation**

`internal/mcpsrv/devices.go`:

```go
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
	id, errRes := s.resolve(in.Device)
	if errRes != nil {
		return errRes, DeviceStatusOutput{}, nil
	}
	now := s.now()
	n, err := s.st.GetNode(id)
	if err != nil {
		return errorResultf("get node %q: %v", id, err), DeviceStatusOutput{}, nil
	}
	if n == nil {
		return errorResultf("no node %q", id), DeviceStatusOutput{}, nil
	}
	undelivered, err := s.st.UndeliveredCommands(id)
	if err != nil {
		return errorResultf("undelivered for %q: %v", id, err), DeviceStatusOutput{}, nil
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
```

Replace `registerTools` in `internal/mcpsrv/mcpsrv.go` and drop the now-unneeded keep-alive line:

```go
func (s *Server) registerTools() {
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "list_devices",
		Description: "List all known nodes with online status and last-seen age.",
	}, s.listDevices)
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "device_status",
		Description: "Show one node's status: kind, source addr, last seen, observed state, undelivered command count.",
	}, s.deviceStatus)
}
```

Delete the `var _ = context.Background ...` line from `mcpsrv.go` (devices.go now imports context legitimately; `mcpsrv.go` no longer needs `context`). Remove the `"context"` import from `mcpsrv.go` if it is otherwise unused.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/mcpsrv/ -run 'TestListDevices|TestDeviceStatus|TestNewServer' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/mcpsrv/
git commit -m "feat(porta): mcp — list_devices + device_status tools (B4b task 2)"
```

---

## Task 3: `device_get_config` + `container_list`

**Files:**
- Create: `internal/mcpsrv/config.go`
- Modify: `internal/mcpsrv/mcpsrv.go` (registerTools)
- Modify: `internal/mcpsrv/mcpsrv_test.go` (add tests)

- [ ] **Step 1: Write the failing tests**

Append to `internal/mcpsrv/mcpsrv_test.go`:

```go
// seedObserved inserts a report carrying an observed_state JSON blob. Verified:
// store.InsertReport (internal/store/store.go:342) UPDATEs nodes.observed_state
// in the same transaction, so GetNode(id).ObservedState returns this blob —
// no extra call needed.
func seedObserved(t *testing.T, st *store.Store, id, observed string, now int64) {
	t.Helper()
	if err := st.EnsureNode(id, now); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchNode(id, "10.0.0.1:6970", now); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertReport(id, observed, "ok", now); err != nil {
		t.Fatal(err)
	}
}

func TestContainerList(t *testing.T) {
	st := newTestStore(t)
	observed := `{"apps":{"control-demo":{"crc":42,"runlevel":3},"watchdog":{"crc":7,"runlevel":1}}}`
	seedObserved(t, st, "aabbccddeeff", observed, 2000)

	s := New(st)
	res, out, err := s.containerList(context.Background(), nil, DeviceInput{Device: "aabbccddeeff"})
	if err != nil || res.IsError {
		t.Fatalf("containerList err=%v isErr=%v", err, res.IsError)
	}
	if len(out.Containers) != 2 {
		t.Fatalf("expected 2 containers, got %d", len(out.Containers))
	}
	// AppsFromObserved sorts by name: control-demo before watchdog.
	if out.Containers[0].Name != "control-demo" || out.Containers[0].Runlevel != 3 {
		t.Fatalf("unexpected first container: %+v", out.Containers[0])
	}
}

func TestDeviceGetConfigAllApps(t *testing.T) {
	st := newTestStore(t)
	observed := `{"apps":{"control-demo":{"crc":42,"runlevel":3}},"config":{"control-demo":{"interval":30}}}`
	seedObserved(t, st, "aabbccddeeff", observed, 2000)

	s := New(st)
	// no App → enumerate observed installed apps
	res, out, err := s.deviceGetConfig(context.Background(), nil, DeviceConfigInput{Device: "aabbccddeeff"})
	if err != nil || res.IsError {
		t.Fatalf("deviceGetConfig err=%v isErr=%v", err, res.IsError)
	}
	if len(out.Rows) != 1 {
		t.Fatalf("expected 1 config row, got %d: %+v", len(out.Rows), out.Rows)
	}
	if out.Rows[0].App != "control-demo" || out.Rows[0].Key != "interval" {
		t.Fatalf("unexpected row: %+v", out.Rows[0])
	}
}
```


- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/mcpsrv/ -run 'TestContainerList|TestDeviceGetConfig' -v`
Expected: FAIL — build error, handlers/types undefined.

- [ ] **Step 3: Write the implementation**

`internal/mcpsrv/config.go`:

```go
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

	apps := []string{in.App}
	if in.App == "" {
		// Enumerate observed installed apps (same source the web detail config
		// panel uses); see internal/web/pages.go.
		installed, err := control.AppsFromObserved(n.ObservedState)
		if err != nil {
			return errorResultf("decode observed apps for %q: %v", id, err), DeviceConfigOutput{}, nil
		}
		apps = apps[:0]
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
```

Add to `registerTools` in `mcpsrv.go`:

```go
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "device_get_config",
		Description: "Show desired-vs-observed config rows for one app, or all observed apps when app is omitted.",
	}, s.deviceGetConfig)
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "container_list",
		Description: "List a node's installed containers (name, crc, runlevel) from observed state.",
	}, s.containerList)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/mcpsrv/ -run 'TestContainerList|TestDeviceGetConfig' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/mcpsrv/
git commit -m "feat(porta): mcp — device_get_config + container_list tools (B4b task 3)"
```

---

## Task 4: `query_telemetry`

**Files:**
- Create: `internal/mcpsrv/telemetry.go`
- Modify: `internal/mcpsrv/mcpsrv.go` (registerTools)
- Modify: `internal/mcpsrv/mcpsrv_test.go` (add tests)

- [ ] **Step 1: Write the failing tests**

Append to `internal/mcpsrv/mcpsrv_test.go`:

```go
func TestQueryTelemetryRecentAndLimit(t *testing.T) {
	st := newTestStore(t)
	if err := st.EnsureNode("aabbccddeeff", 3000); err != nil {
		t.Fatal(err)
	}
	for i := int64(0); i < 5; i++ {
		if err := st.InsertData("aabbccddeeff", 3000+i, i, "metric", "pm25", float64(i), "", "float"); err != nil {
			t.Fatal(err)
		}
	}
	s := New(st)

	// No since/until → RecentData path, newest-first, limit clamp default 100.
	res, out, err := s.queryTelemetry(context.Background(), nil, QueryTelemetryInput{Device: "aabbccddeeff"})
	if err != nil || res.IsError {
		t.Fatalf("queryTelemetry err=%v isErr=%v", err, res.IsError)
	}
	if len(out.Rows) != 5 {
		t.Fatalf("expected 5 rows, got %d", len(out.Rows))
	}
	if out.Rows[0].TS != 3004 {
		t.Fatalf("expected newest-first (ts 3004), got %d", out.Rows[0].TS)
	}

	// limit honored
	_, out2, _ := s.queryTelemetry(context.Background(), nil, QueryTelemetryInput{Device: "aabbccddeeff", Limit: 2})
	if len(out2.Rows) != 2 {
		t.Fatalf("expected 2 rows with limit=2, got %d", len(out2.Rows))
	}
}

func TestQueryTelemetryWindow(t *testing.T) {
	st := newTestStore(t)
	if err := st.EnsureNode("aabbccddeeff", 3000); err != nil {
		t.Fatal(err)
	}
	for i := int64(0); i < 5; i++ {
		if err := st.InsertData("aabbccddeeff", 3000+i, i, "metric", "pm25", float64(i), "", "float"); err != nil {
			t.Fatal(err)
		}
	}
	s := New(st)
	since, until := int64(3001), int64(3003)
	_, out, err := s.queryTelemetry(context.Background(), nil, QueryTelemetryInput{Device: "aabbccddeeff", Since: since, Until: until})
	if err != nil {
		t.Fatalf("queryTelemetry: %v", err)
	}
	for _, r := range out.Rows {
		if r.TS < since || r.TS > until {
			t.Fatalf("row ts %d outside [%d,%d]", r.TS, since, until)
		}
	}
}
```

VERIFIED (internal/store/data.go): `QueryData` filters `ts >= since AND ts <= until` (inclusive both ends), filters `kind` only when non-empty, and orders `ts, seq` (oldest-first). The window test asserts only membership in `[since, until]`, so it holds either way.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/mcpsrv/ -run TestQueryTelemetry -v`
Expected: FAIL — build error, `queryTelemetry`/types undefined.

- [ ] **Step 3: Write the implementation**

`internal/mcpsrv/telemetry.go`:

```go
package mcpsrv

import (
	"context"
	"fmt"

	"github.com/davidg238/porta/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// QueryTelemetryInput bounds a telemetry query. When Since and Until are both
// zero, the most recent rows are returned; otherwise the [Since,Until] window
// is queried. Kind filters by row kind when non-empty. Limit defaults to 100,
// caps at 1000.
type QueryTelemetryInput struct {
	Device string `json:"device" jsonschema:"node MAC (12 lowercase hex) or friendly name"`
	Since  int64  `json:"since,omitempty" jsonschema:"window start, epoch seconds"`
	Until  int64  `json:"until,omitempty" jsonschema:"window end, epoch seconds"`
	Kind   string `json:"kind,omitempty" jsonschema:"filter by telemetry kind"`
	Limit  int    `json:"limit,omitempty" jsonschema:"max rows (default 100, max 1000)"`
}

// TelemetryRow is one telemetry sample.
type TelemetryRow struct {
	TS        int64  `json:"ts"`
	Seq       int64  `json:"seq"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Value     any    `json:"value"`
	Text      string `json:"text"`
	ValueType string `json:"value_type"`
}

// QueryTelemetryOutput is the structured result of query_telemetry.
type QueryTelemetryOutput struct {
	Rows []TelemetryRow `json:"rows"`
}

func (s *Server) queryTelemetry(_ context.Context, _ *mcp.CallToolRequest, in QueryTelemetryInput) (*mcp.CallToolResult, QueryTelemetryOutput, error) {
	id, errRes := s.resolve(in.Device)
	if errRes != nil {
		return errRes, QueryTelemetryOutput{}, nil
	}
	limit := clampLimit(in.Limit)

	var rows []store.DataRow
	var err error
	if in.Since == 0 && in.Until == 0 {
		rows, err = s.st.RecentData(id, limit)
	} else {
		rows, err = s.st.QueryData(id, in.Since, in.Until, in.Kind)
		if len(rows) > limit {
			rows = rows[:limit]
		}
	}
	if err != nil {
		return errorResultf("query telemetry for %q: %v", id, err), QueryTelemetryOutput{}, nil
	}

	out := QueryTelemetryOutput{Rows: make([]TelemetryRow, 0, len(rows))}
	for _, r := range rows {
		out.Rows = append(out.Rows, TelemetryRow{
			TS: r.TS, Seq: r.Seq, Kind: r.Kind, Name: r.Name,
			Value: r.Value, Text: r.Text, ValueType: r.ValueType,
		})
	}
	return textResult(fmt.Sprintf("%s: %d telemetry row(s)", id, len(out.Rows))), out, nil
}
```

VERIFIED (internal/store/data.go): `RecentData` orders `ts DESC, seq DESC` (newest-first) — this is why `TestQueryTelemetryRecentAndLimit` asserts `Rows[0].TS==3004`. `QueryData` is oldest-first, so the post-slice `rows[:limit]` keeps the earliest rows in the window — acceptable; the spec does not require newest-first for windows.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/mcpsrv/ -run TestQueryTelemetry -v`
Expected: PASS.

- [ ] **Step 5: Add registration + commit**

Add to `registerTools` in `mcpsrv.go`:

```go
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "query_telemetry",
		Description: "Query a node's telemetry: recent rows, or a [since,until] epoch-seconds window, optional kind filter, limit default 100 max 1000.",
	}, s.queryTelemetry)
```

```bash
git add internal/mcpsrv/
git commit -m "feat(porta): mcp — query_telemetry tool (B4b task 4)"
```

---

## Task 5: `command_log`

**Files:**
- Create: `internal/mcpsrv/commands.go`
- Modify: `internal/mcpsrv/mcpsrv.go` (registerTools)
- Modify: `internal/mcpsrv/mcpsrv_test.go` (add tests)

- [ ] **Step 1: Write the failing tests**

Append to `internal/mcpsrv/mcpsrv_test.go`:

```go
func TestCommandLogFleetWideAndPerDevice(t *testing.T) {
	st := newTestStore(t)
	if err := st.EnsureNode("aaaaaaaaaaaa", 4000); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureNode("bbbbbbbbbbbb", 4000); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnqueueCommand("aaaaaaaaaaaa", "stop", `{"name":"x"}`, "cli", 4001); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnqueueCommand("bbbbbbbbbbbb", "stop", `{"name":"y"}`, "web", 4002); err != nil {
		t.Fatal(err)
	}
	s := New(st)

	// Fleet-wide: both commands, each carrying its device.
	_, all, err := s.commandLog(context.Background(), nil, CommandLogInput{})
	if err != nil {
		t.Fatalf("commandLog fleet: %v", err)
	}
	if len(all.Commands) != 2 {
		t.Fatalf("expected 2 fleet commands, got %d", len(all.Commands))
	}
	for _, c := range all.Commands {
		if c.Device == "" {
			t.Fatalf("fleet-wide row missing device: %+v", c)
		}
	}

	// Per-device: only that node's command.
	_, one, err := s.commandLog(context.Background(), nil, CommandLogInput{Device: "aaaaaaaaaaaa"})
	if err != nil {
		t.Fatalf("commandLog per-device: %v", err)
	}
	if len(one.Commands) != 1 || one.Commands[0].Device != "aaaaaaaaaaaa" {
		t.Fatalf("expected 1 command for aaaaaaaaaaaa, got %+v", one.Commands)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/mcpsrv/ -run TestCommandLog -v`
Expected: FAIL — build error, `commandLog`/types undefined.

- [ ] **Step 3: Write the implementation**

`internal/mcpsrv/commands.go`:

```go
package mcpsrv

import (
	"context"
	"fmt"

	"github.com/davidg238/porta/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// CommandLogInput selects the fleet-wide audit (Device empty) or one node's
// full command log (Device set). Limit applies only to the fleet-wide path
// (RecentCommands); default 100, cap 1000.
type CommandLogInput struct {
	Device string `json:"device,omitempty" jsonschema:"node MAC or name; omit for fleet-wide audit"`
	Limit  int    `json:"limit,omitempty" jsonschema:"max rows for fleet-wide audit (default 100, max 1000)"`
}

// CommandEntry is one command-queue row.
type CommandEntry struct {
	ID          int64  `json:"id"`
	Device      string `json:"device"`
	Verb        string `json:"verb"`
	Args        string `json:"args"`
	IssuedBy    string `json:"issued_by"`
	IssuedAt    int64  `json:"issued_at"`
	DeliveredAt int64  `json:"delivered_at"`
}

// CommandLogOutput is the structured result of command_log.
type CommandLogOutput struct {
	Commands []CommandEntry `json:"commands"`
}

func entryFromCommand(c store.Command, device string) CommandEntry {
	return CommandEntry{
		ID:          c.ID,
		Device:      device,
		Verb:        c.Verb,
		Args:        c.Args,
		IssuedBy:    c.IssuedBy,
		IssuedAt:    c.IssuedAt,
		DeliveredAt: c.DeliveredAt.Int64, // 0 when undelivered (NULL)
	}
}

func (s *Server) commandLog(_ context.Context, _ *mcp.CallToolRequest, in CommandLogInput) (*mcp.CallToolResult, CommandLogOutput, error) {
	out := CommandLogOutput{Commands: []CommandEntry{}}

	if in.Device == "" {
		logged, err := s.st.RecentCommands(clampLimit(in.Limit))
		if err != nil {
			return errorResultf("recent commands: %v", err), CommandLogOutput{}, nil
		}
		for _, lc := range logged {
			out.Commands = append(out.Commands, entryFromCommand(lc.Command, lc.DeviceID))
		}
		return textResult(fmt.Sprintf("%d command(s) (fleet)", len(out.Commands))), out, nil
	}

	id, errRes := s.resolve(in.Device)
	if errRes != nil {
		return errRes, CommandLogOutput{}, nil
	}
	cmds, err := s.st.CommandLog(id)
	if err != nil {
		return errorResultf("command log for %q: %v", id, err), CommandLogOutput{}, nil
	}
	for _, c := range cmds {
		out.Commands = append(out.Commands, entryFromCommand(c, id))
	}
	return textResult(fmt.Sprintf("%s: %d command(s)", id, len(out.Commands))), out, nil
}
```

Add to `registerTools` in `mcpsrv.go`:

```go
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "command_log",
		Description: "Command audit: fleet-wide recent commands when device is omitted (limit default 100 max 1000), or one node's full command log when device is set.",
	}, s.commandLog)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/mcpsrv/ -run TestCommandLog -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/mcpsrv/
git commit -m "feat(porta): mcp — command_log tool (B4b task 5)"
```

---

## Task 6: Round-trip integration test (all 6 tools)

**Files:**
- Create: `internal/mcpsrv/integration_test.go`

- [ ] **Step 1: Write the test**

`internal/mcpsrv/integration_test.go`:

```go
package mcpsrv

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestListAllSixTools(t *testing.T) {
	s := New(newTestStore(t))
	cs := dialTestClient(t, s)

	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	got := map[string]bool{}
	for _, tool := range res.Tools {
		got[tool.Name] = true
		if tool.InputSchema == nil {
			t.Errorf("tool %q has nil input schema", tool.Name)
		}
	}
	for _, want := range []string{
		"list_devices", "device_status", "device_get_config",
		"container_list", "query_telemetry", "command_log",
	} {
		if !got[want] {
			t.Errorf("missing tool %q", want)
		}
	}
	if len(res.Tools) != 6 {
		t.Errorf("expected 6 tools, got %d", len(res.Tools))
	}
}

func TestCallListDevicesEndToEnd(t *testing.T) {
	st := newTestStore(t)
	if err := st.EnsureNode("aabbccddeeff", 5000); err != nil {
		t.Fatal(err)
	}
	s := New(st)
	cs := dialTestClient(t, s)

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_devices",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("call list_devices: %v", err)
	}
	if res.IsError {
		t.Fatalf("list_devices returned error: %v", res.Content)
	}
	if res.StructuredContent == nil {
		t.Fatalf("expected structured content")
	}
	if len(res.Content) == 0 {
		t.Fatalf("expected a text summary alongside structured content")
	}
}

func TestCallUnknownDeviceIsErrorResult(t *testing.T) {
	s := New(newTestStore(t))
	cs := dialTestClient(t, s)

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "device_status",
		Arguments: map[string]any{"device": "nope"},
	})
	if err != nil {
		t.Fatalf("transport error (should be a tool IsError instead): %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError for unknown device")
	}
}
```

- [ ] **Step 2: Run the integration tests**

Run: `go test ./internal/mcpsrv/ -run 'TestListAllSixTools|TestCall' -v`
Expected: PASS. If `tool.InputSchema` is a non-pointer type in v1.4.1 (so the nil check won't compile), assert on a stable field instead (e.g. `tool.Name != ""`) — adjust to the actual `mcp.Tool` shape rather than forcing the check.

- [ ] **Step 3: Full suite + build + vet**

Run: `go build ./... && go vet ./internal/mcpsrv/ && go test ./...`
Expected: build clean, vet clean, all packages PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/mcpsrv/integration_test.go
git commit -m "test(porta): mcp — round-trip integration test, all 6 tools (B4b task 6)"
```

- [ ] **Step 5: Final whole-implementation review**

Dispatch a code review (superpowers:requesting-code-review or /code-review) over the branch diff. Confirm: read-only (no `EnqueueCommand`/`Register*`/writes in `internal/mcpsrv`), all 6 tools registered, error paths return `IsError` (never panic, never a raw Go error for expected failures), limit clamping correct, structured output + text summary both present. Address findings, then proceed to finishing-a-development-branch.

---

## Verification (acceptance)

1. `go test ./...` green.
2. `go build ./...` clean.
3. Manual smoke: `go run ./cmd/porta serve --http-port 6970` in one shell; in another, `curl -sS -X POST http://127.0.0.1:6970/mcp -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'` returns a JSON-RPC `initialize` result naming server `porta`. (Confirms the endpoint is mounted on the allowlisted listener.)
4. Optional: point an MCP client (Claude Code/Desktop) at `http://127.0.0.1:6970/mcp`, list tools (6), call `list_devices`.
