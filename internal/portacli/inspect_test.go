// Copyright (c) 2026 Ekorau LLC

package portacli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/davidg238/porta/internal/apisrv"
	"github.com/davidg238/porta/internal/control"
	"github.com/davidg238/porta/internal/store"
)

func TestRelativeAge(t *testing.T) {
	if got := control.RelativeAge(0, 1000); got != "never" {
		t.Errorf("never-seen → %q", got)
	}
	if got := control.RelativeAge(940, 1000); got != "60s ago" {
		t.Errorf("60s → %q", got)
	}
	if got := control.RelativeAge(1000-3600, 1000); got != "60m ago" {
		t.Errorf("60m → %q", got)
	}
}

func TestAppsFromObserved(t *testing.T) {
	apps, err := control.AppsFromObserved(`{"apps":{"blink":{"crc":7,"runlevel":3}},"config":{}}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 1 || apps[0].Name != "blink" || apps[0].CRC != 7 || apps[0].Runlevel != 3 {
		t.Errorf("apps = %+v", apps)
	}
	if apps, err := control.AppsFromObserved(""); err != nil || len(apps) != 0 {
		t.Errorf("empty observed: %+v %v", apps, err)
	}
}

// serveStore stands up a real apisrv over st and returns the store + the
// httptest server URL, so read commands exercise the same HTTP path the CLI
// uses in production. Mirrors monitorTestServer (monitor_test.go) but takes an
// already-seeded store.
func serveStore(t *testing.T, st *store.Store) (*store.Store, string) {
	t.Helper()
	mux := http.NewServeMux()
	apisrv.New(st).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return st, srv.URL
}

// runReadCmd runs the root command with --server pointed at srvURL and returns
// captured stdout.
func runReadCmd(t *testing.T, srvURL string, args ...string) string {
	t.Helper()
	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append([]string{"--server", srvURL}, args...))
	if err := root.Execute(); err != nil {
		t.Fatalf("cmd %v: %v — out=%s", args, err, out.String())
	}
	return out.String()
}

// seededStore opens a temp store with one named, online node carrying an
// observed report + a delivered and a pending command.
func seededStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/read.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := nowSec()
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", now)
	st.SetNodeName("aabbccddeeff", "blinky")
	st.EnqueueCommand("aabbccddeeff", "set", `{"app":"sampler","key":"interval","value":30}`, "cli", now)
	un, _ := st.NextUndelivered("aabbccddeeff")
	st.MarkDelivered(un.ID, now)
	st.EnqueueCommand("aabbccddeeff", "set-forward", `{"telemetry":{"on":true}}`, "cli", now)
	st.InsertReport("aabbccddeeff",
		`{"apps":{"blink":{"crc":7,"runlevel":3}},"config":{"sampler":{"interval":30}}}`, `{}`, now)
	return st
}

func TestScanCmdOverAPI(t *testing.T) {
	st := seededStore(t)
	_, url := serveStore(t, st)
	out := runReadCmd(t, url, "scan")
	// id column is %-16s (12-16 hex ids, PROTOCOL.md §1): a 12-hex id pads with 4.
	if !strings.Contains(out, "aabbccddeeff      blinky") {
		t.Errorf("scan output = %q", out)
	}
	if !strings.Contains(out, "online") {
		t.Errorf("scan should show online: %q", out)
	}
}

func TestScanCmdHidesNeverSeen(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/never.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	st.EnsureNode("aabbccddeeff", 0) // never seen (no last_seen)
	_, url := serveStore(t, st)

	out := runReadCmd(t, url, "scan")
	if strings.Contains(out, "aabbccddeeff") {
		t.Errorf("never-seen node should be hidden by default: %q", out)
	}
	out = runReadCmd(t, url, "scan", "--include-never-seen")
	if !strings.Contains(out, "aabbccddeeff") {
		t.Errorf("--include-never-seen should show it: %q", out)
	}
	if !strings.Contains(out, "never") || !strings.Contains(out, "offline") {
		t.Errorf("never-seen row should read never/offline: %q", out)
	}
}

func TestPingCmdOverAPI(t *testing.T) {
	st := seededStore(t)
	_, url := serveStore(t, st)
	// Resolve by name → confirmation echoes name + resolved MAC.
	out := runReadCmd(t, url, "ping", "-d", "blinky")
	if strings.TrimSpace(out) != "blinky (aabbccddeeff): online" {
		t.Errorf("ping output = %q", out)
	}
}

func TestLogCmdOverAPIOrderAndFormat(t *testing.T) {
	st := seededStore(t)
	_, url := serveStore(t, st)
	out := runReadCmd(t, url, "log", "-d", "aabbccddeeff")
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 log lines, got %d: %q", len(lines), out)
	}
	// Oldest-first: the delivered set then the pending set-forward.
	if !strings.HasPrefix(lines[0], "#1   ") || !strings.Contains(lines[0], "set ") || !strings.Contains(lines[0], "delivered=yes") {
		t.Errorf("line0 = %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "#2   ") || !strings.Contains(lines[1], "set-forward") || !strings.Contains(lines[1], "delivered=pending") {
		t.Errorf("line1 = %q", lines[1])
	}
}

func TestDeviceShowCmdOverAPI(t *testing.T) {
	st := seededStore(t)
	_, url := serveStore(t, st)
	out := runReadCmd(t, url, "device", "show", "-d", "blinky")
	for _, want := range []string{
		"id:            aabbccddeeff",
		"name:          blinky",
		"source_addr:   1.2.3.4:5",
		"cadence:       30s",
		"offline_after: 90s (derived 3×cadence)",
		`observed:      {"apps":{"blink":{"crc":7,"runlevel":3}},"config":{"sampler":{"interval":30}}}`,
		"undelivered:   1 command(s)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("device show missing %q; got:\n%s", want, out)
		}
	}
}

func TestContainerListCmdOverAPI(t *testing.T) {
	st := seededStore(t)
	_, url := serveStore(t, st)
	out := runReadCmd(t, url, "container", "list", "-d", "blinky")
	if strings.TrimSpace(out) != "blink            crc=7            runlevel=3" {
		t.Errorf("container list = %q", out)
	}
}

func TestDeviceGetCmdOverAPI(t *testing.T) {
	st := seededStore(t)
	_, url := serveStore(t, st)
	out := runReadCmd(t, url, "device", "get", "sampler", "interval", "-d", "blinky")
	if strings.TrimSpace(out) != "aabbccddeeff: sampler.interval desired=30 observed=30" {
		t.Errorf("device get = %q", out)
	}
}

func TestDeviceShowCmdLastReset(t *testing.T) {
	st := seededStore(t)
	code := int64(6)
	if err := st.UpdateNodeReset("aabbccddeeff", "watchdog", &code); err != nil {
		t.Fatal(err)
	}
	_, url := serveStore(t, st)
	out := runReadCmd(t, url, "device", "show", "-d", "blinky")
	if !strings.Contains(out, "last_reset:    watchdog (6)") {
		t.Errorf("device show missing last_reset line; got:\n%s", out)
	}
}
