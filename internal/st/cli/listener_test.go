// Copyright (c) 2026 Ekorau LLC

package cli

import (
	"bufio"
	"encoding/json"
	"net"
	"testing"

	"github.com/davidg238/porta/internal/st/gateway"
	"github.com/davidg238/porta/internal/st/store"
)

// testListener opens an in-memory store, creates a Gateway and TCP listener on
// localhost:0, starts Serve in a goroutine, and registers cleanup.
func testListener(t *testing.T) (*Listener, string) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	gw := gateway.New(st)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	l := NewListener(gw, ln)
	go l.Serve()
	t.Cleanup(func() { ln.Close() })

	return l, ln.Addr().String()
}

// sendRecv dials the listener, sends a JSON request + newline, reads one JSON
// line response, and returns the parsed Response.
func sendRecv(t *testing.T, addr string, req Request) Response {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		t.Fatal(err)
	}

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		t.Fatal("no response from listener")
	}

	var resp Response
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v\nbody: %s", err, scanner.Text())
	}
	return resp
}

func TestListDevices(t *testing.T) {
	l, addr := testListener(t)
	st := l.gw.Store

	if err := st.DeviceSeen("aabbccddeeff0011", "fd00::1", "child", 0x1234); err != nil {
		t.Fatal(err)
	}

	resp := sendRecv(t, addr, Request{Cmd: "devices"})
	if !resp.OK {
		t.Fatalf("expected ok=true, got error: %s", resp.Error)
	}
	if resp.Data == nil {
		t.Fatal("expected data, got nil")
	}

	// Data should contain the EUI-64.
	raw, _ := json.Marshal(resp.Data)
	if got := string(raw); !contains(got, "aabbccddeeff0011") {
		t.Fatalf("expected device EUI in data, got: %s", got)
	}
}

func TestQueueCommand(t *testing.T) {
	l, addr := testListener(t)
	st := l.gw.Store

	if err := st.DeviceSeen("aabbccddeeff0011", "fd00::1", "child", 0x1234); err != nil {
		t.Fatal(err)
	}
	if err := st.SetDeviceName("aabbccddeeff0011", "sensor1"); err != nil {
		t.Fatal(err)
	}

	resp := sendRecv(t, addr, Request{
		Cmd:    "queue",
		Device: "sensor1",
		Verb:   "status",
	})
	if !resp.OK {
		t.Fatalf("expected ok=true, got error: %s", resp.Error)
	}

	cmd, err := st.PopCommand("aabbccddeeff0011")
	if err != nil {
		t.Fatal(err)
	}
	if cmd == nil {
		t.Fatal("expected queued command, got nil")
	}
	if cmd.Verb != "status" {
		t.Fatalf("expected verb=status, got %q", cmd.Verb)
	}
}

func TestNameDevice(t *testing.T) {
	l, addr := testListener(t)
	st := l.gw.Store

	if err := st.DeviceSeen("aabb", "fd00::1", "child", 0); err != nil {
		t.Fatal(err)
	}

	resp := sendRecv(t, addr, Request{
		Cmd:    "name",
		Device: "aabb",
		Name:   "sensor1",
	})
	if !resp.OK {
		t.Fatalf("expected ok=true, got error: %s", resp.Error)
	}

	// Verify the name is set by resolving it.
	eui, err := st.ResolveDevice("sensor1")
	if err != nil {
		t.Fatal(err)
	}
	if eui != "aabb" {
		t.Fatalf("expected eui=aabb, got %q", eui)
	}
}

func TestUnknownCommand(t *testing.T) {
	_, addr := testListener(t)

	resp := sendRecv(t, addr, Request{Cmd: "bogus"})
	if resp.OK {
		t.Fatal("expected ok=false for unknown command")
	}
	if resp.Error == "" {
		t.Fatal("expected error message")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsBytes(s, sub))
}

func containsBytes(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
