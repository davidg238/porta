// Copyright (c) 2026 Ekorau LLC

package serverstat

import "testing"

func TestTransportOfClassifiesByAddressFamily(t *testing.T) {
	cases := map[string]Transport{
		"192.168.0.240:64058":          WiFi,   // ESP32/Toit over WiFi (IPv4)
		"10.0.0.5:6969":                WiFi,   // any IPv4 LAN
		"[fd77:9957:d9f3:1::1]:6969":   Thread, // nRF52840 over Thread (IPv6 OMR/mesh)
		"[fe80::c816:2b36:2337:69c]:5": Thread, // link-local IPv6 still Thread
		"[::ffff:192.168.0.1]:5":       WiFi,   // v4-mapped on the dual-stack socket = WiFi
	}
	for peer, want := range cases {
		if got := TransportOf(peer); got != want {
			t.Errorf("TransportOf(%q) = %q, want %q", peer, got, want)
		}
	}
}

func TestPacketCountsAndBytesPerTransport(t *testing.T) {
	s := New("1.2.3", "abc123", nil)
	s.Packet(WiFi, 100)
	s.Packet(WiFi, 40)
	s.Packet(Thread, 60)

	snap := s.Snapshot()
	if snap.Packets[WiFi] != 2 || snap.Bytes[WiFi] != 140 {
		t.Errorf("wifi: packets=%d bytes=%d, want 2/140", snap.Packets[WiFi], snap.Bytes[WiFi])
	}
	if snap.Packets[Thread] != 1 || snap.Bytes[Thread] != 60 {
		t.Errorf("thread: packets=%d bytes=%d, want 1/60", snap.Packets[Thread], snap.Bytes[Thread])
	}
	if snap.Packets[ESPNow] != 0 {
		t.Errorf("espnow should be 0, got %d", snap.Packets[ESPNow])
	}
}

func TestReportOutcomeCounters(t *testing.T) {
	s := New("v", "c", nil)
	s.ReportOK()
	s.ReportOK()
	s.ReportRejected()
	snap := s.Snapshot()
	if snap.ReportsOK != 2 || snap.ReportsRejected != 1 {
		t.Errorf("reports ok=%d rejected=%d, want 2/1", snap.ReportsOK, snap.ReportsRejected)
	}
}

func TestVersionAndUptime(t *testing.T) {
	now := int64(1000)
	s := New("9.9.9", "deadbee", func() int64 { return now })
	now = 1042 // 42s later
	snap := s.Snapshot()
	if snap.Version != "9.9.9" || snap.Commit != "deadbee" {
		t.Errorf("version/commit = %q/%q", snap.Version, snap.Commit)
	}
	if snap.UptimeSeconds != 42 {
		t.Errorf("uptime = %d, want 42", snap.UptimeSeconds)
	}
}
