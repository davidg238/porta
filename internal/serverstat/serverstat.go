// Copyright (c) 2026 Ekorau LLC

// Package serverstat holds process-lifetime counters and build identity for
// porta's status surface (/health, /api/status, the console panel). One *Stats
// is created at boot and shared: the transport listeners record inbound packet
// volume, the dispatcher records report outcomes, and the HTTP handlers read a
// Snapshot. All methods are safe for concurrent use.
package serverstat

import (
	"net"
	"sync/atomic"
	"time"
)

// Transport identifies how a packet reached porta. porta's wire protocol is
// transport-neutral; the same engine serves all of these.
type Transport string

const (
	WiFi   Transport = "wifi"   // UDP/IPv4 (ESP32/Toit nodes today)
	Thread Transport = "thread" // UDP/IPv6 over the OTBR mesh (nRF52840/Zephyr)
	ESPNow Transport = "espnow" // serial-bridged L2 frames (future listener)
)

// Transports is the stable display order.
var Transports = []Transport{WiFi, Thread, ESPNow}

// Stats accumulates counters since boot.
type Stats struct {
	Version string
	Commit  string
	start   int64        // epoch seconds at construction
	now     func() int64 // injectable clock (epoch seconds)

	pkts  map[Transport]*atomic.Int64
	bytes map[Transport]*atomic.Int64

	reportsOK       atomic.Int64
	reportsRejected atomic.Int64
}

// New builds a Stats stamped with the build identity. now defaults to wall-clock
// epoch seconds; pass a fake in tests. start is captured from now().
func New(version, commit string, now func() int64) *Stats {
	if now == nil {
		now = func() int64 { return time.Now().Unix() }
	}
	s := &Stats{
		Version: version, Commit: commit, now: now, start: now(),
		pkts:  map[Transport]*atomic.Int64{},
		bytes: map[Transport]*atomic.Int64{},
	}
	for _, t := range Transports {
		s.pkts[t] = &atomic.Int64{}
		s.bytes[t] = &atomic.Int64{}
	}
	return s
}

// Packet records an inbound datagram of n bytes on transport t.
func (s *Stats) Packet(t Transport, n int) {
	if c, ok := s.pkts[t]; ok {
		c.Add(1)
		s.bytes[t].Add(int64(n))
	}
}

// ReportOK / ReportRejected record a report-write outcome.
func (s *Stats) ReportOK()       { s.reportsOK.Add(1) }
func (s *Stats) ReportRejected() { s.reportsRejected.Add(1) }

// UptimeSeconds is now - start.
func (s *Stats) UptimeSeconds() int64 { return s.now() - s.start }

// Snapshot is an immutable read for rendering.
type Snapshot struct {
	Version         string
	Commit          string
	UptimeSeconds   int64
	Packets         map[Transport]int64
	Bytes           map[Transport]int64
	ReportsOK       int64
	ReportsRejected int64
}

// Snapshot reads all counters into a plain value.
func (s *Stats) Snapshot() Snapshot {
	snap := Snapshot{
		Version:         s.Version,
		Commit:          s.Commit,
		UptimeSeconds:   s.UptimeSeconds(),
		Packets:         map[Transport]int64{},
		Bytes:           map[Transport]int64{},
		ReportsOK:       s.reportsOK.Load(),
		ReportsRejected: s.reportsRejected.Load(),
	}
	for _, t := range Transports {
		snap.Packets[t] = s.pkts[t].Load()
		snap.Bytes[t] = s.bytes[t].Load()
	}
	return snap
}

// TransportOf classifies a peer address ("ip:port") by IP family: IPv6 source →
// Thread (the OMR/mesh side of the dual-stack socket), IPv4 (incl. v4-mapped) →
// WiFi. ESP-NOW arrives via its own listener, not by address. Unparseable peers
// fall back to WiFi (the IPv4 default).
func TransportOf(peer string) Transport {
	host, _, err := net.SplitHostPort(peer)
	if err != nil {
		host = peer
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return WiFi
	}
	if ip.To4() != nil {
		return WiFi
	}
	return Thread
}
