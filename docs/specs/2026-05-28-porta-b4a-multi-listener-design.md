# porta Go core — B4a multi-listener serve (design)

**Status:** approved-pending-self-review, ready for implementation plan
**Sub-project:** B4a of the Go-mainline renovation (the first of three B4 slices).
**Parent (umbrella):** B4 = operator surface (MCP + htmx UI). Decomposed into:
B4a (this spec, multi-listener foundation) → B4c (htmx UI, ships next) → B4b
(MCP read surface, ships last). Original brainstorm kickoff at
`docs/brainstorms/2026-05-28-porta-b4-operator-surface-kickoff.md`
(historical, branch `chore/b4-brainstorm-kickoff`); answers locked 2026-05-28.

**Charter:** add an HTTP listener to `porta serve` alongside the existing
UDP/TFTP listener, with IP-allowlist middleware, a `/health` endpoint, and
graceful SIGINT/SIGTERM shutdown. No MCP tools, no UI yet — just the
plumbing that B4b and B4c register routes on. Closes the SIGINT gap from
B3 issue #2 as a side-effect.

## 1. What B4a does *not* change

- **No schema migration.** B4a is server-plumbing only; reads no tables
  beyond `nodes` (for the `/health` count).
- **No wire protocol change.** The device wire (`docs/PROTOCOL.md`) is
  untouched. B4a's HTTP surface is operator-facing only and is **not** part
  of the canonical wire — it is implementation-defined.
- **No CLI surface change beyond `serve`.** All other cobra commands stay
  put.
- **No new dependencies.** The `net/http`, `os/signal`, `syscall`,
  `context`, and `net` standard-library packages already cover everything
  B4a needs.

## 2. New CLI surface

The `porta serve` command grows three flags. Defaults make the listener
LAN-accessible on a free-floating port, allow private RFC1918 ranges, and
keep the existing UDP listener behavior identical.

| Flag | Default | Meaning |
|---|---|---|
| `--http-port` | `6970` | TCP port for the operator HTTP listener. |
| `--http-bind` | `0.0.0.0` | Listener bind address. Use `127.0.0.1` to localhost-only. |
| `--http-allow-cidr` | RFC1918 set (see below) | Repeatable. Peer must match one CIDR to be served; otherwise → 403. |

**RFC1918 default set:** `["10.0.0.0/8","172.16.0.0/12","192.168.0.0/16","127.0.0.0/8","::1/128"]`.

Operator can pass `--http-allow-cidr` one or more times to **replace** the
default set entirely (no implicit additions). Empty value = "disable
allowlist, serve any peer" — the explicit opt-in for a public-facing
deployment.

`--http-port 0` disables the HTTP listener (UDP-only mode, parity with
pre-B4 behavior). Useful for the gw85224-01 deploy if the operator wants
to keep MCP/UI off that host.

## 3. Architecture

```
internal/
  httpsrv/                NEW package — operator HTTP listener foundation
    server.go               Server type; New, Run; graceful Shutdown
    server_test.go
    cidr.go                 CIDR parsing + Allowlist middleware
    cidr_test.go
    health.go               /health handler
    health_test.go
  portacli/
    serve.go              MOD — adds --http-* flags, starts httpsrv alongside UDP
    root.go               MOD — Execute() wraps signal.NotifyContext
```

### Single-process model

`porta serve` runs three concurrent goroutines under one root context:

1. **UDP listener** — existing TFTP/UDP loop (`internal/tftp.Server`).
2. **HTTP listener** — new `httpsrv.Server.Run` (mux with allowlist
   middleware + `/health`).
3. **Signal handler** — `signal.NotifyContext(ctx, SIGINT, SIGTERM)`
   cancels the root context when either signal arrives.

Either listener's exit OR a signal cancels the root context. The other
listener then drains via its own `context.Done()`-driven shutdown. The
process exits zero on clean shutdown, non-zero on listener error.

### Why a separate `internal/httpsrv` package

- B4b (`internal/mcp/`) and B4c (`internal/web/`) will each call
  `s.Mux.Handle(...)` to register their routes on the **same** mux. The
  middleware (allowlist) wraps *every* registered route uniformly.
- `internal/portacli/serve.go` stays focused on flag wiring + lifecycle —
  it should not also hold middleware logic.
- Tests for middleware, `/health`, and lifecycle live in one isolated
  package with no cobra dependency.

## 4. New code surface

### 4.1 `internal/httpsrv/server.go` — listener lifecycle

```go
// Package httpsrv is the porta gateway's operator HTTP listener — the
// foundation that B4b (MCP) and B4c (htmx) register routes on. Provides
// IP allowlist middleware, /health, and graceful shutdown. No app routes
// live here; consumers do s.Mux.Handle("/<path>", handler).
package httpsrv

import (
    "context"
    "net/http"
    "time"

    "github.com/davidg238/porta/internal/store"
)

// Config holds the operator HTTP listener configuration. Bind+Port match
// the cobra --http-bind/--http-port flags; AllowCIDR mirrors --http-allow-cidr.
type Config struct {
    Bind      string   // e.g. "0.0.0.0" or "127.0.0.1"
    Port      int      // 0 means disabled (caller should not construct a Server)
    AllowCIDR []string // empty = serve any peer (no allowlist)
}

// Server wraps the http.Server with porta-specific plumbing. Mux is the
// shared mux that consumer packages register routes on; the allowlist
// middleware wraps every route uniformly.
type Server struct {
    http *http.Server
    Mux  *http.ServeMux
}

// New constructs a Server with the allowlist middleware applied to a
// fresh mux. The /health route is pre-registered; consumer packages
// (mcp, web) register additional routes via s.Mux.Handle.
func New(cfg Config, st *store.Store) (*Server, error)

// Run starts the listener and blocks until ctx is cancelled or the
// listener errors. On ctx cancel, performs a graceful Shutdown with a
// 5-second timeout.
func (s *Server) Run(ctx context.Context) error
```

`Run` implementation sketch:

```go
func (s *Server) Run(ctx context.Context) error {
    errCh := make(chan error, 1)
    go func() { errCh <- s.http.ListenAndServe() }()
    select {
    case <-ctx.Done():
        shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        _ = s.http.Shutdown(shutCtx)
        return nil
    case err := <-errCh:
        if err == http.ErrServerClosed {
            return nil
        }
        return err
    }
}
```

### 4.2 `internal/httpsrv/cidr.go` — allowlist middleware

```go
// AllowCIDR parses raw CIDR strings into *net.IPNet, returning an error
// on any malformed entry. An empty input returns nil — the caller treats
// nil as "no allowlist, serve any peer".
func AllowCIDR(raw []string) ([]*net.IPNet, error)

// AllowlistMiddleware returns http middleware that rejects with 403 any
// request whose peer IP is not in allowed. nil allowed = serve any peer.
// Looks at r.RemoteAddr; behind a proxy you'd need X-Forwarded-For
// support (out of scope — B4a is bind-to-LAN-direct).
func AllowlistMiddleware(allowed []*net.IPNet) func(http.Handler) http.Handler
```

Peer-IP extraction: split `r.RemoteAddr` on `":"`-from-the-right (handle
IPv6 brackets); `net.ParseIP(host)`; walk `allowed` for `Contains(ip)`.
On no match: `http.Error(w, "forbidden", 403)`.

### 4.3 `internal/httpsrv/health.go` — `/health` endpoint

```go
// healthHandler returns 200 with a small JSON body summarizing the
// gateway's state: status, db path (best-effort — empty if unavailable),
// and current registered-nodes count.
//
//   {"status":"ok","nodes":<int>}
//
// Used by uptime monitors and the future ops dashboard for liveness.
func healthHandler(st *store.Store) http.HandlerFunc
```

Body uses `encoding/json`. `st.ListNodes()` failure → `nodes: -1` (still
200, because the listener itself is healthy even if the DB blip transient).

### 4.4 `internal/portacli/root.go` — `Execute` wraps signal.NotifyContext

```go
// Execute runs the porta CLI. Wraps a root context with SIGINT/SIGTERM
// handlers so long-running subcommands (serve, monitor --follow) exit
// cleanly on ctrl-C. Closes the gap from issue #2.
func Execute() error {
    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer stop()
    return NewRootCmd().ExecuteContext(ctx)
}
```

New imports: `context`, `os`, `os/signal`, `syscall`. Side effect: `porta
monitor -f` now exits cleanly on ctrl-C (closes issue #2 inline; no
separate fix needed). The existing `runMonitor` test for ctx cancellation
continues to pass — the test already exercises the cancel path with a
`context.WithCancel`.

### 4.5 `internal/portacli/serve.go` — wires the HTTP listener in

The existing `newServeCmd` runs the UDP listener as a blocking call. After
B4a it runs UDP and HTTP as parallel goroutines under the cobra ctx, and
returns when either completes.

```go
func newServeCmd() *cobra.Command {
    var port, httpPort int
    var httpBind string
    var httpAllowCIDR []string
    cmd := &cobra.Command{
        Use:   "serve",
        Short: "Run the porta gateway: UDP/TFTP listener (+ optional HTTP operator surface)",
        RunE: func(cmd *cobra.Command, _ []string) error {
            ctx := cmd.Context()
            st, err := openStore()
            if err != nil { return err }
            defer st.Close()

            // UDP listener (existing).
            udpErr := make(chan error, 1)
            go func() { udpErr <- serveUDP(ctx, st, port) }()

            // HTTP listener (B4a, optional).
            httpErr := make(chan error, 1)
            if httpPort > 0 {
                srv, err := httpsrv.New(httpsrv.Config{
                    Bind: httpBind, Port: httpPort, AllowCIDR: httpAllowCIDR,
                }, st)
                if err != nil { return err }
                go func() { httpErr <- srv.Run(ctx) }()
                log.Printf("http: listening on %s:%d", httpBind, httpPort)
            } else {
                httpErr <- nil // no-op
            }

            select {
            case err := <-udpErr:
                return err
            case err := <-httpErr:
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

// defaultAllowCIDR returns the RFC1918 + loopback set, as a fresh slice
// per invocation (cobra StringSliceVar shares the backing slice across
// resets).
func defaultAllowCIDR() []string {
    return []string{
        "10.0.0.0/8",
        "172.16.0.0/12",
        "192.168.0.0/16",
        "127.0.0.0/8",
        "::1/128",
    }
}
```

The pre-B4 `serveUDP` was a blocking inline closure inside `RunE`. It is
factored out to `func serveUDP(ctx context.Context, st *store.Store, port
int) error` and accepts a context so it can exit cleanly on cancel. (The
existing UDP loop reads in a tight `for` — add a `context.Done()` check
between reads, or close the listener on ctx.Done from a sidekick
goroutine. Implementation detail; see §5.3.)

## 5. Implementation details that matter

### 5.1 Empty `--http-allow-cidr` semantics

Operator can disable the allowlist by passing `--http-allow-cidr=""`
(empty value). Cobra's `StringSliceVar` parses that as a one-element
slice `[""]`, not an empty slice. `AllowCIDR` must therefore filter
empty / whitespace-only strings before calling `net.ParseCIDR` — both to
support the "disable" case cleanly and to be lenient about a trailing
comma in a multi-value flag (`--http-allow-cidr "10.0.0.0/8,"`).

After filtering: zero CIDRs surviving = "serve any peer".
`AllowlistMiddleware(nil)` and `AllowlistMiddleware([]*net.IPNet{})`
both mean "no allowlist"; the middleware skips the loop when
`len(allowed) == 0`.

### 5.2 IPv4-in-IPv6 normalization

`net.ParseIP("::ffff:192.168.1.1")` returns an IPv4-mapped IPv6 address;
`netip.Addr.As4()` / `ip.To4()` normalizes it. The allowlist match must
work for both `192.168.0.0/16` and a `::ffff:192.168.1.1` peer.
Implementation: after `net.ParseIP(host)`, if `ip.To4() != nil`, use that
4-byte form for the `Contains` check; otherwise use the 16-byte form.

### 5.3 UDP listener cancellation

The current `internal/tftp.Server.Listen` blocks on `ReadFrom`. To exit
on ctx cancel, the wrapper goroutine starts a watchdog that closes the
packet-conn when ctx fires:

```go
func serveUDP(ctx context.Context, st *store.Store, port int) error {
    conn, err := net.ListenPacket("udp", fmt.Sprintf(":%d", port))
    if err != nil { return err }
    go func() { <-ctx.Done(); conn.Close() }()
    // existing server loop, with a clean exit when conn.ReadFrom returns
    // net.ErrClosed (treated as "shutting down", returns nil).
    return serveLoop(conn, st)
}
```

`net.ErrClosed` from `ReadFrom` is the cancel signal; treat as clean
exit. Any other error returns from `serve` and trips the `select` in
`RunE`.

### 5.4 Graceful shutdown timeout

`Server.Run` uses a 5-second `Shutdown` timeout (fixed). This is the
hard cap on in-flight requests after ctx cancel. After 5s, in-flight
handlers are abandoned. Long-running SSE streams from B4c will need to
respect ctx.Done() themselves — that's a B4c concern.

### 5.5 Failure semantics on listener errors

- UDP `net.ListenPacket` failure → `serveUDP` returns error → `RunE`
  returns it → cobra exits non-zero. No HTTP listener was started yet, so
  no leak.
- HTTP `New` failure (CIDR parse error) → `RunE` returns error before
  starting either listener → cobra exits non-zero. UDP goroutine is
  cancelled by ctx (since `defer stop()` in `Execute` cancels on return).
- HTTP `ListenAndServe` failure (port in use) → `httpErr` channel receives;
  `RunE` returns it. The UDP goroutine's `ctx` is then cancelled via
  `Execute`'s `defer stop()`, so UDP shuts down cleanly.

### 5.6 No TLS in B4a

HTTP only. TLS is a defer (B4 follow-up or sub-project D). For
single-operator LAN use, an SSH tunnel + plain HTTP is the standard
shape. If TLS lands later, it's a `Server.New` config switch + cert
material — orthogonal to the allowlist/mux structure.

## 6. Testing strategy

### 6.1 Unit (`internal/httpsrv/`)

- `cidr_test.go` —
  - `AllowCIDR` parses RFC1918 set + IPv6 (`::1/128`); rejects garbage
    (`"not-a-cidr"`).
  - `AllowlistMiddleware`:
    - empty allowed → every request 200.
    - `[192.168.0.0/16]` → request from `192.168.1.5:1234` → 200; from
      `10.0.0.1:1234` → 403.
    - IPv4-mapped IPv6 peer (`::ffff:192.168.1.5`) → matches
      `192.168.0.0/16`.
    - Malformed `r.RemoteAddr` → 403 (defense).
- `health_test.go` —
  - GET `/health` returns 200 with `Content-Type: application/json`.
  - Body is valid JSON with `status:"ok"` and integer `nodes`.
  - With 3 nodes in the store → `nodes:3`.
  - `st.ListNodes` injected to error → `nodes:-1` (handler still 200).
- `server_test.go` —
  - `New(cfg, st)` registers `/health` on the mux.
  - `Run(ctx)` starts on a random port (test config), serves `/health`
    successfully, exits within 100ms of `cancel()`.
  - `Run(ctx)` with a port-in-use returns the listener error.

### 6.2 CLI flag wiring (`internal/portacli/serve_test.go` — new file)

Scope limited to flag wiring + the default-CIDR helper. Full integration
of the `serve` cobra command (binding real sockets, running both
listeners, asserting end-to-end) is fragile and is covered instead by the
manual acceptance step in §6.3.

- `defaultAllowCIDR()` returns the 5-entry RFC1918+loopback set.
- `newServeCmd` registers the flags `--port`, `--http-port`,
  `--http-bind`, `--http-allow-cidr` with the documented defaults.
- `cmd.SetArgs(["--http-port", "0"]); cmd.ParseFlags()` resolves to
  `httpPort == 0`.

### 6.3 Acceptance gate

- `go build ./... && go vet ./... && go test -race ./...` green.
- Manual: `./porta serve` in one terminal; `curl
  http://127.0.0.1:6970/health` → 200 JSON. `curl
  http://<lan-ip>:6970/health` from a LAN host → 200. `curl
  http://<gw-ip>:6970/health` from outside RFC1918 (e.g. via a
  vpn-routed peer) → 403. SIGINT (ctrl-C) → both UDP and HTTP listeners
  exit within 5s.

## 7. Out of scope (explicit)

- **MCP tools** — B4b.
- **htmx operator UI** — B4c.
- **TLS / cert provisioning** — defer.
- **Bearer tokens / auth subsystem** — defer.
- **`X-Forwarded-For` / reverse-proxy support** — assume direct LAN
  exposure; revisit when a deployment puts porta behind nginx.
- **Logging middleware** — Go's `http.Server` defaults emit nothing per
  request. Per-request logging can be added in B4b/B4c as routes land.
- **Metrics endpoint** — `/metrics` (Prometheus exposition) is a natural
  fit but out of scope; B4a's `/health` is sufficient for now.
- **Rate limiting** — single-operator LAN; not needed.
- **`porta http` standalone subcommand** — single-process per Q4; no
  need.

## 8. Files added or changed

```
internal/httpsrv/                NEW package
  server.go         + server_test.go
  cidr.go           + cidr_test.go
  health.go         + health_test.go
internal/portacli/serve.go       (adds --http-* flags + httpsrv start)
internal/portacli/root.go        (Execute wraps signal.NotifyContext)
docs/PROTOCOL.md                 (no change — HTTP is operator surface, not wire)
```

Total: 6 new files, 2 modified files.

## 9. References

- B3 issue [#2](https://github.com/davidg238/porta/issues/2) — SIGINT
  wiring, closed inline by B4a's `Execute` change.
- `docs/brainstorms/2026-05-28-porta-b4-operator-surface-kickoff.md` —
  the brainstorm doc; Q1-Q8 answers locked 2026-05-28.
- `cmd/st-devserver/main.go` — parked multi-listener pattern (UDP + TCP
  + HTTP under one process with `signal.Notify(SIGINT)`).
- `internal/st/mcpserver/server.go:144-148` — parked `mcp.NewSSEHandler`
  wiring on a mux; B4b will lift the pattern (Streamable HTTP variant).
- `docs/specs/2026-05-27-porta-go-core-data-plane-design.md` — B1 (the
  current `serve` baseline).

## 10. Open questions

None. All 8 brainstorm questions answered; 3 follow-ups (allowlist
default, package name, ship order) pinned 2026-05-28.
