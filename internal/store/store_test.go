package store

import (
	"testing"
)

func openTmp(t *testing.T) *Store {
	t.Helper()
	st, err := Open(t.TempDir() + "/porta.db")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestTouchNodeCreatesThenUpdates(t *testing.T) {
	st := openTmp(t)
	if err := st.TouchNode("aabbccddeeff", "192.0.2.1:5000", 1000); err != nil {
		t.Fatal(err)
	}
	n, err := st.GetNode("aabbccddeeff")
	if err != nil || n == nil {
		t.Fatalf("GetNode: %v %v", n, err)
	}
	if n.Name == "" {
		t.Error("expected auto-assigned name")
	}
	if !n.LastSeen.Valid || n.LastSeen.Int64 != 1000 {
		t.Errorf("last_seen = %v, want 1000", n.LastSeen)
	}
	if n.Kind != "toit" {
		t.Errorf("kind = %q, want toit", n.Kind)
	}
	if n.PollIntervalS != 30 || n.MaxOfflineS != 300 {
		t.Errorf("defaults wrong: poll=%d max=%d", n.PollIntervalS, n.MaxOfflineS)
	}
	if err := st.TouchNode("aabbccddeeff", "", 2000); err != nil {
		t.Fatal(err)
	}
	n, _ = st.GetNode("aabbccddeeff")
	if n.LastSeen.Int64 != 2000 {
		t.Errorf("last_seen = %d, want 2000", n.LastSeen.Int64)
	}
	if n.SourceAddr != "192.0.2.1:5000" {
		t.Errorf("source_addr = %q, want preserved", n.SourceAddr)
	}
}

func TestEnsureNodeNoLastSeen(t *testing.T) {
	st := openTmp(t)
	if err := st.EnsureNode("001122334455", 500); err != nil {
		t.Fatal(err)
	}
	n, _ := st.GetNode("001122334455")
	if n == nil {
		t.Fatal("node not created")
	}
	if n.LastSeen.Valid {
		t.Error("ensure must not set last_seen")
	}
	st.TouchNode("001122334455", "x", 600)
	st.EnsureNode("001122334455", 700)
	n, _ = st.GetNode("001122334455")
	if !n.LastSeen.Valid || n.LastSeen.Int64 != 600 {
		t.Errorf("ensure clobbered last_seen: %v", n.LastSeen)
	}
}

func TestPayloadRegisterFetch(t *testing.T) {
	st := openTmp(t)
	img := []byte{1, 2, 3, 4, 5}
	if err := st.RegisterPayload(12345, "blink", img); err != nil {
		t.Fatal(err)
	}
	ok, _ := st.PayloadExists(12345)
	if !ok {
		t.Fatal("payload should exist")
	}
	got, err := st.Payload(12345)
	if err != nil || string(got) != string(img) {
		t.Errorf("Payload = %v %v", got, err)
	}
	missing, _ := st.Payload(99999)
	if missing != nil {
		t.Error("missing crc should return nil")
	}
	st.RegisterPayload(12345, "blink2", []byte{9})
	got, _ = st.Payload(12345)
	if string(got) != string([]byte{9}) {
		t.Error("re-register should replace")
	}
}

func TestCommandQueueFIFOAndDeliver(t *testing.T) {
	st := openTmp(t)
	id1, err := st.EnqueueCommand("dev", "run", `{"name":"a"}`, "cli", 100)
	if err != nil {
		t.Fatal(err)
	}
	st.EnqueueCommand("dev", "stop", `{"name":"a"}`, "cli", 101)

	next, _ := st.NextUndelivered("dev")
	if next == nil || next.ID != id1 || next.Verb != "run" {
		t.Fatalf("FIFO wrong: %+v", next)
	}
	if err := st.MarkDelivered(next.ID, 200); err != nil {
		t.Fatal(err)
	}
	next, _ = st.NextUndelivered("dev")
	if next == nil || next.Verb != "stop" {
		t.Fatalf("after deliver, next should be stop: %+v", next)
	}
	un, _ := st.UndeliveredCommands("dev")
	if len(un) != 1 {
		t.Errorf("undelivered = %d, want 1", len(un))
	}
	log, _ := st.CommandLog("dev")
	if len(log) != 2 {
		t.Errorf("log = %d, want 2", len(log))
	}
	if !log[0].DeliveredAt.Valid || log[1].DeliveredAt.Valid {
		t.Error("delivery flags wrong in log")
	}
}

func TestInsertReportCachesObservedState(t *testing.T) {
	st := openTmp(t)
	st.TouchNode("dev", "x", 10)
	obs := `{"apps":{"blink":{"crc":7}},"config":{}}`
	if err := st.InsertReport("dev", obs, `{"uptime":42}`, 300); err != nil {
		t.Fatal(err)
	}
	n, _ := st.GetNode("dev")
	if n.ObservedState != obs {
		t.Errorf("observed_state = %q, want cached", n.ObservedState)
	}
	if !n.LastReportAt.Valid || n.LastReportAt.Int64 != 300 {
		t.Errorf("last_report_at = %v, want 300", n.LastReportAt)
	}
}

func TestNodeOnline(t *testing.T) {
	st := openTmp(t)
	st.TouchNode("dev", "x", 1000)
	n, _ := st.GetNode("dev")
	if !n.Online(1000 + 299) {
		t.Error("within max_offline should be online")
	}
	if n.Online(1000 + 301) {
		t.Error("past max_offline should be offline")
	}
	en := &Node{}
	if en.Online(123456) {
		t.Error("never-seen must be offline")
	}
}

func TestRecentCommandsCrossDeviceNewestFirst(t *testing.T) {
	st := openTmp(t)
	st.EnqueueCommand("n1", "set", `{"a":1}`, "cli", 100)
	st.EnqueueCommand("n2", "stop", `{}`, "web", 101)
	rows, err := st.RecentCommands(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].DeviceID != "n2" || rows[0].Verb != "stop" {
		t.Fatalf("want newest-first cross-device, got %+v", rows)
	}
}

func TestUpdateNodeIdentity(t *testing.T) {
	st := openTmp(t)
	if err := st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateNodeIdentity("aabbccddeeff", "esp32c6", "v2.0.0-alpha.192"); err != nil {
		t.Fatal(err)
	}
	n, err := st.GetNode("aabbccddeeff")
	if err != nil || n == nil {
		t.Fatalf("GetNode: %v / %v", n, err)
	}
	if n.Chip != "esp32c6" || n.Sdk != "v2.0.0-alpha.192" {
		t.Errorf("got chip=%q sdk=%q, want esp32c6 / v2.0.0-alpha.192", n.Chip, n.Sdk)
	}
	// Empty values must not clobber a known identity.
	if err := st.UpdateNodeIdentity("aabbccddeeff", "", ""); err != nil {
		t.Fatal(err)
	}
	n, err = st.GetNode("aabbccddeeff")
	if err != nil || n == nil {
		t.Fatalf("GetNode after empty update: %v / %v", n, err)
	}
	if n.Chip != "esp32c6" || n.Sdk != "v2.0.0-alpha.192" {
		t.Errorf("empty update clobbered identity: chip=%q sdk=%q", n.Chip, n.Sdk)
	}
}

func TestRecentCommandsForDevice(t *testing.T) {
	st := openTmp(t)
	for i := 0; i < 3; i++ {
		if _, err := st.EnqueueCommand("dev1", "set", `{"app":"a","key":"k","value":1}`, "cli", int64(100+i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.EnqueueCommand("dev2", "stop", `{"name":"x"}`, "cli", 200); err != nil {
		t.Fatal(err)
	}

	got, err := st.RecentCommandsForDevice("dev1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (limit)", len(got))
	}
	if got[0].ID <= got[1].ID {
		t.Errorf("not newest-first: %d then %d", got[0].ID, got[1].ID)
	}
	for _, c := range got {
		if c.Verb != "set" {
			t.Errorf("leaked another device's command: %+v", c)
		}
	}
}
