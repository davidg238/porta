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
	if err := h.AcceptWrite("bogus?id=dev", "p:1"); err == nil {
		t.Error("unknown base must be rejected")
	}
	if err := h.AcceptWrite("report", "p:1"); err == nil {
		t.Error("report without id must be rejected")
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

func TestWriteReconcileReissuesOnDrift(t *testing.T) {
	h, st := newH(t)
	st.EnsureNode("dev", 1000)
	// Operator sets a.k=30; deliver it.
	cmdID, _ := st.EnqueueCommand("dev", "set", `{"app":"a","key":"k","value":30}`, "cli", 1100)
	if err := st.MarkDelivered(cmdID, 1101); err != nil {
		t.Fatal(err)
	}
	// Node reports a.k=25 (drift).
	body := []byte(`{"apps":{},"config":{"a":{"k":25}},"health":{}}`)
	if err := h.Write("report?id=dev", "1.2.3.4:5000", body); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// A gateway-reconcile re-issue should now be the next undelivered.
	next, err := st.NextUndelivered("dev")
	if err != nil || next == nil {
		t.Fatalf("NextUndelivered: %v %v", next, err)
	}
	if next.IssuedBy != "gateway-reconcile" {
		t.Errorf("issued_by = %q, want gateway-reconcile", next.IssuedBy)
	}
	if next.Args != `{"app":"a","key":"k","value":30}` {
		t.Errorf("re-issue Args = %s, want byte-identical to source", next.Args)
	}
}

func TestWriteReconcileSelfThrottle(t *testing.T) {
	h, st := newH(t)
	st.EnsureNode("dev", 1000)
	cmdID, _ := st.EnqueueCommand("dev", "set", `{"app":"a","key":"k","value":30}`, "cli", 1100)
	st.MarkDelivered(cmdID, 1101)
	body := []byte(`{"apps":{},"config":{"a":{"k":25}},"health":{}}`)
	if err := h.Write("report?id=dev", "p:1", body); err != nil {
		t.Fatal(err)
	}
	// Second drifted report — re-issue from the first one is still pending
	// (delivered_at NULL), so reconcile MUST NOT issue another.
	if err := h.Write("report?id=dev", "p:1", body); err != nil {
		t.Fatal(err)
	}
	log, _ := st.CommandLog("dev")
	reissues := 0
	for _, c := range log {
		if c.IssuedBy == "gateway-reconcile" {
			reissues++
		}
	}
	if reissues != 1 {
		t.Errorf("got %d gateway-reconcile rows, want 1 (self-throttle)", reissues)
	}
}

func TestWriteReconcileSecondCycleAfterDelivery(t *testing.T) {
	h, st := newH(t)
	st.EnsureNode("dev", 1000)
	cmdID, _ := st.EnqueueCommand("dev", "set", `{"app":"a","key":"k","value":30}`, "cli", 1100)
	st.MarkDelivered(cmdID, 1101)
	body := []byte(`{"apps":{},"config":{"a":{"k":25}},"health":{}}`)
	// First report → 1 re-issue.
	if err := h.Write("report?id=dev", "p:1", body); err != nil {
		t.Fatalf("Write 1: %v", err)
	}
	// Mark the re-issue delivered, simulating the node fetching it.
	un, _ := st.UndeliveredCommands("dev")
	if len(un) != 1 {
		t.Fatalf("expected 1 undelivered re-issue, got %d", len(un))
	}
	st.MarkDelivered(un[0].ID, 1200)
	// Second drifted report → second re-issue is allowed (in-flight guard cleared).
	if err := h.Write("report?id=dev", "p:1", body); err != nil {
		t.Fatalf("Write 2: %v", err)
	}
	log, _ := st.CommandLog("dev")
	reissues := 0
	for _, c := range log {
		if c.IssuedBy == "gateway-reconcile" {
			reissues++
		}
	}
	if reissues != 2 {
		t.Errorf("got %d gateway-reconcile rows, want 2", reissues)
	}
}

func TestWriteReconcilePending(t *testing.T) {
	h, st := newH(t)
	st.EnsureNode("dev", 1000)
	cmdID, _ := st.EnqueueCommand("dev", "set", `{"app":"a","key":"k","value":30}`, "cli", 1100)
	st.MarkDelivered(cmdID, 1101)
	// Report says config has app a but not the key k (delivered but lost).
	body := []byte(`{"apps":{},"config":{"a":{}},"health":{}}`)
	if err := h.Write("report?id=dev", "p:1", body); err != nil {
		t.Fatalf("Write: %v", err)
	}
	un, _ := st.UndeliveredCommands("dev")
	if len(un) != 1 || un[0].IssuedBy != "gateway-reconcile" {
		t.Fatalf("pending key not re-issued: %+v", un)
	}
}

// TestWriteReconcileNullConfigNoReissues guards against the config:null trap.
// `null` is wire-legal JSON; decoded into map[string]map[string]any it yields
// a NIL map, which a naive implementation would treat as an empty observed
// and re-issue every desired key on every report (storm). The handler must
// detect nil observed and skip reconcile entirely (parity with "missing config").
func TestWriteReconcileNullConfigNoReissues(t *testing.T) {
	h, st := newH(t)
	st.EnsureNode("dev", 1000)
	cmdID, _ := st.EnqueueCommand("dev", "set", `{"app":"a","key":"k","value":30}`, "cli", 1100)
	st.MarkDelivered(cmdID, 1101)
	body := []byte(`{"apps":{},"config":null,"health":{}}`)
	if err := h.Write("report?id=dev", "p:1", body); err != nil {
		t.Fatalf("Write: %v", err)
	}
	un, _ := st.UndeliveredCommands("dev")
	if len(un) != 0 {
		t.Errorf("config:null should not trigger re-issues, got %d", len(un))
	}
}

func TestWriteSucceedsEvenWithMalformedConfig(t *testing.T) {
	h, st := newH(t)
	st.EnsureNode("dev", 1000)
	// config field is a string, not an object — reconcile must not fail the write.
	body := []byte(`{"apps":{},"config":"oops","health":{}}`)
	if err := h.Write("report?id=dev", "p:1", body); err != nil {
		t.Fatalf("Write should swallow reconcile errors, got %v", err)
	}
	// Report row was still committed.
	n, _ := st.GetNode("dev")
	if n == nil || n.ObservedState == "" {
		t.Error("observed_state should be set even when reconcile bails")
	}
}

func TestWriteAcceptsDataAndIngestsJSONL(t *testing.T) {
	h, st := newH(t)
	st.EnsureNode("aabbccddeeff", 1000)
	body := []byte(
		`{"ts":100,"seq":0,"kind":"metric","name":"pm","value":13}` + "\n" +
			`{"ts":101,"seq":1,"kind":"metric","name":"t","value":20.5}` + "\n" +
			`{"ts":102,"seq":2,"kind":"metric","name":"door","value":true}` + "\n" +
			`{"ts":103,"seq":3,"kind":"metric","name":"mode","value":"auto"}` + "\n" +
			`{"ts":104,"seq":4,"kind":"log","text":"started blink"}` + "\n")
	if err := h.AcceptWrite("data?id=aabbccddeeff", "p:1"); err != nil {
		t.Fatalf("AcceptWrite: %v", err)
	}
	if err := h.Write("data?id=aabbccddeeff", "p:1", body); err != nil {
		t.Fatalf("Write: %v", err)
	}
	rows, _ := st.QueryData("aabbccddeeff", 0, 200, "")
	if len(rows) != 5 {
		t.Fatalf("got %d rows, want 5", len(rows))
	}
	if rows[0].ValueType != "int" {
		t.Errorf("rows[0].ValueType=%q, want int", rows[0].ValueType)
	}
	if rows[1].ValueType != "float" {
		t.Errorf("rows[1].ValueType=%q, want float", rows[1].ValueType)
	}
	if rows[2].ValueType != "bool" {
		t.Errorf("rows[2].ValueType=%q, want bool", rows[2].ValueType)
	}
	if rows[3].ValueType != "string" {
		t.Errorf("rows[3].ValueType=%q, want string", rows[3].ValueType)
	}
	if rows[3].Text != "auto" {
		t.Errorf("rows[3].Text=%q, want auto", rows[3].Text)
	}
	if rows[4].Kind != "log" || rows[4].Text != "started blink" {
		t.Errorf("rows[4]=%+v", rows[4])
	}
}

func TestWriteDataTruncatedTailToleratesSkip(t *testing.T) {
	h, st := newH(t)
	st.EnsureNode("ffeeddccbbaa", 1000)
	body := []byte(
		`{"ts":100,"kind":"log","text":"a"}` + "\n" +
			`{"ts":101,"kind":"log","text":"b"}` + "\n" +
			`{"ts":102,"kind":"met` /* truncated */)
	if err := h.Write("data?id=ffeeddccbbaa", "p:1", body); err != nil {
		t.Fatalf("Write: %v", err)
	}
	rows, _ := st.QueryData("ffeeddccbbaa", 0, 200, "")
	if len(rows) != 2 {
		t.Errorf("got %d rows, want 2 (truncated tail must be skipped)", len(rows))
	}
}

func TestWriteDataNonObjectLineSkipped(t *testing.T) {
	h, st := newH(t)
	st.EnsureNode("112233445566", 1000)
	body := []byte(
		`{"ts":100,"kind":"log","text":"a"}` + "\n" +
			`42` + "\n" +
			`{"ts":101,"kind":"log","text":"b"}` + "\n")
	if err := h.Write("data?id=112233445566", "p:1", body); err != nil {
		t.Fatalf("Write: %v", err)
	}
	rows, _ := st.QueryData("112233445566", 0, 200, "")
	if len(rows) != 2 {
		t.Errorf("got %d rows, want 2 (non-object line skipped)", len(rows))
	}
}

func TestWriteDataSeqFallbackCountsAcceptedRows(t *testing.T) {
	h, st := newH(t)
	st.EnsureNode("abcdefabcdef", 1000)
	// Leading blank + middle non-object: the two accepted rows must get
	// seq 0 and 1 (success-counter), not the raw split index (1 and 3).
	body := []byte("\n" +
		`{"kind":"log","text":"a"}` + "\n" +
		`42` + "\n" +
		`{"kind":"log","text":"b"}` + "\n")
	if err := h.Write("data?id=abcdefabcdef", "p:1", body); err != nil {
		t.Fatalf("Write: %v", err)
	}
	rows, _ := st.QueryData("abcdefabcdef", 0, 2000, "")
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if rows[0].Text != "a" || rows[0].Seq != 0 {
		t.Errorf("rows[0]={Text:%q Seq:%d}, want {a 0}", rows[0].Text, rows[0].Seq)
	}
	if rows[1].Text != "b" || rows[1].Seq != 1 {
		t.Errorf("rows[1]={Text:%q Seq:%d}, want {b 1}", rows[1].Text, rows[1].Seq)
	}
}

func TestWriteDataNonScalarValueDegrades(t *testing.T) {
	h, st := newH(t)
	st.EnsureNode("aaaa11112222", 1000)
	body := []byte(`{"ts":300,"kind":"metric","name":"x","value":[1,2]}` + "\n")
	if err := h.Write("data?id=aaaa11112222", "p:1", body); err != nil {
		t.Fatalf("Write: %v", err)
	}
	rows, _ := st.QueryData("aaaa11112222", 0, 400, "")
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].Value != nil {
		t.Errorf("Value=%v, want nil (degraded)", rows[0].Value)
	}
	if rows[0].ValueType != "" {
		t.Errorf("ValueType=%q, want \"\" (degraded)", rows[0].ValueType)
	}
}

func TestAcceptWriteRejectsDataWithoutID(t *testing.T) {
	h, _ := newH(t)
	if err := h.AcceptWrite("data", "p:1"); err == nil {
		t.Error("AcceptWrite(data) without id must error")
	}
	if err := h.AcceptWrite("data?id=", "p:1"); err == nil {
		t.Error("AcceptWrite(data?id=) (empty id) must error")
	}
}
