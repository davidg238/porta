# porta B4c — htmx operator UI: design

**Status:** approved design (brainstormed 2026-05-28). Predecessor B4a (multi-listener
HTTP foundation) shipped at `aa25aa5`. This is the third piece of the B4 operator surface
(`porta-go-mainline-renovation`), and per the 2026-05-28 decision it ships **before** B4b
(MCP read surface).

## 1. Context

After B1–B3, porta is a cobra CLI + UDP/TFTP gateway with a sqlite store (`nodes`,
`command_queue`, `reports`, `payloads`, `data_log`) conforming to `docs/PROTOCOL.md`. B4a
added an optional operator HTTP listener to `porta serve` (`internal/httpsrv`): a shared
`http.ServeMux` behind a CIDR-allowlist middleware, with `/health` pre-registered and
graceful shutdown under the root context. Nothing renders on it yet.

B4c layers a **browser operator console** onto that listener so an operator can watch the
fleet and drive the gateway without the CLI. It is the first *greenfield* (non-parity)
operator surface; the parked `internal/st/debugui` (`html/template` + `//go:embed` + an SSE
`Hub`) is studied for patterns but not reused wholesale.

The defining constraint is the **node communication model**: Toit nodes are deep-sleep /
poll-based. A queued command is not delivered until the node's next wake (poll interval,
30s default but can be minutes), and its effect is not observed until the *report after
that*. There is no synchronous request/response. The UI is therefore **async-by-design**,
and it makes that reality unavoidable through a prominent next-check-in gauge.

## 2. Locked umbrella decisions (B4 brainstorm, 2026-05-28)

These were settled when B4 was decomposed and are inputs here, not open questions:

- MCP read-only first (B4b); **UI is full read + write + upload** (this spec).
- **Three pages:** `/` (node list), `/n/<id>` (per-node detail + forms + image upload),
  `/log` (command audit).
- Single `porta serve` process and a single HTTP listener (B4a).
- **LAN + CIDR-allowlist** binding — already enforced by B4a's `AllowlistMiddleware`; B4c
  adds no auth of its own.
- `kind` is **displayed but never branched on** — every path assumes `kind == "toit"`. ST
  re-enablement (sub-project D) adds branching later.
- Streamable HTTP for MCP — irrelevant to B4c (no MCP here).

## 3. Goals / non-goals

**Goals**
- Render the full read surface (fleet list, per-node detail, command audit) live.
- Drive the full write surface from the browser: `set`, `set-console`,
  `set-poll-interval`, `set-max-offline`, `rename`, container `install` (image upload) and
  `uninstall`.
- Make the deep-sleep/async cadence legible via a next-check-in gauge.
- No wire-protocol or store-schema changes; reuse the store's existing read computations
  (incl. desired-vs-observed config) and the existing command queue for writes.
- Single source of truth for write orchestration shared between CLI and web.

**Non-goals (B4c)**
- MCP tools (B4b). SSE / push / a broadcast Hub. Auth beyond B4a's allowlist. `kind`
  branching. Mobile-responsive layout (desktop browser is the target). Compile-and-deliver
  (`/tools/*`, sub-project C). Multi-operator / RBAC.

## 4. Architecture

Two new packages plus thin wiring; no changes to the wire or schema.

### 4.1 `internal/control` (new, cobra-free) — write orchestration

Today the write cores live in `internal/portacli/mutate.go` (`runDeviceSet`, `runInstall`,
`runUninstall`, `runSetPollInterval`, plus `store.SetMaxOffline` / `store.SetNodeName`
calls) and print to an `io.Writer`. They are extracted into a neutral package that returns
structured results so both the CLI and the web layer call the same logic:

```go
package control

// ResolveNodeID turns a <node> arg (MAC or friendly name) into a canonical
// node id — moved here (with isMAC) from portacli/resolve.go so both the CLI
// and web share one resolver. Web link targets use canonical ids, but /n/<id>
// also accepts a name/MAC via this helper.
func ResolveNodeID(st *store.Store, nodeArg string) (string, error)

// Each returns the enqueued command id (0 for store-only mutations) and an error.
func Set(st *store.Store, id, app, key string, v any, now int64) (cmdID int64, err error)
func SetConsole(st *store.Store, id string, on bool, now int64) (int64, error)
func SetPollInterval(st *store.Store, id string, secs, now int64) (int64, error)
func SetMaxOffline(st *store.Store, id string, secs int64) error            // store-only
func Rename(st *store.Store, id, name string) error                        // store-only
func Uninstall(st *store.Store, id, name string, now int64) (int64, error)
func Install(st *store.Store, id, name string, img io.Reader, opts InstallOpts, now int64) (cmdID int64, err error)
```

- `Install` accepts **bytes via `io.Reader`**, not a file path — it reads the image,
  computes CRC32-IEEE + size, calls `RegisterPayload`, builds the `run` command
  (triggers/runlevel/lifecycle from `InstallOpts`), and enqueues it. This fixes the
  CLI-only `os.ReadFile(path)` assumption so a browser multipart upload can use it
  directly. The CLI's `runInstall` becomes: read file → `control.Install(…, file, …)`.
- Type inference (`config.InferScalar`), command building (`internal/command`), and
  trigger parsing stay where they are; `control` only orchestrates.
- `portacli` is refactored to call `control.*` and keep its existing print lines. The
  existing portacli mutate tests move/extend to cover `control` (no behavior change).

### 4.2 `internal/web` (new) — the UI

- `html/template` + `//go:embed` for one template set (base layout + per-page bodies +
  htmx-swappable partials), plus ~100 lines of hand-rolled CSS embedded in the template.
  Zero external/runtime assets; fully offline. htmx itself is vendored as a single embedded
  `htmx.min.js` served from the package (no CDN).
- A `Handler` constructed with the store and a `control` dependency:

```go
func New(st *store.Store) *Handler
func (h *Handler) Register(mux *http.ServeMux)   // mounts /, /n/, /log, /assets, partial routes
```

- Reads go through `store` queries; writes go through `internal/control`. The handler holds
  no node state and no Hub — every dynamic region is re-fetched by polling.

### 4.3 Wiring

In `internal/portacli/serve.go`, when `--http-port > 0` (Server already constructed),
register the UI on the shared mux before `srv.Run`:

```go
web.New(st).Register(srv.Mux)
```

It inherits the allowlist middleware and graceful shutdown for free. `/health` stays.

## 5. Pages, routes, and partials

All dynamic regions are independent partials fetched with `hx-get` and
`hx-trigger="every 2s"`. A small embedded client-side 1s `setInterval` animates the
check-in gauges between polls (smooth countdown without hammering the server).

### 5.1 `/` — node list
Full page = base layout + nav (`Nodes` | `Command Log`) + the node-table partial.

- **Partial `GET /partials/nodes`** — dense table: status dot, name, `kind`, last-seen, IP
  (source addr), state summary (running app + cfg-key count, or `idle` / `offline`), and a
  **Next check-in** column.
- Each row links to `/n/<id>`.

### 5.2 `/n/<id>` — per-node detail (single scrolling page)
Full page = base layout + nav + six stacked sections, each its own partial:

1. **Header** (`GET /partials/n/<id>/header`) — identity (name, `kind`, IP, EUI/MAC,
   console on/off) + the **large check-in gauge**.
2. **Config** (`GET /partials/n/<id>/config`) — desired-vs-observed table with
   `converged` / `pending` / `drift` markers, computed exactly as the CLI `device get`
   does (config-self-heal + D5 echo). Read-only render.
3. **Telemetry** (`GET /partials/n/<id>/telemetry`) — last 10 `data_log` rows
   (ts, key, value, value_type).
4. **Pending commands** (`GET /partials/n/<id>/pending`) — undelivered `command_queue`
   rows for this node (id, verb, args, age).
5. **Actions** — forms (see §6) that POST and swap the *Pending commands* partial.
6. **Containers** (`GET /partials/n/<id>/containers`) — installed list + install
   (multipart upload) + uninstall (see §6).

`GET /n/<id>` resolves the node via `control.ResolveNodeID` (the shared resolver, §4.1);
unknown id → 404.

### 5.3 `/log` — command audit
Full page + **partial `GET /partials/log`**: reverse-chronological `command_queue` table
(id, node, verb, args, issued_by, queued_at, delivered_at), polled every 2s. Read-only.

## 6. Write forms (the async write surface)

Each form `hx-post`s to a route that calls `control.*` (enqueuing with
**`issued_by="web"`**) and returns the re-rendered affected partial plus a confirmation
line. Forms never block on the node.

- `POST /n/<id>/set` — app, key, value (value typed via `config.InferScalar`). → swap
  *Pending commands*.
- `POST /n/<id>/console` — `on|off`.
- `POST /n/<id>/poll-interval` — duration (parsed via `command.ParseDurationSeconds`).
- `POST /n/<id>/max-offline` — duration (store-only; refreshes header/gauge).
- `POST /n/<id>/rename` — new name (store-only; refreshes header + node list).
- `POST /n/<id>/containers/install` — **multipart**: file (`.bin` bytes) + name +
  trigger/interval/lifecycle/runlevel fields → `control.Install(reader, …)`. → swap
  *Containers* + *Pending commands*.
- `POST /n/<id>/containers/uninstall` — name → `control.Uninstall`.

**Confirmation framing** surfaces the async reality. A successful queue-write renders
e.g. `queued #412 — delivers on next check-in (~24m)`. The command then progresses in the
polled partials: *pending* → (next wake) gone from pending / *delivered* → (next report)
reflected in *Config* observed / *Telemetry*. This mirrors the gauge cadence so the
operator is never surprised that "nothing happened yet."

## 7. The next-check-in gauge

Derived **entirely gateway-side** from `last_seen` + `poll_interval` (+ `max_offline`); no
node-side support. A pure helper computes the state; the template renders a colored bar; a
1s client tick animates the countdown between 2s polls.

```
elapsed = now - last_seen
expected = poll_interval
if elapsed <= expected:        green,  fill = elapsed/expected,  label "every <interval> · next ~<expected-elapsed>"
else if elapsed <= max_offline: amber,  fill = 100%,             label "overdue <elapsed-expected>"
else:                           red,    fill = 100% (dimmed),    label "offline (> max-offline)"
```

The interval magnitude is shown numerically (`every 30m`) so the deep-sleep cadence is
"in your face": the operator immediately sees how long until a node can act on a command.
The gauge doubles as the online/offline indicator (status dot color = gauge color).

## 8. Live-update model

**htmx polling only.** Each partial carries `hx-trigger="every 2s"`; the gauge's client
tick is cosmetic. No SSE, no Hub, no broadcast plumbing. Rationale: reports arrive only
~every poll-interval, so polling latency is imperceptible, and the single-operator console
does not justify a streaming subsystem. SSE remains a clean future addition (register an
`/events` route on the same mux) if a real-time need ever appears.

## 9. Security / binding

No new auth. The UI is mounted on B4a's listener, so it is reachable only from peers
inside `--http-allow-cidr` (RFC1918 + loopback by default) and only when `--http-port > 0`.
Writes are as privileged as the CLI; that is acceptable under the same LAN-trusted,
single-operator model documented in `docs/specs/2026-05-24-security-transport-evolution-design.md`.
Standard hardening: forms are POST-only, multipart upload size-capped, templates use
`html/template` auto-escaping.

## 10. Testing

- **`internal/control`** — unit tests (largely inherited from the portacli mutate refactor):
  each verb enqueues the expected command with `issued_by` set; `Install` from a byte
  reader computes the correct CRC32-IEEE + size and registers the payload; store-only
  mutations (`SetMaxOffline`, `Rename`) persist.
- **`internal/web`** — `httptest` per route/partial: node table renders seeded nodes; each
  detail partial renders store fixtures; each form POST calls `control` and returns the
  swapped partial; multipart install path reads bytes and enqueues `run`; unknown node →
  404. The gauge state machine is a pure helper with table-driven tests (green / overdue /
  offline boundaries).
- **Integration** — `httptest.Server` over a seeded store: GET `/`, GET `/n/<id>`, POST a
  `set` form, POST a multipart install; assert the resulting `command_queue` rows
  (verb/args/`issued_by="web"`) and `payloads` entry.
- Existing portacli tests stay green after the `control` extraction (behavior-preserving).

## 11. Implementation tasks (for the plan; ~8)

1. Extract `internal/control` (write cores + `ResolveNodeID`/`isMAC`); refactor `portacli`
   to call it; `Install` accepts `io.Reader`. (Behavior-preserving; existing CLI tests
   green.)
2. `internal/web` scaffold: package, `//go:embed` template + CSS + vendored htmx, base
   layout + nav, `New`/`Register`, wire into `serve.go`.
3. `/` node list + `/partials/nodes` + the gauge helper and its rendering.
4. `/n/<id>` read sections: header+gauge, config (desired/observed), telemetry, pending.
5. Detail write forms: `set`, `console`, `poll-interval`, `max-offline`, `rename`
   (POST → `control` → partial swap + confirmation framing).
6. Container `install` (multipart upload) + `uninstall`.
7. `/log` command-audit page + `/partials/log`.
8. Integration test over `httptest.Server`; final wiring review.

## 12. References

- `internal/httpsrv/{server,health,cidr}.go` — the listener + allowlist B4c mounts on.
- `internal/portacli/mutate.go` — write cores being extracted to `internal/control`.
- `internal/portacli/inspect.go` / `resolve.go` — read queries + `resolveNodeID` reused.
- `internal/st/debugui/{handler,render}.go` + `static/debug.html` — parked
  `html/template` + `//go:embed` + htmx pattern (reference only).
- `docs/brainstorms/2026-05-28-porta-b4-operator-surface-kickoff.md` — Q1–Q8 + decomposition.
- `docs/specs/2026-05-28-porta-b4a-multi-listener-design.md` — predecessor.
- `docs/specs/2026-05-24-config-self-heal-design.md` + `…-d5-observed-config-echo-design.md`
  — the desired-vs-observed computation the Config section renders.
- `docs/PROTOCOL.md` — the wire contract (unchanged by B4c).
