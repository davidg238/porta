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
	if len(res.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(res.Tools))
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
