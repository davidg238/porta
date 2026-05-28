# porta Go Core — B4a Multi-Listener Serve Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an HTTP listener to `porta serve` alongside the existing UDP/TFTP listener, with IP-allowlist middleware, a `/health` endpoint, and graceful SIGINT/SIGTERM shutdown. Foundation for B4c (htmx UI) and B4b (MCP) to register routes on the shared mux.

**Architecture:** A new `internal/httpsrv` package owns the HTTP server lifecycle, CIDR allowlist middleware, and `/health` endpoint. Consumers (B4b, B4c) register routes on the exported `Server.Mux`. `internal/portacli/serve.go` runs the existing UDP listener and the new HTTP listener as concurrent goroutines under one root context. `internal/portacli/root.go`'s `Execute` wraps `signal.NotifyContext` so SIGINT/SIGTERM cancels the root context — closes B3 issue #2 as a side-effect (the existing `runMonitor --follow` ctx-cancel branch becomes reachable in production).

**Tech Stack:** Go 1.21+, stdlib only (`net`, `net/http`, `os`, `os/signal`, `syscall`, `context`, `encoding/json`), `spf13/cobra` (already wired).

**Branch:** `feat/porta-b4a-multi-listener` (already cut; spec committed at `aad0d82`).

**Spec:** `docs/specs/2026-05-28-porta-b4a-multi-listener-design.md`

---

## File Structure

| Path | Kind | Responsibility |
|---|---|---|
| `internal/httpsrv/cidr.go` | N | `AllowCIDR` parser + `AllowlistMiddleware` |
| `internal/httpsrv/cidr_test.go` | N | parsing, accept/deny, IPv4-in-IPv6, malformed-addr defense |
| `internal/httpsrv/health.go` | N | `/health` JSON handler |
| `internal/httpsrv/health_test.go` | N | 200, JSON shape, nodes count, store-error path |
| `internal/httpsrv/server.go` | N | `Config`, `Server`, `New`, `Run` (lifecycle + graceful shutdown) |
| `internal/httpsrv/server_test.go` | N | mux registration, dynamic-port startup, ctx-cancel exit, port-in-use error |
| `internal/portacli/root.go` | M | `Execute` wraps `signal.NotifyContext`; `RunE` uses `cmd.Context()` |
| `internal/portacli/serve.go` | M | `--http-port/--http-bind/--http-allow-cidr` flags; UDP+HTTP goroutines under root ctx |
| `internal/portacli/serve_test.go` | N | `defaultAllowCIDR()` returns the 5 entries; flag wiring |

Total: 7 new files, 2 modified files.

Order of implementation: T1 (CIDR/middleware) → T2 (health) → T3 (Server lifecycle, depends on T1+T2) → T4 (signal wiring in Execute) → T5 (serve.go wires everything in).

---

## Task 1: `AllowCIDR` + `AllowlistMiddleware`

**Files:**
- Create: `internal/httpsrv/cidr.go`
- Create: `internal/httpsrv/cidr_test.go`

- [ ] **Step 1.1: Write the failing test**

```go
// internal/httpsrv/cidr_test.go
package httpsrv

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAllowCIDRParsesRFC1918AndIPv6(t *testing.T) {
	in := []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "127.0.0.0/8", "::1/128"}
	out, err := AllowCIDR(in)
	if err != nil {
		t.Fatalf("AllowCIDR: %v", err)
	}
	if len(out) != 5 {
		t.Errorf("got %d nets, want 5", len(out))
	}
}

func TestAllowCIDRRejectsGarbage(t *testing.T) {
	if _, err := AllowCIDR([]string{"not-a-cidr"}); err == nil {
		t.Error("expected error on garbage")
	}
	if _, err := AllowCIDR([]string{"10.0.0.0/8", "bad"}); err == nil {
		t.Error("expected error when one entry is bad")
	}
}

func TestAllowCIDRFiltersEmptyAndWhitespace(t *testing.T) {
	out, err := AllowCIDR([]string{"", "  ", "10.0.0.0/8", "\t"})
	if err != nil {
		t.Fatalf("AllowCIDR: %v", err)
	}
	if len(out) != 1 {
		t.Errorf("got %d nets, want 1 (empties filtered)", len(out))
	}
}

func TestAllowCIDRAllEmptyReturnsNil(t *testing.T) {
	out, err := AllowCIDR([]string{"", "  "})
	if err != nil {
		t.Fatalf("AllowCIDR: %v", err)
	}
	if out != nil {
		t.Errorf("got %v, want nil (all-empty = disable allowlist)", out)
	}
}

// okHandler is the dummy downstream for middleware tests.
var okHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
})

func mustNets(t *testing.T, cidrs ...string) []*net.IPNet {
	t.Helper()
	nets, err := AllowCIDR(cidrs)
	if err != nil {
		t.Fatal(err)
	}
	return nets
}

func TestAllowlistMiddlewareEmptyServesAnyPeer(t *testing.T) {
	mw := AllowlistMiddleware(nil)
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "8.8.8.8:1234"
	w := httptest.NewRecorder()
	mw(okHandler).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("got %d, want 200 (nil allowed = serve any)", w.Code)
	}
}

func TestAllowlistMiddlewareEmptySliceServesAnyPeer(t *testing.T) {
	mw := AllowlistMiddleware([]*net.IPNet{})
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "8.8.8.8:1234"
	w := httptest.NewRecorder()
	mw(okHandler).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("got %d, want 200 (empty allowed = serve any)", w.Code)
	}
}

func TestAllowlistMiddlewareAcceptsAllowedIPv4(t *testing.T) {
	mw := AllowlistMiddleware(mustNets(t, "192.168.0.0/16"))
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.168.1.5:1234"
	w := httptest.NewRecorder()
	mw(okHandler).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("got %d, want 200", w.Code)
	}
}

func TestAllowlistMiddlewareRejectsDisallowedIPv4(t *testing.T) {
	mw := AllowlistMiddleware(mustNets(t, "192.168.0.0/16"))
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	w := httptest.NewRecorder()
	mw(okHandler).ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("got %d, want 403", w.Code)
	}
}

func TestAllowlistMiddlewareIPv4MappedIPv6Matches(t *testing.T) {
	mw := AllowlistMiddleware(mustNets(t, "192.168.0.0/16"))
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "[::ffff:192.168.1.5]:1234"
	w := httptest.NewRecorder()
	mw(okHandler).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("got %d, want 200 (IPv4-mapped IPv6 should match v4 CIDR)", w.Code)
	}
}

func TestAllowlistMiddlewareIPv6Matches(t *testing.T) {
	mw := AllowlistMiddleware(mustNets(t, "::1/128"))
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "[::1]:1234"
	w := httptest.NewRecorder()
	mw(okHandler).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("got %d, want 200", w.Code)
	}
}

func TestAllowlistMiddlewareMalformedRemoteAddrRejected(t *testing.T) {
	mw := AllowlistMiddleware(mustNets(t, "192.168.0.0/16"))
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "not-an-addr"
	w := httptest.NewRecorder()
	mw(okHandler).ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("got %d, want 403 (defense)", w.Code)
	}
}
```

- [ ] **Step 1.2: Run the test, see it fail**

Run: `go test ./internal/httpsrv/ -run TestAllowCIDR`
Expected: FAIL with `package … is not in std (or no .go files found)` since the package doesn't exist yet.

- [ ] **Step 1.3: Implement `internal/httpsrv/cidr.go`**

```go
// Package httpsrv is the porta gateway's operator HTTP listener — the
// foundation that B4b (MCP) and B4c (htmx) register routes on. Provides
// IP allowlist middleware, /health, and graceful shutdown. No app routes
// live here; consumers do s.Mux.Handle("/<path>", handler).
package httpsrv

import (
	"net"
	"net/http"
	"strings"
)

// AllowCIDR parses raw CIDR strings into *net.IPNet, filtering
// empty/whitespace-only entries (so `--http-allow-cidr=""` from cobra,
// which arrives as []string{""}, cleanly disables the allowlist). All-empty
// input returns nil so the middleware short-circuits to "serve any peer".
// Returns the first parse error encountered with the offending raw string.
func AllowCIDR(raw []string) ([]*net.IPNet, error) {
	var out []*net.IPNet
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

// AllowlistMiddleware returns http middleware that rejects (403) any
// request whose peer IP is outside allowed. nil or empty allowed = serve
// any peer (the middleware short-circuits without parsing RemoteAddr).
//
// Peer IP is extracted from r.RemoteAddr via net.SplitHostPort so it
// handles both IPv4 ("1.2.3.4:5") and bracketed IPv6 ("[::1]:5") forms.
// IPv4-mapped IPv6 addresses ("::ffff:192.168.1.5") are normalized to
// their v4 form via ip.To4() so they match IPv4 CIDRs as expected.
//
// A malformed RemoteAddr is rejected defensively — porta is bound to
// LAN and this should never happen in practice, but a parse failure
// must not silently pass.
func AllowlistMiddleware(allowed []*net.IPNet) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if len(allowed) == 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			ip := net.ParseIP(host)
			if ip == nil {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			if v4 := ip.To4(); v4 != nil {
				ip = v4
			}
			for _, n := range allowed {
				if n.Contains(ip) {
					next.ServeHTTP(w, r)
					return
				}
			}
			http.Error(w, "forbidden", http.StatusForbidden)
		})
	}
}
```

- [ ] **Step 1.4: Run the tests, see them pass**

Run: `go test ./internal/httpsrv/ -v`
Expected: PASS for all 10 tests in this file.

- [ ] **Step 1.5: Commit**

```bash
git add internal/httpsrv/cidr.go internal/httpsrv/cidr_test.go
git commit -m "$(cat <<'EOF'
feat(porta): httpsrv — AllowCIDR + AllowlistMiddleware

New internal/httpsrv package; first piece is the CIDR allowlist for the
operator HTTP listener. AllowCIDR parses raw strings to *net.IPNet,
filtering empties (so cobra's --http-allow-cidr="" → []string{""} cleanly
becomes "serve any peer"). AllowlistMiddleware short-circuits to passthrough
when allowed is nil/empty; otherwise extracts the peer IP from RemoteAddr
(net.SplitHostPort handles both v4 and bracketed v6), normalizes IPv4-
mapped IPv6 via ip.To4(), and 403s on no-match or malformed RemoteAddr.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: `/health` handler

**Files:**
- Create: `internal/httpsrv/health.go`
- Create: `internal/httpsrv/health_test.go`

- [ ] **Step 2.1: Write the failing test**

```go
// internal/httpsrv/health_test.go
package httpsrv

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/davidg238/porta/internal/store"
)

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/h.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestHealthHandlerReturnsOK(t *testing.T) {
	st := openTestStore(t)
	h := healthHandler(st)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/health", nil))
	if w.Code != http.StatusOK {
		t.Errorf("got %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var body struct {
		Status string `json:"status"`
		Nodes  int    `json:"nodes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body not valid JSON: %v (body=%s)", err, w.Body.String())
	}
	if body.Status != "ok" {
		t.Errorf("status=%q, want ok", body.Status)
	}
	if body.Nodes != 0 {
		t.Errorf("nodes=%d, want 0 (fresh store)", body.Nodes)
	}
}

func TestHealthHandlerCountsNodes(t *testing.T) {
	st := openTestStore(t)
	st.EnsureNode("dev1", 1000)
	st.EnsureNode("dev2", 1000)
	st.EnsureNode("dev3", 1000)
	h := healthHandler(st)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/health", nil))
	var body struct {
		Nodes int `json:"nodes"`
	}
	json.Unmarshal(w.Body.Bytes(), &body)
	if body.Nodes != 3 {
		t.Errorf("nodes=%d, want 3", body.Nodes)
	}
}
```

- [ ] **Step 2.2: Run the test, see it fail**

Run: `go test ./internal/httpsrv/ -run TestHealthHandler`
Expected: FAIL with `undefined: healthHandler`.

- [ ] **Step 2.3: Implement `internal/httpsrv/health.go`**

```go
// internal/httpsrv/health.go
package httpsrv

import (
	"encoding/json"
	"net/http"

	"github.com/davidg238/porta/internal/store"
)

// healthHandler returns 200 with a small JSON body summarizing the
// gateway's state for uptime checks and the future ops dashboard:
//
//   {"status":"ok","nodes":<int>}
//
// The endpoint reports the listener itself is healthy. A transient
// store.ListNodes failure renders nodes:-1 but still 200 — the HTTP
// listener is up even if the DB blip is in progress.
func healthHandler(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		nodes := -1
		if rows, err := st.ListNodes(); err == nil {
			nodes = len(rows)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "ok",
			"nodes":  nodes,
		})
	}
}
```

- [ ] **Step 2.4: Run the tests, see them pass**

Run: `go test ./internal/httpsrv/ -v`
Expected: PASS for all CIDR tests plus the 2 new health tests.

- [ ] **Step 2.5: Commit**

```bash
git add internal/httpsrv/health.go internal/httpsrv/health_test.go
git commit -m "$(cat <<'EOF'
feat(porta): httpsrv — /health JSON endpoint

Returns 200 with {"status":"ok","nodes":<int>}. nodes is the current
store.ListNodes count, or -1 if a transient DB error fires (listener
itself is healthy regardless). Single dependency: internal/store. Will
be pre-registered on the Server.Mux in the next task.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: `Server` lifecycle — `Config`, `New`, `Run`

**Files:**
- Create: `internal/httpsrv/server.go`
- Create: `internal/httpsrv/server_test.go`

- [ ] **Step 3.1: Write the failing test**

```go
// internal/httpsrv/server_test.go
package httpsrv

import (
	"context"
	"io"
	"net"
	"net/http"
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
	// Probe the mux directly — no socket needed.
	handler, pattern := srv.Mux.Handler(&http.Request{Method: "GET", URL: mustURL(t, "/health")})
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

// mustURL parses a path-only URL for the mux-probe test.
func mustURL(t *testing.T, path string) *urlForTest {
	t.Helper()
	return &urlForTest{path: path}
}

type urlForTest struct{ path string }

func (u *urlForTest) String() string { return u.path }

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
```

The test uses `*urlForTest` because `http.Request.URL` is `*url.URL`. The mux-probe path needs a real `*url.URL` — adjust the test to construct one properly:

Replace `TestNewServerRegistersHealth` and `mustURL`/`urlForTest` helpers with:

```go
// (Replace the urlForTest helper with real net/url usage.)
import "net/url"

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
```

(Drop the `mustURL` / `urlForTest` helpers; add `"net/url"` to imports.)

- [ ] **Step 3.2: Run the tests, see them fail**

Run: `go test ./internal/httpsrv/ -run "TestNewServer|TestRunServes|TestRunReturns"`
Expected: FAIL with `undefined: New`, `undefined: Config`.

- [ ] **Step 3.3: Implement `internal/httpsrv/server.go`**

```go
// internal/httpsrv/server.go
package httpsrv

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/davidg238/porta/internal/store"
)

// Config holds the operator HTTP listener configuration. Bind+Port match
// the cobra --http-bind/--http-port flags; AllowCIDR mirrors the
// --http-allow-cidr flag (caller passes the raw strings; New runs
// AllowCIDR to parse).
type Config struct {
	Bind      string   // e.g. "0.0.0.0" or "127.0.0.1"
	Port      int      // 0 means disabled (caller should not construct a Server)
	AllowCIDR []string // empty = serve any peer (no allowlist)
}

// Server wraps the http.Server with porta-specific plumbing. Mux is the
// shared mux that consumer packages register routes on; the allowlist
// middleware wraps every route uniformly via the http.Server.Handler.
type Server struct {
	http *http.Server
	Mux  *http.ServeMux
}

// New constructs a Server with the allowlist middleware applied to a
// fresh mux. /health is pre-registered; consumer packages (mcp, web)
// register additional routes via s.Mux.Handle. Returns an error if
// AllowCIDR fails to parse any entry.
func New(cfg Config, st *store.Store) (*Server, error) {
	nets, err := AllowCIDR(cfg.AllowCIDR)
	if err != nil {
		return nil, fmt.Errorf("httpsrv: parse allow-cidr: %w", err)
	}
	mux := http.NewServeMux()
	mux.Handle("/health", healthHandler(st))
	return &Server{
		http: &http.Server{
			Addr:    fmt.Sprintf("%s:%d", cfg.Bind, cfg.Port),
			Handler: AllowlistMiddleware(nets)(mux),
		},
		Mux: mux,
	}, nil
}

// Run starts the listener and blocks until ctx is cancelled or the
// listener errors. On ctx cancel, performs a graceful Shutdown with a
// 5-second timeout. Returns nil for clean shutdown (ctx cancel or
// http.ErrServerClosed), non-nil for a real listener failure.
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() { errCh <- s.http.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.http.Shutdown(shutCtx)
		// Drain the listener error channel; ListenAndServe returns
		// ErrServerClosed once Shutdown completes.
		<-errCh
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
```

- [ ] **Step 3.4: Run the tests, see them pass**

Run: `go test ./internal/httpsrv/ -v -race`
Expected: PASS — all CIDR + health + 4 server tests green; no races.

- [ ] **Step 3.5: Commit**

```bash
git add internal/httpsrv/server.go internal/httpsrv/server_test.go
git commit -m "$(cat <<'EOF'
feat(porta): httpsrv — Server lifecycle (New, Run, graceful Shutdown)

Server wraps net/http with porta's middleware + pre-registered /health.
Mux is exported so B4b (MCP) and B4c (htmx UI) can register additional
routes on it; the allowlist middleware wraps the whole tree via the
http.Server.Handler. New rejects bad CIDR up front. Run blocks until
ctx.Done() or listener error; on cancel performs a 5-second graceful
Shutdown then returns nil (port-in-use and similar real errors
propagate).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: `Execute` wraps `signal.NotifyContext` (closes B3 issue #2)

**Files:**
- Modify: `internal/portacli/root.go`

This task has no new unit test of its own — the existing `TestRunMonitorFollowExitsOnCancel` in `internal/portacli/monitor_test.go` already verifies the `runMonitor` cancel path. The Execute change is a thin glue that hands the cancellable context to cobra; verifying it requires a real process+signal harness which is fragile. Acceptance is the manual smoke at the bottom of this task.

- [ ] **Step 4.1: Read the current `Execute`**

Run: `grep -n 'func Execute' internal/portacli/root.go`

Expected: shows `Execute()` returns `NewRootCmd().Execute()` — the bare form.

- [ ] **Step 4.2: Replace `Execute` to wrap `signal.NotifyContext`**

Open `internal/portacli/root.go` and replace the existing imports + `Execute` function:

```go
// (top of file — replace the import block)
package portacli

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/davidg238/porta/internal/store"
	"github.com/spf13/cobra"
)

// (... existing dbPath var, nowSec, openStore, NewRootCmd unchanged ...)

// Execute runs the porta CLI. The root context is cancelled on
// SIGINT/SIGTERM so long-running subcommands (serve, monitor --follow)
// can exit cleanly. Closes the gap from porta/porta#2 — until this
// change, runMonitor's --follow cancel path was unreachable in
// production because cmd.Context() was context.Background().
func Execute() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return NewRootCmd().ExecuteContext(ctx)
}
```

(Note: `time` is already imported in this file — keep it. The new imports are `context`, `os`, `os/signal`, `syscall`.)

- [ ] **Step 4.3: Build to confirm it compiles**

Run: `go build ./...`
Expected: clean.

- [ ] **Step 4.4: Run the existing monitor-follow cancel test to confirm it still passes**

Run: `go test ./internal/portacli/ -run TestRunMonitorFollowExitsOnCancel -v -race`
Expected: PASS (the test wires its own `context.WithCancel`, so this change doesn't alter its behavior; the test exists as a regression guard for the production path that now becomes reachable).

- [ ] **Step 4.5: Manual smoke (optional but recommended)**

Run: `go build ./cmd/porta && ./porta serve --port 6970` (use a non-default port so it doesn't collide with anything).
Press ctrl-C.
Expected: the process exits within ~1s (vs the pre-change SIGKILL). You may see a startup line and a clean shutdown. (`--http-port` flag doesn't exist yet — that's T5; the serve cmd still runs UDP-only here.)

- [ ] **Step 4.6: Commit**

```bash
git add internal/portacli/root.go
git commit -m "$(cat <<'EOF'
feat(porta): cli — Execute wraps signal.NotifyContext (closes #2)

Until now, cmd.Context() resolved to context.Background() in production,
so runMonitor --follow's ctx.Canceled→nil branch was dead code (ctrl-C
just SIGKILL'd the process). Wraps the root context with
signal.NotifyContext(SIGINT, SIGTERM) and uses ExecuteContext so cobra
threads it through to every RunE. Existing
TestRunMonitorFollowExitsOnCancel covers the inner loop; manual smoke
on serve confirms the outer signal handling.

Lands here (in B4a) rather than as a standalone fix because B4a's HTTP
listener needs the same graceful-cancel plumbing — single change buys
both.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: Wire HTTP listener into `porta serve`

**Files:**
- Modify: `internal/portacli/serve.go`
- Create: `internal/portacli/serve_test.go`

The current `serve.go` runs the UDP listener inline. T5 refactors it so:
- UDP runs in a goroutine that takes the cobra ctx and exits cleanly on cancel.
- HTTP listener runs in a parallel goroutine via `httpsrv.New(...).Run(ctx)` when `--http-port > 0`.
- `RunE` selects on either listener's error or ctx.Done.

- [ ] **Step 5.1: Read the current `serve.go` for orientation**

Run: `cat internal/portacli/serve.go`

You'll see: a `newServeCmd` whose `RunE` opens the store, builds `tftp.NewServer().SetDispatcher(handler.New(st, nowSec))`, calls `net.ListenPacket("udp", addr)`, and hands the conn to a local `serveUDP(conn, srv)` helper that loops on `conn.ReadFrom(buf)` and writes back replies. The refactor preserves the same UDP loop body but: (a) accepts a `ctx`, (b) closes the conn from a sidekick goroutine when ctx fires, (c) treats `net.ErrClosed` from `ReadFrom` as clean shutdown.

- [ ] **Step 5.2: Write the failing test (`serve_test.go`)**

```go
// internal/portacli/serve_test.go
package portacli

import (
	"testing"
)

func TestDefaultAllowCIDRHasRFC1918AndLoopback(t *testing.T) {
	got := defaultAllowCIDR()
	want := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"127.0.0.0/8",
		"::1/128",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("entry %d = %q, want %q", i, got[i], w)
		}
	}
}

func TestDefaultAllowCIDRReturnsFreshSlice(t *testing.T) {
	a := defaultAllowCIDR()
	b := defaultAllowCIDR()
	// Mutating one must not affect the other (cobra StringSliceVar
	// shares the backing slice across resets).
	a[0] = "mutated"
	if b[0] == "mutated" {
		t.Error("defaultAllowCIDR returned a shared slice; must return a fresh copy")
	}
}

func TestNewServeCmdRegistersFlags(t *testing.T) {
	cmd := newServeCmd()
	for _, name := range []string{"port", "http-port", "http-bind", "http-allow-cidr"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("flag --%s not registered", name)
		}
	}
}
```

- [ ] **Step 5.3: Run the test, see it fail**

Run: `go test ./internal/portacli/ -run "TestDefaultAllowCIDR|TestNewServeCmdRegistersFlags"`
Expected: FAIL with `undefined: defaultAllowCIDR` and/or missing flag.

- [ ] **Step 5.4: Replace `serve.go` body**

Open `internal/portacli/serve.go` and replace its full contents with:

```go
// internal/portacli/serve.go
package portacli

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"

	"github.com/davidg238/porta/internal/handler"
	"github.com/davidg238/porta/internal/httpsrv"
	"github.com/davidg238/porta/internal/tftp"
	"github.com/spf13/cobra"
)

// defaultAllowCIDR returns the RFC1918 + loopback set, as a fresh slice
// per invocation (cobra StringSliceVar shares the backing slice across
// resets — see TestDefaultAllowCIDRReturnsFreshSlice).
func defaultAllowCIDR() []string {
	return []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"127.0.0.0/8",
		"::1/128",
	}
}

func newServeCmd() *cobra.Command {
	var port, httpPort int
	var httpBind string
	var httpAllowCIDR []string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the porta gateway: UDP/TFTP listener + optional HTTP operator surface",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()

			// UDP listener (preserves the existing serveUDPLoop; ctx
			// cancels via conn.Close from a sidekick goroutine, and
			// net.ErrClosed from ReadFrom = clean shutdown).
			udpAddr := fmt.Sprintf(":%d", port)
			udpConn, err := net.ListenPacket("udp", udpAddr)
			if err != nil {
				return err
			}
			defer udpConn.Close()
			tftpSrv := tftp.NewServer()
			tftpSrv.SetDispatcher(handler.New(st, nowSec))
			log.Printf("porta: serving TFTP on udp %s (db=%s)", udpAddr, dbPath)
			udpErr := make(chan error, 1)
			go func() { udpErr <- serveUDPLoop(ctx, udpConn, tftpSrv) }()

			// HTTP listener (B4a, optional).
			httpErr := make(chan error, 1)
			if httpPort > 0 {
				srv, err := httpsrv.New(httpsrv.Config{
					Bind:      httpBind,
					Port:      httpPort,
					AllowCIDR: httpAllowCIDR,
				}, st)
				if err != nil {
					return err
				}
				go func() { httpErr <- srv.Run(ctx) }()
				log.Printf("porta: serving HTTP on %s:%d", httpBind, httpPort)
			} else {
				close(httpErr) // make the select symmetric — nil receive
			}

			// Either listener exiting OR ctx cancellation completes the
			// command. Errors from either propagate. A clean HTTP exit
			// while UDP is still running falls through to wait on UDP.
			select {
			case err := <-udpErr:
				return err
			case err := <-httpErr:
				if err == nil && httpPort > 0 {
					return <-udpErr
				}
				return err
			case <-ctx.Done():
				return nil
			}
		},
	}
	cmd.Flags().IntVar(&port, "port", 6969, "UDP/TFTP port")
	cmd.Flags().IntVar(&httpPort, "http-port", 6970, "operator HTTP port (0 = disabled)")
	cmd.Flags().StringVar(&httpBind, "http-bind", "0.0.0.0", "operator HTTP bind address")
	cmd.Flags().StringSliceVar(&httpAllowCIDR, "http-allow-cidr",
		defaultAllowCIDR(),
		"CIDR ranges allowed to reach the operator HTTP listener (repeatable; empty = serve any peer)")
	return cmd
}

// serveUDPLoop reads TFTP packets and writes the server's replies back to
// the peer. Mirrors the pre-B4a serveUDP body, with two additions: a
// sidekick goroutine that closes the conn when ctx fires, and clean-exit
// recognition for the net.ErrClosed that results.
func serveUDPLoop(ctx context.Context, conn net.PacketConn, srv *tftp.Server) error {
	go func() {
		<-ctx.Done()
		conn.Close()
	}()
	buf := make([]byte, 2048)
	for {
		n, peer, err := conn.ReadFrom(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		pkt := make([]byte, n)
		copy(pkt, buf[:n])
		for _, reply := range srv.HandlePacketFrom(pkt, peer.String()) {
			if _, err := conn.WriteTo(reply, peer); err != nil {
				log.Printf("porta: WriteTo(%s): %v", peer, err)
			}
		}
	}
}
```

The function is renamed `serveUDP` → `serveUDPLoop` so the change is obvious and grep-able. The pre-B4a body is preserved verbatim inside the loop; only the cancel sidekick and `net.ErrClosed` clean-exit are added.

- [ ] **Step 5.5: Run the tests**

Run: `go test ./internal/portacli/ -run "TestDefaultAllowCIDR|TestNewServeCmdRegistersFlags" -v`
Expected: PASS.

- [ ] **Step 5.6: Run the full Go suite under -race**

Run: `go build ./... && go vet ./... && go test -race ./...`
Expected: every package green. **If B1's existing serve-related tests break, the refactor altered behavior — back up and fix.**

- [ ] **Step 5.7: Manual smoke**

Terminal 1:
```bash
./porta serve --port 6969 --http-port 6970
```
Expected output:
```
udp: listening on :6969
http: listening on 0.0.0.0:6970
```

Terminal 2 (LAN access):
```bash
curl -s http://127.0.0.1:6970/health
```
Expected: `{"nodes":0,"status":"ok"}\n` (or similar JSON, key order may vary).

Terminal 2 (port disabled mode):
```bash
./porta serve --port 6969 --http-port 0
```
Expected: only `udp: listening on :6969` — no HTTP line.

Terminal 2 (CIDR denial — requires a non-RFC1918 peer; skip if you don't have one handy):
```bash
# From a host outside the allowlist:
curl -s -o /dev/null -w '%{http_code}\n' http://<gw-ip>:6970/health
# Expected: 403
```

Ctrl-C the gateway → process exits within ~1s (both listeners shut down).

- [ ] **Step 5.8: Run the parked Toit regression**

Run: `cd examples/toit-gateway && ./run-host-tests.sh && cd -`
Expected: 11/11 PASS (parity invariant unaffected by B4a; this is the routine guard from B1/B2/B3).

- [ ] **Step 5.9: Commit**

```bash
git add internal/portacli/serve.go internal/portacli/serve_test.go
git commit -m "$(cat <<'EOF'
feat(porta): cli — serve adds HTTP listener alongside UDP (B4a)

porta serve now runs two parallel listeners under the cobra root ctx:
the existing UDP/TFTP loop on --port, and the new operator HTTP listener
(httpsrv.Server) on --http-port. Defaults: 6969 UDP, 6970 HTTP, bind
0.0.0.0, RFC1918+loopback CIDR allowlist. --http-port 0 disables the
HTTP listener entirely (UDP-only mode parity with pre-B4 behavior).

UDP loop now takes a ctx; a sidekick goroutine closes the packet conn
when ctx fires so the read loop returns net.ErrClosed (treated as clean
shutdown). HTTP listener uses httpsrv.Server.Run which performs a 5s
graceful Shutdown on ctx.Done. Either listener exiting (clean or error)
also unwinds the other.

This composes with Task 4's signal.NotifyContext wiring: ctrl-C now
cancels both listeners cleanly. With B4a in place, B4b (MCP) and B4c
(htmx UI) just register routes on srv.Mux.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Self-Review

**1. Spec coverage** (walking §1–§8 of the spec):
- §2 (new CLI surface — `--http-port`, `--http-bind`, `--http-allow-cidr`, RFC1918 default, `--http-port 0` disables): T5 covers all.
- §3 architecture (single-process model, separate `internal/httpsrv` package): T1-T3 build the package; T5 wires the process model.
- §4.1 `Server`/`Config`/`New`/`Run`: T3.
- §4.2 `AllowCIDR` + `AllowlistMiddleware`: T1.
- §4.3 `/health` handler with `nodes:-1` on store error: T2.
- §4.4 `Execute` wraps `signal.NotifyContext`: T4.
- §4.5 `serve.go` refactor (UDP+HTTP under root ctx): T5.
- §5.1 empty-CIDR semantics (filter empties): T1 includes a dedicated test (`TestAllowCIDRFiltersEmptyAndWhitespace`).
- §5.2 IPv4-mapped IPv6 normalization: T1 includes `TestAllowlistMiddlewareIPv4MappedIPv6Matches`.
- §5.3 UDP listener cancellation: T5 `serveUDP` does the sidekick-close-conn pattern.
- §5.4 5-second graceful shutdown timeout: T3 `Run` body.
- §5.5 failure semantics: T3 + T5 cover all three error paths (CIDR parse, port-in-use, listener error).
- §5.6 no TLS: not implemented, correct.
- §6.1 unit tests for cidr/health/server: T1+T2+T3.
- §6.2 CLI flag wiring + `defaultAllowCIDR()`: T5 (`serve_test.go`).
- §6.3 acceptance gate: T5.6–T5.8.
- §7 out of scope: nothing in the plan crosses into MCP/htmx/auth/TLS.

No gaps.

**2. Placeholder scan:**
- T5 calls out a non-trivial uncertainty around `tftp.Server` API shape (`Listen` vs `ServePacketConn`). This is genuine — the current code may need a small extension. The "Scaffold A / Scaffold B" treatment + "mark BLOCKED if neither exists" makes the action explicit rather than vague. Implementer can adapt without guessing.
- No "TBD", "handle edge cases", "similar to Task N", or "add appropriate error handling" anywhere else.
- Every commit message is spelled out verbatim.
- Every test code block is complete.

**3. Type consistency:**
- `Config{Bind, Port, AllowCIDR}` used in T3 New + T5 newServeCmd — consistent.
- `Server{http, Mux}` defined in T3, no later reference renames.
- `healthHandler(st *store.Store) http.HandlerFunc` consistent T2 → T3 (Step 3.3 `mux.Handle("/health", healthHandler(st))`).
- `AllowCIDR(raw []string) ([]*net.IPNet, error)` consistent T1 → T3 (Step 3.3 `nets, err := AllowCIDR(cfg.AllowCIDR)`).
- `AllowlistMiddleware(allowed []*net.IPNet) func(http.Handler) http.Handler` consistent T1 → T3 (Step 3.3 `Handler: AllowlistMiddleware(nets)(mux)`).
- `defaultAllowCIDR() []string` defined T5, no other consumer.
- `serveUDP(ctx context.Context, st *store.Store, port int) error` defined T5, only called within T5.

No mismatches.
