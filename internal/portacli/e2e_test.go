// Copyright (c) 2026 Ekorau LLC

package portacli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/davidg238/porta/internal/apisrv"
	"github.com/davidg238/porta/internal/store"
)

// TestDeviceSetEndToEnd runs the real cobra command with --server pointed at a
// real apisrv over a temp store, proving flag plumbing + selector passthrough +
// the write landing in the store.
func TestDeviceSetEndToEnd(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/e2e.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	st.SetNodeName("aabbccddeeff", "blinky")

	mux := http.NewServeMux()
	apisrv.New(st).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	// Resolve by NAME to prove the server resolves the selector and echoes the MAC.
	root.SetArgs([]string{"device", "set", "sampler", "interval", "30",
		"-d", "blinky", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v — out=%s", err, out.String())
	}

	cmd, _ := st.NextUndelivered("aabbccddeeff")
	if cmd == nil || cmd.Verb != "set" {
		t.Fatalf("queued=%+v", cmd)
	}
	// Confirmation leads with the resolved MAC, not the name.
	if !strings.Contains(out.String(), "aabbccddeeff: enqueued set sampler.interval=30") {
		t.Errorf("output = %q", out.String())
	}
}

// TestServerDownIsFriendly proves the transport-error wrap surfaces through the
// command when no server is listening.
func TestServerDownIsFriendly(t *testing.T) {
	// Stand up then immediately close a server to get a refused port.
	srv := httptest.NewServer(http.NewServeMux())
	url := srv.URL
	srv.Close()

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"device", "set-forward", "--print", "off", "--log", "on", "--telemetry", "on", "-d", "aabbccddeeff", "--server", url})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "porta serve") {
		t.Fatalf("want friendly 'porta serve' error, got %v", err)
	}
}
