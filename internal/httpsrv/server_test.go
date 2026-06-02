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

// TestNewSetsReadHeaderTimeout pins that New configures a positive
// ReadHeaderTimeout — without it a slow client can hold a handler goroutine
// open indefinitely by trickling header bytes (Slowloris).
func TestNewSetsReadHeaderTimeout(t *testing.T) {
	st := openTestStore(t)
	srv, err := New(Config{Bind: "127.0.0.1", Port: freePort(t)}, st)
	if err != nil {
		t.Fatal(err)
	}
	if srv.http.ReadHeaderTimeout != defaultReadHeaderTimeout {
		t.Errorf("ReadHeaderTimeout = %v, want %v", srv.http.ReadHeaderTimeout, defaultReadHeaderTimeout)
	}
}

// TestReadHeaderTimeoutClosesSlowClient verifies the mechanism end-to-end: a
// client that opens a connection and never finishes its request headers is
// dropped by the server once ReadHeaderTimeout elapses (overridden small here
// to keep the test fast), instead of holding the connection forever.
func TestReadHeaderTimeoutClosesSlowClient(t *testing.T) {
	st := openTestStore(t)
	port := freePort(t)
	srv, err := New(Config{Bind: "127.0.0.1", Port: port}, st)
	if err != nil {
		t.Fatal(err)
	}
	srv.http.ReadHeaderTimeout = 150 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Run(ctx) }()
	if err := waitListening(t, port, 500*time.Millisecond); err != nil {
		t.Fatal(err)
	}

	conn, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Partial request: a header line with no terminating blank line, so the
	// server keeps waiting for headers until ReadHeaderTimeout fires.
	if _, err := conn.Write([]byte("GET /health HTTP/1.1\r\n")); err != nil {
		t.Fatal(err)
	}
	// Generous client deadline well past the server timeout. If the server had
	// no header timeout, this Read would block until the deadline (timeout
	// error); with the timeout, the server closes the conn first → we get a
	// response or EOF promptly.
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 256)
	_, rerr := conn.Read(buf)
	if ne, ok := rerr.(net.Error); ok && ne.Timeout() {
		t.Fatal("read hit client deadline: server did not close the slow connection")
	}
	// Any non-timeout outcome (EOF or a 408 response) means the server acted.
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
