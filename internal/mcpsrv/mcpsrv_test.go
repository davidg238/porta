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

func TestNewServerRegistersTools(t *testing.T) {
	s := New(newTestStore(t))
	cs := dialTestClient(t, s)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(res.Tools) != 4 {
		t.Fatalf("expected 4 tools, got %d", len(res.Tools))
	}
	names := make(map[string]bool)
	for _, tool := range res.Tools {
		names[tool.Name] = true
	}
	if !names["list_devices"] {
		t.Fatalf("expected list_devices tool, got %v", res.Tools)
	}
	if !names["device_status"] {
		t.Fatalf("expected device_status tool, got %v", res.Tools)
	}
	if !names["device_get_config"] {
		t.Fatalf("expected device_get_config tool, got %v", res.Tools)
	}
	if !names["container_list"] {
		t.Fatalf("expected container_list tool, got %v", res.Tools)
	}
}

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

// seedObserved inserts a report carrying an observed_state JSON blob. Verified:
// store.InsertReport UPDATEs nodes.observed_state in the same transaction, so
// GetNode(id).ObservedState returns this blob — no extra call needed.
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
