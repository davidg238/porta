package handler

import (
	"errors"
	"testing"

	"github.com/davidg238/porta/internal/store"
	"github.com/davidg238/porta/internal/tftp"
)

func newH(t *testing.T) (*Handler, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/h.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	clock := int64(1000)
	return New(st, func() int64 { return clock }), st
}

func TestParseResource(t *testing.T) {
	base, params := parseResource("payload?id=aabb&crc=12345")
	if base != "payload" || params["id"] != "aabb" || params["crc"] != "12345" {
		t.Errorf("got %q %v", base, params)
	}
	base, params = parseResource("commands?id=")
	if base != "commands" || params["id"] != "" {
		t.Errorf("bare value: %q %v", base, params)
	}
	base, params = parseResource("report")
	if base != "report" || len(params) != 0 {
		t.Errorf("no query: %q %v", base, params)
	}
}

func TestReadCommandsDrainIsEmptyNotError(t *testing.T) {
	h, st := newH(t)
	st.TouchNode("dev", "p", 1000)
	data, err := h.Read("commands?id=dev", "p:1")
	if err != nil {
		t.Fatalf("empty queue must be (nil,nil), got err %v", err)
	}
	if len(data) != 0 {
		t.Errorf("empty queue body = %q, want empty", data)
	}
}

func TestReadCommandsServesFlatCommand(t *testing.T) {
	h, st := newH(t)
	st.TouchNode("dev", "p", 1000)
	st.EnqueueCommand("dev", "run", `{"name":"blink","crc":7}`, "cli", 1000)
	data, err := h.Read("commands?id=dev", "p:1")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"crc":7,"name":"blink","verb":"run"}` {
		t.Errorf("served %q", data)
	}
}

func TestDrainVsErrorNeverCollapse(t *testing.T) {
	h, st := newH(t)
	st.TouchNode("dev", "p", 1000)

	data, err := h.Read("commands?id=dev", "p")
	if err != nil || len(data) != 0 {
		t.Fatalf("empty queue: got (%q, %v), want (empty, nil)", data, err)
	}

	st.Close() // subsequent queries fail
	_, err = h.Read("commands?id=dev", "p")
	if err == nil {
		t.Fatal("store error must surface as a non-nil error, not an empty body")
	}
	_ = errors.Is // keep import meaningful if refactored
}

func TestReadPayloadRawBytesAndNotFound(t *testing.T) {
	h, st := newH(t)
	st.RegisterPayload(12345, "blink", []byte{1, 2, 3})
	data, err := h.Read("payload?id=dev&crc=12345", "p")
	if err != nil || string(data) != string([]byte{1, 2, 3}) {
		t.Errorf("payload = %q, %v", data, err)
	}
	if _, err := h.Read("payload?id=dev&crc=99999", "p"); err == nil {
		t.Error("missing crc must error (→ file not found)")
	}
	if _, err := h.Read("payload?id=dev&crc=notanint", "p"); err == nil {
		t.Error("bad crc must error")
	}
}

func TestReadUnknownResourceErrors(t *testing.T) {
	h, _ := newH(t)
	if _, err := h.Read("nonsense?id=dev", "p"); err == nil {
		t.Error("unknown RRQ resource must error")
	}
}

func TestReadTouchesNode(t *testing.T) {
	h, st := newH(t)
	if _, err := h.Read("commands?id=newdev", "1.2.3.4:9"); err != nil {
		t.Fatal(err)
	}
	n, _ := st.GetNode("newdev")
	if n == nil || !n.LastSeen.Valid {
		t.Error("RRQ with ?id= must touch the node")
	}
	if n.SourceAddr != "1.2.3.4:9" {
		t.Errorf("source_addr = %q, want recorded from peer", n.SourceAddr)
	}
}

func TestWriteReportIngest(t *testing.T) {
	h, st := newH(t)
	if err := h.AcceptWrite("report?id=dev", "p"); err != nil {
		t.Fatalf("report WRQ should be accepted: %v", err)
	}
	body := `{"apps":{"blink":{"crc":7}},"config":{"blink":{"k":1}},"health":{"uptime":42}}`
	if err := h.Write("report?id=dev", "p", []byte(body)); err != nil {
		t.Fatal(err)
	}
	n, _ := st.GetNode("dev")
	if n == nil || n.ObservedState != `{"apps":{"blink":{"crc":7}},"config":{"blink":{"k":1}}}` {
		t.Errorf("observed_state = %q", n.ObservedState)
	}
}

func TestWriteRejectsNonReportAndMissingID(t *testing.T) {
	h, _ := newH(t)
	if err := h.AcceptWrite("data?id=dev", "p"); err == nil {
		t.Error("data WRQ deferred to B3 → must be rejected in B1")
	}
	if err := h.AcceptWrite("report", "p"); err == nil {
		t.Error("report without ?id= must be rejected")
	}
}

func TestCompleteMarksDelivered(t *testing.T) {
	h, st := newH(t)
	st.TouchNode("dev", "p", 1000)
	id, _ := st.EnqueueCommand("dev", "run", `{"name":"x"}`, "cli", 1000)
	h.Read("commands?id=dev", "p")
	c, _ := st.NextUndelivered("dev")
	if c == nil || c.ID != id {
		t.Fatal("Read must not mark delivered")
	}
	h.Complete(tftp.OpRRQ, "commands?id=dev", "p", true)
	if c, _ := st.NextUndelivered("dev"); c != nil {
		t.Error("Complete(RRQ, commands) must mark delivered")
	}
}

func TestCompletePayloadDoesNotMark(t *testing.T) {
	h, st := newH(t)
	st.TouchNode("dev", "p", 1000)
	st.EnqueueCommand("dev", "run", `{"name":"x"}`, "cli", 1000)
	h.Complete(tftp.OpRRQ, "payload?id=dev&crc=1", "p", true)
	if c, _ := st.NextUndelivered("dev"); c == nil {
		t.Error("completing a payload transfer must NOT mark a command delivered")
	}
}
