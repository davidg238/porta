// Copyright (c) 2026 Ekorau LLC

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

func TestUpdateNodeReportInterval(t *testing.T) {
	st := openTmp(t)
	if err := st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000); err != nil {
		t.Fatal(err)
	}
	// Default (never reported) is 0 → callers fall back to poll-interval.
	n, _ := st.GetNode("aabbccddeeff")
	if n.ReportIntervalS != 0 {
		t.Errorf("fresh node report_interval_s = %d, want 0", n.ReportIntervalS)
	}
	secs := int64(60)
	if err := st.UpdateNodeReportInterval("aabbccddeeff", &secs); err != nil {
		t.Fatal(err)
	}
	n, err := st.GetNode("aabbccddeeff")
	if err != nil || n == nil {
		t.Fatalf("GetNode: %v / %v", n, err)
	}
	if n.ReportIntervalS != 60 {
		t.Errorf("got report_interval_s=%d, want 60", n.ReportIntervalS)
	}
	// A nil value (absent in report) must not clobber a known cadence.
	if err := st.UpdateNodeReportInterval("aabbccddeeff", nil); err != nil {
		t.Fatal(err)
	}
	n, _ = st.GetNode("aabbccddeeff")
	if n.ReportIntervalS != 60 {
		t.Errorf("nil update clobbered report_interval_s: %d", n.ReportIntervalS)
	}
}

func TestUpdateNodeReset(t *testing.T) {
	st := openTmp(t)
	if err := st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000); err != nil {
		t.Fatal(err)
	}
	code := int64(6)
	if err := st.UpdateNodeReset("aabbccddeeff", "watchdog", &code); err != nil {
		t.Fatal(err)
	}
	n, err := st.GetNode("aabbccddeeff")
	if err != nil || n == nil {
		t.Fatalf("GetNode: %v / %v", n, err)
	}
	if n.LastReset != "watchdog" || !n.LastResetCode.Valid || n.LastResetCode.Int64 != 6 {
		t.Errorf("got reset=%q code=%v, want watchdog / 6", n.LastReset, n.LastResetCode)
	}
	// Empty category + nil code must not clobber a known value.
	if err := st.UpdateNodeReset("aabbccddeeff", "", nil); err != nil {
		t.Fatal(err)
	}
	n, _ = st.GetNode("aabbccddeeff")
	if n.LastReset != "watchdog" || n.LastResetCode.Int64 != 6 {
		t.Errorf("empty update clobbered reset: reset=%q code=%v", n.LastReset, n.LastResetCode)
	}
}

func TestUpdateNodeConfig(t *testing.T) {
	st := openTmp(t)
	if err := st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000); err != nil {
		t.Fatal(err)
	}
	// Fresh node carries no echoed config.
	n, _ := st.GetNode("aabbccddeeff")
	if n.NodeConfig != "" {
		t.Errorf("fresh node_config = %q, want empty", n.NodeConfig)
	}
	// A deep-sleep echo with a name persists the blob and mirrors the name.
	ds := `{"mode":"deep-sleep","min_awake_s":5,"max_awake_s":20,"max_asleep_s":300,"name":"door"}`
	if err := st.UpdateNodeConfig("aabbccddeeff", ds, "door"); err != nil {
		t.Fatal(err)
	}
	n, _ = st.GetNode("aabbccddeeff")
	if n.NodeConfig != ds {
		t.Errorf("node_config = %q, want %q", n.NodeConfig, ds)
	}
	if n.Name != "door" {
		t.Errorf("name = %q, want mirrored 'door'", n.Name)
	}
	// An always-on echo without a name updates the blob but must NOT clobber the
	// mirrored name (unnamed echo → name key omitted → keep prior).
	ao := `{"mode":"always-on","poll_interval_s":60}`
	if err := st.UpdateNodeConfig("aabbccddeeff", ao, ""); err != nil {
		t.Fatal(err)
	}
	n, _ = st.GetNode("aabbccddeeff")
	if n.NodeConfig != ao {
		t.Errorf("node_config = %q, want %q", n.NodeConfig, ao)
	}
	if n.Name != "door" {
		t.Errorf("empty name clobbered mirror: %q", n.Name)
	}
}

func TestNodeCadenceS(t *testing.T) {
	// deep-sleep node's cadence is its max_asleep_s.
	ds := &Node{NodeConfig: `{"mode":"deep-sleep","max_awake_s":20,"max_asleep_s":900}`}
	if got := ds.CadenceS(); got != 900 {
		t.Errorf("deep-sleep CadenceS = %d, want 900", got)
	}
	// always-on node's cadence is its poll_interval_s.
	ao := &Node{NodeConfig: `{"mode":"always-on","poll_interval_s":60}`}
	if got := ao.CadenceS(); got != 60 {
		t.Errorf("always-on CadenceS = %d, want 60", got)
	}
	// No echo / garbage → 0 (caller falls back).
	if got := (&Node{}).CadenceS(); got != 0 {
		t.Errorf("no-config CadenceS = %d, want 0", got)
	}
	if got := (&Node{NodeConfig: "not json"}).CadenceS(); got != 0 {
		t.Errorf("garbage CadenceS = %d, want 0", got)
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
