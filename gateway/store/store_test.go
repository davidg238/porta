package store

import (
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open(:memory:): %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestDeviceUpsert(t *testing.T) {
	s := testStore(t)

	err := s.DeviceSeen("00112233aabbccdd", "fd00::1", "child", 0x4001)
	if err != nil {
		t.Fatalf("DeviceSeen: %v", err)
	}

	devs, err := s.ListDevices()
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(devs) != 1 {
		t.Fatalf("expected 1 device, got %d", len(devs))
	}
	d := devs[0]
	if d.EUI64 != "00112233aabbccdd" {
		t.Errorf("EUI64 = %q", d.EUI64)
	}
	if d.SourceAddr != "fd00::1" {
		t.Errorf("SourceAddr = %q", d.SourceAddr)
	}
	if d.Role != "child" {
		t.Errorf("Role = %q", d.Role)
	}
	if d.RLOC16 != 0x4001 {
		t.Errorf("RLOC16 = %d", d.RLOC16)
	}
	if d.FirstSeen.IsZero() {
		t.Error("FirstSeen is zero")
	}
	if d.LastSeen.IsZero() {
		t.Error("LastSeen is zero")
	}
}

func TestDeviceUpsertUpdatesLastSeen(t *testing.T) {
	s := testStore(t)

	err := s.DeviceSeen("aabb", "fd00::1", "child", 0x4001)
	if err != nil {
		t.Fatalf("first DeviceSeen: %v", err)
	}
	devs, _ := s.ListDevices()
	first := devs[0].LastSeen

	time.Sleep(5 * time.Millisecond)

	err = s.DeviceSeen("aabb", "fd00::2", "router", 0x4002)
	if err != nil {
		t.Fatalf("second DeviceSeen: %v", err)
	}
	devs, _ = s.ListDevices()
	d := devs[0]

	if !d.LastSeen.After(first) {
		t.Errorf("LastSeen not updated: %v vs %v", d.LastSeen, first)
	}
	if d.SourceAddr != "fd00::2" {
		t.Errorf("SourceAddr not updated: %q", d.SourceAddr)
	}
	if d.Role != "router" {
		t.Errorf("Role not updated: %q", d.Role)
	}
}

func TestDeviceName(t *testing.T) {
	s := testStore(t)

	s.DeviceSeen("aabb", "fd00::1", "child", 0x4001)
	err := s.SetDeviceName("aabb", "sensor-1")
	if err != nil {
		t.Fatalf("SetDeviceName: %v", err)
	}

	devs, _ := s.ListDevices()
	if devs[0].Name != "sensor-1" {
		t.Errorf("Name = %q, want sensor-1", devs[0].Name)
	}
}

func TestResolveDevice(t *testing.T) {
	s := testStore(t)

	s.DeviceSeen("00112233aabbccdd", "fd00::1", "child", 0x4001)
	s.SetDeviceName("00112233aabbccdd", "sensor-1")

	// Resolve by EUI-64
	eui, err := s.ResolveDevice("00112233aabbccdd")
	if err != nil {
		t.Fatalf("ResolveDevice by EUI: %v", err)
	}
	if eui != "00112233aabbccdd" {
		t.Errorf("got %q", eui)
	}

	// Resolve by name
	eui, err = s.ResolveDevice("sensor-1")
	if err != nil {
		t.Fatalf("ResolveDevice by name: %v", err)
	}
	if eui != "00112233aabbccdd" {
		t.Errorf("got %q", eui)
	}

	// Unknown
	_, err = s.ResolveDevice("unknown")
	if err == nil {
		t.Error("expected error for unknown device")
	}
}

func TestCommandQueue(t *testing.T) {
	s := testStore(t)

	s.DeviceSeen("dev1", "", "", 0)
	s.DeviceSeen("dev2", "", "", 0)

	// Queue commands for dev1
	s.QueueCommand("dev1", "reboot", nil)
	s.QueueCommand("dev1", "flash", []byte{0x01, 0x02})
	// Queue command for dev2
	s.QueueCommand("dev2", "ping", nil)

	// Pop dev1 commands in FIFO order
	cmd, err := s.PopCommand("dev1")
	if err != nil {
		t.Fatalf("PopCommand: %v", err)
	}
	if cmd == nil || cmd.Verb != "reboot" {
		t.Fatalf("expected reboot, got %+v", cmd)
	}

	cmd, _ = s.PopCommand("dev1")
	if cmd == nil || cmd.Verb != "flash" {
		t.Fatalf("expected flash, got %+v", cmd)
	}
	if len(cmd.Payload) != 2 || cmd.Payload[0] != 0x01 {
		t.Errorf("payload mismatch: %v", cmd.Payload)
	}

	// dev1 empty
	cmd, _ = s.PopCommand("dev1")
	if cmd != nil {
		t.Errorf("expected nil, got %+v", cmd)
	}

	// dev2 independent
	cmd, _ = s.PopCommand("dev2")
	if cmd == nil || cmd.Verb != "ping" {
		t.Fatalf("expected ping, got %+v", cmd)
	}
}

func TestCommandQueuePrune(t *testing.T) {
	s := testStore(t)

	s.DeviceSeen("dev1", "", "", 0)
	s.QueueCommand("dev1", "reboot", nil)

	// Pop so it gets a sent_at timestamp.
	cmd, err := s.PopCommand("dev1")
	if err != nil || cmd == nil {
		t.Fatalf("PopCommand: err=%v cmd=%v", err, cmd)
	}

	// maxAge=1h should not delete a just-sent command.
	n, err := s.PruneCommands(time.Hour)
	if err != nil {
		t.Fatalf("PruneCommands(1h): %v", err)
	}
	if n != 0 {
		t.Errorf("pruned %d, want 0 (command is recent)", n)
	}

	// maxAge=0 should delete it (cutoff = now).
	n, err = s.PruneCommands(0)
	if err != nil {
		t.Fatalf("PruneCommands(0): %v", err)
	}
	if n != 1 {
		t.Errorf("pruned %d, want 1", n)
	}
}

func TestDataLog(t *testing.T) {
	s := testStore(t)

	s.DeviceSeen("dev1", "", "", 0)

	s.LogData("dev1", []byte("temp=22"))
	time.Sleep(5 * time.Millisecond)
	mid := time.Now()
	time.Sleep(5 * time.Millisecond)
	s.LogData("dev1", []byte("temp=23"))

	// Query all
	rows, err := s.QueryData("dev1", time.Time{}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("QueryData: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if string(rows[0].Payload) != "temp=22" {
		t.Errorf("row 0 payload = %q", rows[0].Payload)
	}

	// Query with time filter — only second row
	rows, _ = s.QueryData("dev1", mid, time.Now().Add(time.Hour))
	if len(rows) != 1 {
		t.Fatalf("expected 1 row after mid, got %d", len(rows))
	}
	if string(rows[0].Payload) != "temp=23" {
		t.Errorf("payload = %q", rows[0].Payload)
	}

	// Different device returns nothing
	rows, _ = s.QueryData("dev2", time.Time{}, time.Now().Add(time.Hour))
	if len(rows) != 0 {
		t.Errorf("expected 0 rows for dev2, got %d", len(rows))
	}
}

func TestDataLogPrune(t *testing.T) {
	s := testStore(t)

	s.LogData("dev1", []byte("old"))
	time.Sleep(10 * time.Millisecond)

	// maxAge=0 prunes all
	n, err := s.PruneData(0)
	if err != nil {
		t.Fatalf("PruneData(0): %v", err)
	}
	if n != 1 {
		t.Errorf("pruned %d, want 1", n)
	}

	// Add fresh data
	s.LogData("dev1", []byte("new"))

	// maxAge=1h prunes none
	n, _ = s.PruneData(time.Hour)
	if n != 0 {
		t.Errorf("pruned %d, want 0", n)
	}
}

func TestDebugBreakpoints(t *testing.T) {
	s := testStore(t)

	err := s.SetDebugBreakpoint("aabb", "sensor", 12, 7, 11)
	if err != nil {
		t.Fatalf("SetDebugBreakpoint: %v", err)
	}

	bps, err := s.ListDebugBreakpoints("aabb")
	if err != nil {
		t.Fatalf("ListDebugBreakpoints: %v", err)
	}
	if len(bps) != 1 {
		t.Fatalf("expected 1 breakpoint, got %d", len(bps))
	}
	if bps[0].Module != "sensor" || bps[0].STLine != 12 {
		t.Errorf("breakpoint = %+v", bps[0])
	}

	err = s.ClearDebugBreakpoint("aabb", "sensor", 12)
	if err != nil {
		t.Fatalf("ClearDebugBreakpoint: %v", err)
	}
	bps, _ = s.ListDebugBreakpoints("aabb")
	if len(bps) != 0 {
		t.Errorf("expected 0 breakpoints after clear, got %d", len(bps))
	}
}

func TestDebugState(t *testing.T) {
	s := testStore(t)

	err := s.UpdateDebugState("aabb", "paused", "breakpoint", 7, "read", "sensor", 12)
	if err != nil {
		t.Fatalf("UpdateDebugState: %v", err)
	}

	ds, err := s.GetDebugState("aabb")
	if err != nil {
		t.Fatalf("GetDebugState: %v", err)
	}
	if ds.Status != "paused" {
		t.Errorf("Status = %q", ds.Status)
	}
	if ds.PauseReason != "breakpoint" {
		t.Errorf("PauseReason = %q", ds.PauseReason)
	}
	if ds.CurrentPC != 7 {
		t.Errorf("CurrentPC = %d", ds.CurrentPC)
	}
	if ds.CurrentFunction != "read" {
		t.Errorf("CurrentFunction = %q", ds.CurrentFunction)
	}
}

func TestDebugCommandQueue(t *testing.T) {
	s := testStore(t)

	err := s.QueueDebugCommand("aabb", "dbg:step")
	if err != nil {
		t.Fatalf("QueueDebugCommand: %v", err)
	}

	cmd, err := s.PopDebugCommand("aabb")
	if err != nil {
		t.Fatalf("PopDebugCommand: %v", err)
	}
	if cmd != "dbg:step" {
		t.Errorf("command = %q, want %q", cmd, "dbg:step")
	}

	cmd, err = s.PopDebugCommand("aabb")
	if err != nil {
		t.Fatalf("PopDebugCommand (empty): %v", err)
	}
	if cmd != "" {
		t.Errorf("expected empty, got %q", cmd)
	}
}
