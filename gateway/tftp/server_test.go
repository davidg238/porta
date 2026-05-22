package tftp

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"testing"
)

func TestServerRRQSingleBlock(t *testing.T) {
	s := NewServer()
	s.RegisterGet("/commands", func() []byte {
		return []byte("hello")
	})

	rrq := BuildRRQ("/commands", 0)
	replies := s.HandlePacket(rrq)
	if len(replies) != 1 {
		t.Fatalf("expected 1 reply, got %d", len(replies))
	}
	op, _ := ParseOpcode(replies[0])
	if op != OpDATA {
		t.Fatalf("expected DATA opcode, got %d", op)
	}
	block, data, _ := ParseData(replies[0])
	if block != 1 {
		t.Fatalf("expected block 1, got %d", block)
	}
	if !bytes.Equal(data, []byte("hello")) {
		t.Fatalf("expected hello, got %q", data)
	}
}

func TestServerRRQWithBlksize(t *testing.T) {
	s := NewServer()
	s.RegisterGet("/commands", func() []byte {
		return []byte("hi")
	})

	rrq := BuildRRQ("/commands", 64)
	replies := s.HandlePacket(rrq)
	if len(replies) != 1 {
		t.Fatalf("expected 1 reply (OACK), got %d", len(replies))
	}
	op, _ := ParseOpcode(replies[0])
	if op != OpOACK {
		t.Fatalf("expected OACK, got %d", op)
	}

	// Client sends ACK 0 to confirm OACK
	ack0 := BuildACK(0)
	replies = s.HandlePacket(ack0)
	if len(replies) != 1 {
		t.Fatalf("expected 1 reply (DATA), got %d", len(replies))
	}
	op, _ = ParseOpcode(replies[0])
	if op != OpDATA {
		t.Fatalf("expected DATA, got %d", op)
	}
	block, data, _ := ParseData(replies[0])
	if block != 1 {
		t.Fatalf("expected block 1, got %d", block)
	}
	if !bytes.Equal(data, []byte("hi")) {
		t.Fatalf("expected hi, got %q", data)
	}
}

func TestServerRRQMultiBlock(t *testing.T) {
	s := NewServer()
	payload := make([]byte, 150)
	for i := range payload {
		payload[i] = byte(i)
	}
	s.RegisterGet("/firmware", func() []byte {
		return payload
	})

	rrq := BuildRRQ("/firmware", 64)
	replies := s.HandlePacket(rrq)
	if len(replies) != 1 {
		t.Fatalf("expected OACK, got %d replies", len(replies))
	}

	// ACK 0 → DATA block 1 (64 bytes)
	replies = s.HandlePacket(BuildACK(0))
	if len(replies) != 1 {
		t.Fatalf("expected DATA block 1, got %d replies", len(replies))
	}
	_, d1, _ := ParseData(replies[0])
	if len(d1) != 64 {
		t.Fatalf("block 1: expected 64 bytes, got %d", len(d1))
	}

	// ACK 1 → DATA block 2 (64 bytes)
	replies = s.HandlePacket(BuildACK(1))
	_, d2, _ := ParseData(replies[0])
	if len(d2) != 64 {
		t.Fatalf("block 2: expected 64 bytes, got %d", len(d2))
	}

	// ACK 2 → DATA block 3 (22 bytes, final)
	replies = s.HandlePacket(BuildACK(2))
	_, d3, _ := ParseData(replies[0])
	if len(d3) != 22 {
		t.Fatalf("block 3: expected 22 bytes, got %d", len(d3))
	}

	// Reconstruct
	var got []byte
	got = append(got, d1...)
	got = append(got, d2...)
	got = append(got, d3...)
	if !bytes.Equal(got, payload) {
		t.Fatal("reconstructed data mismatch")
	}
}

func TestServerRRQUnknownPath(t *testing.T) {
	s := NewServer()

	rrq := BuildRRQ("/nonexistent", 0)
	replies := s.HandlePacket(rrq)
	if len(replies) != 1 {
		t.Fatalf("expected 1 reply, got %d", len(replies))
	}
	op, _ := ParseOpcode(replies[0])
	if op != OpERROR {
		t.Fatalf("expected ERROR, got %d", op)
	}
	code := binary.BigEndian.Uint16(replies[0][2:4])
	if code != 1 {
		t.Fatalf("expected error code 1 (file not found), got %d", code)
	}
}

func TestServerWRQSingleBlock(t *testing.T) {
	s := NewServer()
	var gotPath string
	var gotData []byte
	s.RegisterPut("/results", func(path string, data []byte) {
		gotPath = path
		gotData = make([]byte, len(data))
		copy(gotData, data)
	})

	wrq := BuildWRQ("/results", 0)
	replies := s.HandlePacket(wrq)
	if len(replies) != 1 {
		t.Fatalf("expected 1 reply (ACK 0), got %d", len(replies))
	}
	op, _ := ParseOpcode(replies[0])
	if op != OpACK {
		t.Fatalf("expected ACK, got %d", op)
	}
	block, _ := ParseACK(replies[0])
	if block != 0 {
		t.Fatalf("expected ACK 0, got ACK %d", block)
	}

	// Send DATA block 1 with < DefaultBlockSize bytes (final)
	data := BuildData(1, []byte("result data"))
	replies = s.HandlePacket(data)
	if len(replies) != 1 {
		t.Fatalf("expected 1 reply (ACK 1), got %d", len(replies))
	}
	block, _ = ParseACK(replies[0])
	if block != 1 {
		t.Fatalf("expected ACK 1, got ACK %d", block)
	}
	if gotPath != "/results" {
		t.Fatalf("handler path: got %q", gotPath)
	}
	if !bytes.Equal(gotData, []byte("result data")) {
		t.Fatalf("handler data: got %q", gotData)
	}
}

func TestServerWRQWithBlksize(t *testing.T) {
	s := NewServer()
	var gotData []byte
	s.RegisterPut("/results", func(path string, data []byte) {
		gotData = make([]byte, len(data))
		copy(gotData, data)
	})

	wrq := BuildWRQ("/results", 64)
	replies := s.HandlePacket(wrq)
	if len(replies) != 1 {
		t.Fatalf("expected OACK, got %d replies", len(replies))
	}
	op, _ := ParseOpcode(replies[0])
	if op != OpOACK {
		t.Fatalf("expected OACK, got %d", op)
	}

	// Send DATA block 1 (short block = final)
	data := BuildData(1, []byte("done"))
	replies = s.HandlePacket(data)
	if len(replies) != 1 {
		t.Fatalf("expected ACK, got %d replies", len(replies))
	}
	block, _ := ParseACK(replies[0])
	if block != 1 {
		t.Fatalf("expected ACK 1, got ACK %d", block)
	}
	if !bytes.Equal(gotData, []byte("done")) {
		t.Fatalf("handler data: got %q", gotData)
	}
}

func TestServerWRQMultiBlock(t *testing.T) {
	s := NewServer()
	var handlerCalled int
	var gotData []byte
	s.RegisterPut("/results", func(path string, data []byte) {
		handlerCalled++
		gotData = make([]byte, len(data))
		copy(gotData, data)
	})

	wrq := BuildWRQ("/results", 64)
	s.HandlePacket(wrq) // OACK

	// Block 1: 64 bytes (full)
	block1 := make([]byte, 64)
	for i := range block1 {
		block1[i] = byte(i)
	}
	replies := s.HandlePacket(BuildData(1, block1))
	b, _ := ParseACK(replies[0])
	if b != 1 {
		t.Fatalf("expected ACK 1, got %d", b)
	}
	if handlerCalled != 0 {
		t.Fatal("handler should NOT be called until final block")
	}

	// Block 2: 3 bytes (short = final)
	block2 := []byte{0xAA, 0xBB, 0xCC}
	replies = s.HandlePacket(BuildData(2, block2))
	b, _ = ParseACK(replies[0])
	if b != 2 {
		t.Fatalf("expected ACK 2, got %d", b)
	}
	if handlerCalled != 1 {
		t.Fatalf("expected handler called once, got %d", handlerCalled)
	}
	if len(gotData) != 67 {
		t.Fatalf("expected 67 bytes, got %d", len(gotData))
	}

	// Verify data integrity
	expected := append(block1, block2...)
	if !bytes.Equal(gotData, expected) {
		t.Fatal("data mismatch")
	}
}

func TestServerCommandQueue(t *testing.T) {
	s := NewServer()

	// Queue two commands for device A
	s.QueueCommand("devA", "eval", []byte{1, 2})
	s.QueueCommand("devA", "reboot", nil)
	// Queue one for device B
	s.QueueCommand("devB", "eval", []byte{3})

	// Pop device A commands in order
	cmd := s.PopCommand("devA")
	if cmd == nil || cmd.Verb != "eval" {
		t.Fatal("expected eval command for devA")
	}
	if !bytes.Equal(cmd.Payload, []byte{1, 2}) {
		t.Fatal("wrong payload")
	}

	cmd = s.PopCommand("devA")
	if cmd == nil || cmd.Verb != "reboot" {
		t.Fatal("expected reboot command for devA")
	}

	cmd = s.PopCommand("devA")
	if cmd != nil {
		t.Fatal("expected nil when queue empty")
	}

	// Device B independent
	cmd = s.PopCommand("devB")
	if cmd == nil || cmd.Verb != "eval" {
		t.Fatal("expected eval command for devB")
	}
	if !bytes.Equal(cmd.Payload, []byte{3}) {
		t.Fatal("wrong payload for devB")
	}

	cmd = s.PopCommand("devB")
	if cmd != nil {
		t.Fatal("expected nil for empty devB")
	}

	// Unknown device
	cmd = s.PopCommand("devC")
	if cmd != nil {
		t.Fatal("expected nil for unknown device")
	}
}

func TestCommandToJSON(t *testing.T) {
	cmd := &Command{Verb: "eval", Payload: []byte{0xDE, 0xAD}}
	j := CommandToJSON(cmd)

	var m map[string]string
	if err := json.Unmarshal(j, &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if m["verb"] != "eval" {
		t.Fatalf("verb: got %q", m["verb"])
	}
	if m["payload"] != hex.EncodeToString([]byte{0xDE, 0xAD}) {
		t.Fatalf("payload: got %q", m["payload"])
	}
}
