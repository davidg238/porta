// Copyright (c) 2026 Ekorau LLC

package portacli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/davidg238/porta/devsdk/apiclient"
	"github.com/davidg238/porta/internal/apisrv"
	"github.com/davidg238/porta/internal/store"
)

// newClientServer stands up the REAL apisrv.Handler over a temp store behind an
// httptest server and returns a client pointed at it plus the store (so tests
// can assert what landed). This gives true CLI-core → HTTP → apisrv → store
// coverage.
func newClientServer(t *testing.T) (*apiclient.Client, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/m.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	mux := http.NewServeMux()
	apisrv.New(st).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return apiclient.New(srv.URL), st
}

func TestRunDeviceSetEnqueuesAndPrints(t *testing.T) {
	c, st := newClientServer(t)
	var out bytes.Buffer
	// Selector is a well-formed MAC never seen → EnsureNode-on-write creates it.
	if err := runDeviceSet(&out, c, "aabbccddeeff", "sampler", "interval", "30"); err != nil {
		t.Fatal(err)
	}
	cmd, _ := st.NextUndelivered("aabbccddeeff")
	if cmd == nil || cmd.Verb != "set" {
		t.Fatalf("queued=%+v", cmd)
	}
	if !strings.Contains(out.String(), "aabbccddeeff: enqueued set sampler.interval=30 (command #") {
		t.Errorf("output = %q", out.String())
	}
}

// TestRunDeviceSetTypeInference preserves the int/float/bool/string inference
// coverage migrated out of config_test.go.
func TestRunDeviceSetTypeInference(t *testing.T) {
	cases := []struct {
		name, value, wantArgs string
	}{
		{"int", "30", `{"app":"a","key":"k","value":30}`},
		{"float", "21.5", `{"app":"a","key":"k","value":21.5}`},
		{"bool", "true", `{"app":"a","key":"k","value":true}`},
		{"string", "eco", `{"app":"a","key":"k","value":"eco"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, st := newClientServer(t)
			var out bytes.Buffer
			if err := runDeviceSet(&out, c, "aabbccddeeff", "a", "k", tc.value); err != nil {
				t.Fatal(err)
			}
			next, _ := st.NextUndelivered("aabbccddeeff")
			if next == nil || next.Args != tc.wantArgs {
				t.Errorf("Args=%v, want %s", next, tc.wantArgs)
			}
		})
	}
}

func TestRunDeviceSetConsole(t *testing.T) {
	c, st := newClientServer(t)
	var out bytes.Buffer
	if err := runDeviceSetConsole(&out, c, "aabbccddeeff", "on"); err != nil {
		t.Fatal(err)
	}
	cmd, _ := st.NextUndelivered("aabbccddeeff")
	if cmd == nil || cmd.Verb != "set-console" {
		t.Fatalf("queued=%+v", cmd)
	}
	if !strings.Contains(out.String(), "aabbccddeeff: enqueued set-console on (command #") {
		t.Errorf("output = %q", out.String())
	}
}

func TestRunDeviceSetConsoleBadStateIsServerError(t *testing.T) {
	c, _ := newClientServer(t)
	var out bytes.Buffer
	err := runDeviceSetConsole(&out, c, "aabbccddeeff", "maybe")
	if err == nil || !strings.Contains(err.Error(), "on or off") {
		t.Fatalf("want server validation error, got %v", err)
	}
}

func TestRunDeviceSetPowerMode(t *testing.T) {
	c, st := newClientServer(t)
	var out bytes.Buffer
	if err := runDeviceSetPowerMode(&out, c, "aabbccddeeff", "always-on"); err != nil {
		t.Fatal(err)
	}
	cmd, _ := st.NextUndelivered("aabbccddeeff")
	if cmd == nil || cmd.Verb != "set-power-mode" {
		t.Fatalf("queued=%+v", cmd)
	}
	if !strings.Contains(out.String(), "aabbccddeeff: enqueued set-power-mode always-on (command #") {
		t.Errorf("output = %q", out.String())
	}
}

func TestRunSetPollIntervalEnqueuesSilently(t *testing.T) {
	c, st := newClientServer(t)
	if err := runSetPollInterval(c, "aabbccddeeff", "45s"); err != nil {
		t.Fatal(err)
	}
	cmd, _ := st.NextUndelivered("aabbccddeeff")
	if cmd == nil || cmd.Verb != "set-poll-interval" {
		t.Fatalf("queued=%+v", cmd)
	}
}

func TestRunUninstallEnqueuesStop(t *testing.T) {
	c, st := newClientServer(t)
	var out bytes.Buffer
	if err := runUninstall(&out, c, "aabbccddeeff", "blink"); err != nil {
		t.Fatal(err)
	}
	cmd, _ := st.NextUndelivered("aabbccddeeff")
	if cmd == nil || cmd.Verb != "stop" || cmd.Args != `{"name":"blink"}` {
		t.Fatalf("queued=%+v", cmd)
	}
	if !strings.Contains(out.String(), "aabbccddeeff: enqueued stop blink (command #") {
		t.Errorf("output = %q", out.String())
	}
}

func TestRunInstallRegistersAndPrintsWithoutCRC(t *testing.T) {
	c, st := newClientServer(t)
	dir := t.TempDir()
	bin := filepath.Join(dir, "blink.bin")
	img := []byte("fake-image-bytes")
	if err := os.WriteFile(bin, img, 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runInstall(&out, c, "aabbccddeeff", "blink", bin, apiclient.InstallOpts{
		Lifecycle: "run-loop", Runlevel: 3, Triggers: []string{"boot"},
	}); err != nil {
		t.Fatal(err)
	}
	cmd, _ := st.NextUndelivered("aabbccddeeff")
	if cmd == nil || cmd.Verb != "run" {
		t.Fatalf("queued=%+v", cmd)
	}
	var args map[string]interface{}
	json.Unmarshal([]byte(cmd.Args), &args)
	if args["size"].(float64) != float64(len(img)) {
		t.Errorf("size arg = %v, want %d", args["size"], len(img))
	}
	s := out.String()
	if !strings.Contains(s, "aabbccddeeff: registered blink (16 B); enqueued run (command #") {
		t.Errorf("output = %q", s)
	}
	if strings.Contains(s, "@") {
		t.Errorf("CRC should be dropped from the install line: %q", s)
	}
}

func TestRunInstallRejectsNonBin(t *testing.T) {
	c, _ := newClientServer(t)
	dir := t.TempDir()
	pod := filepath.Join(dir, "x.pod")
	os.WriteFile(pod, []byte("x"), 0o644)
	var out bytes.Buffer
	if err := runInstall(&out, c, "aabbccddeeff", "x", pod, apiclient.InstallOpts{Lifecycle: "run-once"}); err == nil {
		t.Error(".pod must be rejected (only .bin)")
	}
}

func TestRunDeviceName(t *testing.T) {
	c, st := newClientServer(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	if err := runDeviceName(c, "aabbccddeeff", "newname"); err != nil {
		t.Fatal(err)
	}
	n, _ := st.GetNode("aabbccddeeff")
	if n.Name != "newname" {
		t.Errorf("name=%q", n.Name)
	}
}

func TestRunSetMaxOffline(t *testing.T) {
	c, st := newClientServer(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	if err := runSetMaxOffline(c, "aabbccddeeff", 600); err != nil {
		t.Fatal(err)
	}
	n, _ := st.GetNode("aabbccddeeff")
	if n.MaxOfflineS != 600 {
		t.Errorf("max_offline_s=%d", n.MaxOfflineS)
	}
}
