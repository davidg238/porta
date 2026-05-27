package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/davidg238/porta/internal/debug"
	"github.com/davidg238/porta/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// setup creates an in-memory store and an MCP client session connected to the server.
func setup(t *testing.T) (*store.Store, *mcp.ClientSession) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	dbg := debug.NewManager(st)
	srv := New(st, dbg, "http://127.0.0.1:5686")
	ct, stTransport := mcp.NewInMemoryTransports()

	ctx := context.Background()

	// Connect server first.
	serverSession, err := srv.Connect(ctx, stTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { serverSession.Close() })

	// Then connect client.
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })

	return st, cs
}

func textContent(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if len(result.Content) == 0 {
		t.Fatal("expected at least one content item")
	}
	// Marshal and re-parse to get text. Content is an interface.
	b, err := result.Content[0].MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	// The JSON is {"type":"text","text":"..."}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	text, _ := m["text"].(string)
	return text
}

func TestListDevices(t *testing.T) {
	st, cs := setup(t)
	ctx := context.Background()

	if err := st.DeviceSeen("0011223344556677", "fd00::1", "child", 0x1234); err != nil {
		t.Fatal(err)
	}

	result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "list_devices"})
	if err != nil {
		t.Fatal(err)
	}
	text := textContent(t, result)
	if !strings.Contains(text, "0011223344556677") {
		t.Fatalf("expected device EUI in output, got: %s", text)
	}
}

func TestNameDevice(t *testing.T) {
	st, cs := setup(t)
	ctx := context.Background()

	if err := st.DeviceSeen("aabbccdd11223344", "fd00::2", "child", 0x5678); err != nil {
		t.Fatal(err)
	}

	result, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "name_device",
		Arguments: map[string]any{"eui64": "aabbccdd11223344", "name": "sensor-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := textContent(t, result)
	if !strings.Contains(text, "sensor-1") {
		t.Fatalf("expected name confirmation, got: %s", text)
	}

	// Verify in store.
	devs, err := st.ListDevices()
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) != 1 || devs[0].Name != "sensor-1" {
		t.Fatalf("expected device named sensor-1, got: %+v", devs)
	}
}

func TestNetworkStatus(t *testing.T) {
	st, cs := setup(t)
	ctx := context.Background()

	_ = st.DeviceSeen("dev1111111111111", "fd00::1", "child", 0x1000)
	_ = st.DeviceSeen("dev2222222222222", "fd00::2", "router", 0x2000)

	result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "network_status"})
	if err != nil {
		t.Fatal(err)
	}
	text := textContent(t, result)
	if !strings.Contains(text, "Devices: 2") {
		t.Fatalf("expected 2 devices in status, got: %s", text)
	}
}

func TestQueueCommand(t *testing.T) {
	st, cs := setup(t)
	ctx := context.Background()

	_ = st.DeviceSeen("cmd1111111111111", "fd00::1", "child", 0x1000)
	_ = st.SetDeviceName("cmd1111111111111", "node-a")

	result, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "queue_command",
		Arguments: map[string]any{"device": "node-a", "verb": "reboot"},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := textContent(t, result)
	if !strings.Contains(text, "queued") {
		t.Fatalf("expected queued confirmation, got: %s", text)
	}

	// Verify command in store.
	cmd, err := st.PopCommand("cmd1111111111111")
	if err != nil {
		t.Fatal(err)
	}
	if cmd == nil || cmd.Verb != "reboot" {
		t.Fatalf("expected reboot command, got: %+v", cmd)
	}
}

func TestQueryData(t *testing.T) {
	st, cs := setup(t)
	ctx := context.Background()

	_ = st.DeviceSeen("data111111111111", "fd00::1", "child", 0x1000)
	_ = st.LogData("data111111111111", []byte("temp=22.5"))

	result, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "query_data",
		Arguments: map[string]any{"device": "data111111111111"},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := textContent(t, result)
	if !strings.Contains(text, "temp=22.5") {
		t.Fatalf("expected data payload in output, got: %s", text)
	}
}
