# Control-plane HTTP API (S1) — server-as-workhorse, CLI-as-client

**Status:** approved design (2026-06-02). First sub-project (S1) of a larger
re-architecture: porta becomes a **workhorse server with a network API**, and the
CLI (and any future language tooling) becomes a **client** of that API.

## 1. Why — the architecture shift

Today every porta CLI command (`device set`, `container install`, `porta run`, …)
mutates state by **opening the SQLite db directly** (`openStore()` →
`store.Open("porta.db")`); `serve` opens the same db, and the two share it via WAL.
So the CLI is **co-located by design** — it talks to the server through the shared
db file, not over the network. There is **no HTTP write API**: the web POST
handlers call `internal/control` in-process, and the MCP surface is read-only.

That has three problems the operator flagged:

1. **Two paths.** Co-located CLI writes the db; a remote gateway (e.g.
   `gw85224-01`) can't be driven at all without SSH. Two transports, two code
   paths.
2. **Auditability.** Direct-db CLI access bypasses any central record. porta
   already stamps `issued_by` on commands and has a `/log` audit page, but that
   record is only trustworthy if **every** mutation flows through one writer.
3. **Polyglot future.** porta's end-state is one Go server owning the protocol +
   symmetric `/tools/{toit,smalltalk}` clients. The proven shape for this is
   st-zephyr's jast-gw: a Go gateway with a network listener, driven by a separate
   (Python `jast2`) CLI, with the language/compile work living **client-side** (the
   Smalltalk transpiler ran as its own service). A language-agnostic server API +
   the per-node `kind` seam is what lets a different CLI "plug in."

**The shift:** the server is the sole owner of the store and exposes a network API;
the CLI is a thin client; language/compile work happens client-side (the Phase-1
`internal/toolchain` already compiles+relocates locally). Co-located and remote
collapse to **one path** (HTTP to the server, localhost when co-located).

### Decomposition (this spec = S1 only)

- **S1 — Control-plane HTTP API (this spec):** server-side authenticated JSON
  endpoints over `internal/control` + `internal/store`, on the existing B4a
  listener. Writes + the reads a CLI needs. *Everything below depends on it.*
- **S2 — CLI becomes an API client:** `--server`/`$PORTA_SERVER`; default =
  localhost; mutating commands stop opening the db. Co-located = localhost HTTP.
- **S3 — `porta run` over the API:** compile/relocate locally (Phase-1 toolchain),
  deliver the built `.bin` + run command via S1; identity/SDK guard reads via S1.
- **S4 — Web identity/SDK visibility:** small, independent (chip/sdk header +
  SDK-match badge); can ship in parallel anytime.
- **S5 — Console tail over the API (`porta monitor`):** the server already ingests
  a node's console when forwarding is enabled (`set-console on` → the B3/data
  up-path), so a **streaming** read endpoint (SSE/chunked — the one thing S1
  excludes) lets `porta monitor -d <node>` stream the captured console in the CLI
  session. No USB / separate `jag monitor`; works against a *remote* gateway with
  no serial line. Its own sub-project because it needs streaming.
- **S6 — Client-side panic decode:** a Toit panic prints a base64 blob to serial;
  surfacing it is just S5's console tail. **Decoding** needs the image's
  `.snapshot` for `jag decode` to symbolicate — the existing panic-decode backlog
  was blocked because *porta stores only the relocated `.bin`, not the snapshot*.
  This architecture resolves it: compile is client-side and the Phase-1 toolchain
  already produces `app.snapshot` beside `app.bin`, so the **CLI retains the
  snapshot for images it deployed** (keyed by CRC) and runs `jag decode` locally
  against its own jag cache. Snapshots live with the client that built them — the
  server stays language-agnostic. Caveat: an image deployed outside that CLI (raw
  `.bin` web upload, another operator's machine) surfaces but can't auto-decode.
- **Future — Smalltalk CLI** plugs into the same S1 API.

Each of S2–S6 gets its own spec → plan → build cycle. S5/S6 are noted here so S1's
"no streaming" boundary is understood as deliberate (they need it; S1 doesn't).

## 2. Locked decisions (don't re-litigate)

- **API shape: uniform command envelope.** `POST /api/nodes/{sel}/commands` with
  `{verb, args}` → `{ok, data, error}` (mirrors jast-gw's `Request`/`Response`).
  New device verb = no new route. A separate multipart endpoint handles the binary
  image. Rationale: extensible, language-agnostic for the future Smalltalk CLI.
- **Transport: JSON over HTTP on the existing listener.** The same JSON envelope
  st-zephyr sent, but carried by porta's existing port-6970 B4a listener (web +
  MCP already live there), **not** a second TCP/JSON-lines socket. Reuses the CIDR
  auth, graceful shutdown, Slowloris protection, and multipart upload.
- **Auth: CIDR allowlist only, for now.** Writes inherit the same LAN+loopback
  allowlist the web UI and MCP rely on — no new secret to manage; the network is
  the trust boundary (as jast-gw relied on Tailscale). A bearer token can be added
  later if porta ever faces a less-trusted network.
- **Scope: writes + the CLI reads.** S1 is a complete CLI-facing API — mutation
  endpoints **and** the minimal reads CLI commands need (list/resolve nodes, node
  detail incl. identity, command log) — so S2 is a pure re-point.
- **No streaming in S1.** Because compile is **client-side**, the server never
  narrates a build; the `-v` narration is local CLI output. Every S1 endpoint is
  plain request/response — no SSE/chunked needed.
- **One writer.** apisrv is a thin adapter over `internal/control` + `store`; the
  web keeps calling control in-process. No business logic is duplicated, and the
  web is **not** refactored to consume the API in S1.

## 3. Architecture

New package `internal/apisrv`, a sibling of `internal/web` and `internal/mcpsrv`.
It registers JSON routes on the shared `httpsrv.Server.Mux` and is wired in
`serve.go` alongside web + MCP, inheriting the CIDR allowlist middleware uniformly.

```
                       httpsrv.Server (port 6970, CIDR allowlist)
                        ├─ /health         (httpsrv)
                        ├─ / , /n/… , /log  (web  → control.*  → store)   [HTML]
                        ├─ /mcp            (mcpsrv → store/control)        [read-only]
                        └─ /api/…          (apisrv → control.* → store)    [JSON]  ← NEW
```

apisrv holds a `*store.Store` (and uses the `control` package functions); it does
**not** own state. Presentation (JSON encoding) stays in apisrv; mutation logic
stays in control. This is the same boundary the package doc for `control` already
declares ("the cobra CLI and the web UI share one implementation … presentation
stays in the callers").

### Files (each one focused)

- `internal/apisrv/apisrv.go` — `Handler` struct, route registration, the response
  envelope helpers (`writeOK`/`writeErr`), `{sel}` node resolution.
- `internal/apisrv/commands.go` — the verb dispatch: decode `{verb, args}`, map to
  the matching `control.*` call, return the command id.
- `internal/apisrv/containers.go` — the multipart image-install handler (mirrors
  `web.postInstall`: `MaxBytesReader` cap → `control.Install`).
- `internal/apisrv/reads.go` — the GET handlers over `control` view-models + store.
- `internal/apisrv/*_test.go` — `httptest` table-driven tests.
- `internal/portacli/serve.go` — register apisrv on the mux (one line, alongside
  web + mcp).

## 4. Surface

`{sel}` is a node **selector** — an id (12-hex MAC) or a name — resolved server-side
(the same resolution the CLI's `-d` flag uses today).

### Writes

**`POST /api/nodes/{sel}/commands`** — Content-Type `application/json`.

```json
{ "verb": "set", "args": { "app": "sampler", "key": "interval", "value": 30 } }
```

`verb ∈ {set, set-console, set-poll-interval, set-power-mode, stop}` — the five
**image-less** verbs. `args` is verb-specific and decoded per verb:

| verb | args | control call |
|------|------|--------------|
| `set` | `{app, key, value}` | `control.Set` |
| `set-console` | `{state}` (`on`/`off`) | `control.SetConsole` |
| `set-poll-interval` | `{interval}` (e.g. `"30s"` or seconds int) | `control.SetPollInterval` |
| `set-power-mode` | `{mode}` | `control.SetPowerMode` |
| `stop` | `{name}` | `control.Uninstall` |

Response: `{ "ok": true, "data": { "command_id": 42 }, "error": "" }`.

> **No `run` verb here.** Running a container always needs the image: the protocol
> `run` command carries name + size + CRC32, and `control.Install` is the single
> operation that registers the payload *and* enqueues the run (there is no
> control function to re-run a previously-registered payload). The JSON envelope
> carries no file, so the run path is the multipart endpoint below; S3's
> `porta run` calls it. The exact `control` function names are taken from the real
> package at implementation time — the table is the mapping, not new signatures.
> (A future "re-run the CRC-cached payload without re-uploading" verb is feasible —
> the gateway retains payloads keyed by CRC — but is out of scope.)

**`POST /api/nodes/{sel}/containers`** — Content-Type `multipart/form-data`.
Parts: `image` (the relocated `.bin`), `name`, `lifecycle`, `runlevel`, `interval`,
`triggers`. Maps to `control.Install` (size + CRC32 computed server-side). Mirrors
`web.postInstall`, including the `http.MaxBytesReader` cap (reuse the `maxUpload`
constant value). Response: `{ ok, data:{ command_id, size }, error }`.

> The CRC32 is computed inside `control.Install` and stored in the queued `run`
> command's args (so the node verifies its download); `Install` returns only the
> command id, so the response carries `{command_id, size}` and the CRC is read back
> via `GET /api/nodes/{sel}/commands` rather than recomputed here.

**`PATCH /api/nodes/{sel}`** — Content-Type `application/json`. Node-management
settings, not device commands:

```json
{ "name": "new-name", "max_offline_s": 300 }
```

Both fields optional; apply whichever is present (rename / set-max-offline). Kept
separate from the command queue, mirroring the web's gw-settings-vs-actions split.
Response: `{ ok, data:{ }, error }`.

### Reads

- **`GET /api/nodes`** → `{ ok, data:{ nodes:[ {id, name, kind, ip, last_seen,
  online, chip, sdk} … ] }, error }`.
- **`GET /api/nodes/{sel}`** → node detail: identity (`chip`, `sdk`), observed apps,
  config desired-vs-observed rows, poll interval, online/check-in. Reuses the same
  `control` view-model functions the web detail page and MCP `device_status` use.
- **`GET /api/nodes/{sel}/commands`** → recent command log for the node
  (`{id, verb, args, state, issued_by, ts}` rows) for `porta log`.

## 5. Response schema & audit

- **Envelope:** every response is JSON `{ ok: bool, data: object|null, error: string }`
  (echoing jast-gw's `Response`), **plus** a meaningful HTTP status:
  - `200` success · `400` bad request (unknown/missing verb, bad args, bad
    duration) · `404` unknown node · `409` control-layer conflict · `413` upload
    too large.
  - `ok=false` always carries a human-readable `error`; `ok=true` always carries
    `data` (possibly empty object).
- **Audit:** every write stamps `issued_by = "api"` into the existing command-log,
  so `/log` and `GET …/commands` capture API-originated commands. (Today's direct
  CLI commands log `issued_by="cli"`; once S2 re-points the CLI they will log
  `"api"`.) Source IP may be added to the server log line. Per-caller identity is a
  later enrichment (would ride on the deferred bearer token).

## 6. Error handling

- Unknown/missing `verb`, missing required `args`, unparseable duration → `400`
  with `{ok:false,error}`.
- Unknown `{sel}` → `404`.
- A `control.*` error (validation, e.g. rejecting a daemon power-mode on a
  duty-cycle node) → `400`/`409` with the control error message passed through.
- Oversize multipart → `http.MaxBytesReader` makes `ParseMultipartForm` fail →
  `400`/`413` (same mechanism as `web.postInstall`).
- All handlers are panic-safe (recover → `500` `{ok:false}`), so one bad request
  never takes down the shared listener.

## 7. Testing

Host-only, no device and no real `toit` (same approach as Phase 1):

- **Command dispatch:** table-driven `httptest` — for each verb, POST the envelope
  and assert (a) the right command is queued (via `store.NextUndelivered`, exactly
  as Phase 1's `runDeploy` tests do) and (b) the response envelope/status.
- **Container install:** build a multipart body with canned `.bin` bytes; assert a
  `run` command is queued with the right size/CRC; assert the `MaxBytesReader` cap
  rejects an oversize part.
- **PATCH:** rename + max-offline apply; partial bodies.
- **Reads:** seed a node (incl. `UpdateNodeIdentity`), assert the JSON shape and
  that `chip`/`sdk` surface in list + detail.
- **Errors:** unknown verb, missing args, unknown node, bad duration.
- The CIDR allowlist itself is already covered by `httpsrv` tests and is inherited,
  not re-tested here.

## 8. Out of scope (S1 scope guard / YAGNI)

- The CLI client re-point (S2), `porta run` over the API (S3), the web
  identity/SDK visibility (S4) — separate sub-projects.
- Bearer-token auth; per-caller audit identity.
- Any streaming/SSE (compile is client-side; nothing to stream).
- Refactoring the web POST handlers to consume the API — they stay in-process
  calling `control` directly. (A later cleanup could unify them, but not in S1.)
- Read parity with MCP beyond what the CLI needs — S1 adds only the three reads
  above; MCP keeps its own surface.

## 9. Open implementation details to pin (not design risks)

- Exact `control.*` function names/signatures per verb (taken from the real
  package at implementation time; the §4 table is the mapping).
- Whether `{sel}` resolution reuses an existing `control`/store resolver or a small
  apisrv helper.
- The precise JSON field names for the read view-models (align with what the CLI
  will consume in S2; reuse `control` view structs where they already serialize
  cleanly).
