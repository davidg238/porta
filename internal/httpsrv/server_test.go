// internal/httpsrv/server_test.go
package httpsrv

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// freePort returns an OS-assigned free TCP port for tests.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

func TestNewServerRegistersHealth(t *testing.T) {
	st := openTestStore(t)
	srv, err := New(Config{Bind: "127.0.0.1", Port: freePort(t)}, st)
	if err != nil {
		t.Fatal(err)
	}
	if srv.Mux == nil {
		t.Fatal("Mux is nil")
	}
	u, _ := url.Parse("/health")
	handler, pattern := srv.Mux.Handler(&http.Request{Method: "GET", URL: u})
	if handler == nil || pattern != "/health" {
		t.Errorf("mux didn't register /health: handler=%v pattern=%q", handler, pattern)
	}
}

func TestNewServerRejectsBadCIDR(t *testing.T) {
	st := openTestStore(t)
	_, err := New(Config{Bind: "127.0.0.1", Port: freePort(t), AllowCIDR: []string{"not-a-cidr"}}, st)
	if err == nil {
		t.Error("expected error on bad CIDR")
	}
}

func TestRunServesHealthAndExitsOnCancel(t *testing.T) {
	st := openTestStore(t)
	port := freePort(t)
	srv, err := New(Config{Bind: "127.0.0.1", Port: port}, st)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	// Wait for the listener to come up.
	if err := waitListening(t, port, 500*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	// /health responds.
	resp, err := http.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/health")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("got %d, want 200; body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"status":"ok"`) {
		t.Errorf("body=%s, want status:ok", body)
	}
	// Cancel and assert clean exit within the shutdown budget.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit within 2s of cancel")
	}
}

func TestRunReturnsErrorOnPortInUse(t *testing.T) {
	st := openTestStore(t)
	port := freePort(t)
	// Squat the port.
	squat, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	defer squat.Close()
	srv, err := New(Config{Bind: "127.0.0.1", Port: port}, st)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err = srv.Run(ctx)
	if err == nil {
		t.Error("expected error on port-in-use")
	}
}

// waitListening polls 127.0.0.1:port until something accepts a TCP
// connect, or budget elapses.
func waitListening(t *testing.T, port int, budget time.Duration) error {
	t.Helper()
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 50*time.Millisecond)
		if err == nil {
			c.Close()
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return net.ErrClosed
}
