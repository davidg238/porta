// Copyright (c) 2026 Ekorau LLC

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
