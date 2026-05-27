package gateway

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/davidg238/porta/internal/store"
	"github.com/davidg238/porta/internal/tftp"
)

// testGateway opens an in-memory store, creates a Gateway, and registers cleanup.
func testGateway(t *testing.T) *Gateway {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return New(st)
}

func zeroTime() time.Time  { return time.Time{} }
func futureTime() time.Time { return time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC) }

func TestDevicePollRegistersDevice(t *testing.T) {
	gw := testGateway(t)
	deviceID := "aabbccddeeff1122"

	pkt := tftp.BuildRRQ("/commands?id="+deviceID, 64)
	gw.HandleDevicePacket(pkt, "10.0.0.1:5683")

	devs, err := gw.Store.ListDevices()
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(devs) != 1 {
		t.Fatalf("expected 1 device, got %d", len(devs))
	}
	if devs[0].EUI64 != deviceID {
		t.Errorf("expected EUI64 %q, got %q", deviceID, devs[0].EUI64)
	}
}

func TestDevicePollGetsCommand(t *testing.T) {
	gw := testGateway(t)
	deviceID := "aabbccddeeff1122"

	// Queue a command in the store.
	if err := gw.Store.QueueCommand(deviceID, "status", nil); err != nil {
		t.Fatalf("QueueCommand: %v", err)
	}

	// Step 1: RRQ with blksize — expect OACK.
	rrq := tftp.BuildRRQ("/commands?id="+deviceID, 64)
	resp := gw.HandleDevicePacket(rrq, "10.0.0.1:5683")
	if len(resp) != 1 {
		t.Fatalf("expected 1 response (OACK), got %d", len(resp))
	}
	op, _ := tftp.ParseOpcode(resp[0])
	if op != tftp.OpOACK {
		t.Fatalf("expected OACK (opcode %d), got %d", tftp.OpOACK, op)
	}

	// Step 2: ACK 0 — expect DATA block 1 with command JSON.
	ack0 := tftp.BuildACK(0)
	resp = gw.HandleDevicePacket(ack0, "10.0.0.1:5683")
	if len(resp) != 1 {
		t.Fatalf("expected 1 response (DATA), got %d", len(resp))
	}
	op, _ = tftp.ParseOpcode(resp[0])
	if op != tftp.OpDATA {
		t.Fatalf("expected DATA (opcode %d), got %d", tftp.OpDATA, op)
	}

	// Verify JSON payload.
	_, data, _ := tftp.ParseData(resp[0])
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal JSON: %v", err)
	}
	if m["verb"] != "status" {
		t.Errorf("expected verb %q, got %q", "status", m["verb"])
	}
	if m["payload"] != "" {
		t.Errorf("expected empty payload, got %q", m["payload"])
	}
}

func TestDevicePollEmptyQueue(t *testing.T) {
	gw := testGateway(t)
	deviceID := "aabbccddeeff1122"

	// Step 1: RRQ with blksize — expect OACK.
	rrq := tftp.BuildRRQ("/commands?id="+deviceID, 64)
	resp := gw.HandleDevicePacket(rrq, "10.0.0.1:5683")
	if len(resp) != 1 {
		t.Fatalf("expected 1 response (OACK), got %d", len(resp))
	}
	op, _ := tftp.ParseOpcode(resp[0])
	if op != tftp.OpOACK {
		t.Fatalf("expected OACK, got opcode %d", op)
	}

	// Step 2: ACK 0 — expect DATA block 1 with empty payload.
	ack0 := tftp.BuildACK(0)
	resp = gw.HandleDevicePacket(ack0, "10.0.0.1:5683")
	if len(resp) != 1 {
		t.Fatalf("expected 1 response (DATA), got %d", len(resp))
	}
	op, _ = tftp.ParseOpcode(resp[0])
	if op != tftp.OpDATA {
		t.Fatalf("expected DATA, got opcode %d", op)
	}

	_, data, _ := tftp.ParseData(resp[0])
	if len(data) != 0 {
		t.Errorf("expected empty data, got %d bytes: %q", len(data), data)
	}
}

func TestDevicePutsResults(t *testing.T) {
	gw := testGateway(t)
	deviceID := "aabb"

	// Step 1: WRQ with blksize — expect OACK.
	wrq := tftp.BuildWRQ("/results?id="+deviceID, 64)
	resp := gw.HandleDevicePacket(wrq, "10.0.0.1:5683")
	if len(resp) != 1 {
		t.Fatalf("expected 1 response (OACK), got %d", len(resp))
	}
	op, _ := tftp.ParseOpcode(resp[0])
	if op != tftp.OpOACK {
		t.Fatalf("expected OACK, got opcode %d", op)
	}

	// Step 2: DATA block 1 with result payload (shorter than blksize = final).
	payload := []byte("temp=22.3")
	dataPkt := tftp.BuildData(1, payload)
	resp = gw.HandleDevicePacket(dataPkt, "10.0.0.1:5683")
	if len(resp) != 1 {
		t.Fatalf("expected 1 response (ACK), got %d", len(resp))
	}
	op, _ = tftp.ParseOpcode(resp[0])
	if op != tftp.OpACK {
		t.Fatalf("expected ACK, got opcode %d", op)
	}

	// Verify data appears in store.
	rows, err := gw.Store.QueryData(deviceID, zeroTime(), futureTime())
	if err != nil {
		t.Fatalf("QueryData: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 data row, got %d", len(rows))
	}
	if string(rows[0].Payload) != "temp=22.3" {
		t.Errorf("expected payload %q, got %q", "temp=22.3", string(rows[0].Payload))
	}
}

func TestDebugPathRouting(t *testing.T) {
	gw := testGateway(t)
	deviceID := "aabbccddeeff0011"

	_ = gw.Store.DeviceSeen(deviceID, "fd00::1", "child", 0x4001)
	_ = gw.Store.QueueDebugCommand(deviceID, "dbg:step")

	// Step 1: RRQ for /debug?id=... — expect OACK.
	rrq := tftp.BuildRRQ("/debug?id="+deviceID, 64)
	resp := gw.HandleDevicePacket(rrq, "fd00::1")
	if len(resp) != 1 {
		t.Fatalf("expected 1 response (OACK), got %d", len(resp))
	}
	op, _ := tftp.ParseOpcode(resp[0])
	if op != tftp.OpOACK {
		t.Fatalf("expected OACK, got opcode %d", op)
	}

	// Step 2: ACK 0 — expect DATA with debug command.
	ack0 := tftp.BuildACK(0)
	resp = gw.HandleDevicePacket(ack0, "fd00::1")
	if len(resp) != 1 {
		t.Fatalf("expected 1 response (DATA), got %d", len(resp))
	}
	_, data, _ := tftp.ParseData(resp[0])
	if string(data) != "dbg:step" {
		t.Errorf("debug command = %q, want %q", string(data), "dbg:step")
	}
}

func TestDebugResultParsing(t *testing.T) {
	gw := testGateway(t)
	deviceID := "aabb"

	_ = gw.Store.DeviceSeen(deviceID, "fd00::1", "child", 0x4001)

	// Step 1: WRQ for /debug_result?id=... — expect OACK.
	wrq := tftp.BuildWRQ("/debug_result?id="+deviceID, 64)
	resp := gw.HandleDevicePacket(wrq, "fd00::1")
	if len(resp) != 1 {
		t.Fatalf("expected 1 response (OACK), got %d", len(resp))
	}

	// Step 2: Send "dbg:paused breakpoint 7 12 read"
	payload := []byte("dbg:paused breakpoint 7 12 read")
	dataPkt := tftp.BuildData(1, payload)
	resp = gw.HandleDevicePacket(dataPkt, "fd00::1")
	if len(resp) != 1 {
		t.Fatalf("expected 1 response (ACK), got %d", len(resp))
	}

	// Verify debug state was updated.
	ds, err := gw.Store.GetDebugState(deviceID)
	if err != nil {
		t.Fatalf("GetDebugState: %v", err)
	}
	if ds.Status != "paused" {
		t.Errorf("Status = %q, want paused", ds.Status)
	}
	if ds.PauseReason != "breakpoint" {
		t.Errorf("PauseReason = %q, want breakpoint", ds.PauseReason)
	}
	if ds.CurrentPC != 7 {
		t.Errorf("CurrentPC = %d, want 7", ds.CurrentPC)
	}
	if ds.CurrentFunction != "read" {
		t.Errorf("CurrentFunction = %q, want read", ds.CurrentFunction)
	}
}
