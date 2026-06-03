# CLI becomes an API client (S2) — writes over HTTP

**Status:** approved design (2026-06-02). Second sub-project (S2) of the
server-as-workhorse re-architecture (see
`docs/specs/2026-06-02-control-plane-api-design.md` for S1 and the S1–S6
decomposition). S1 shipped the control-plane HTTP API; S2 makes the CLI a **client**
of it for the mutating commands.

## 1. Why — finish the "one writer" invariant for writes

S1 added an authenticated JSON HTTP API (`internal/apisrv`) over
`internal/control` + `internal/store`. But the CLI still mutates state by **opening
the SQLite db directly** (`openStore()` → `store.Open`), bypassing the server. The
whole re-architecture exists to enforce one invariant: **every mutation flows
through the server (single writer → trustworthy audit).** That invariant is purely
about *writes* — a read never mutates state, so a read that still opens the db does
not violate it.

S2 therefore re-points the **8 mutating CLI commands** to POST/PATCH the S1 API,
fully achieving the invariant. Reads (`scan`, `ping`, `device show`, `device get`,
`container list`, `log`), `run`, and `monitor` are **deliberately deferred** to
later phases (they each need more work — reads need the `device get` arbitrary-app
gap closed; `monitor` needs a telemetry read endpoint, which is S5). The agreed
**end-state is everything-over-HTTP except `serve`**; S2 is the first, highest-value
slice of that, and we are explicitly *not* maintaining a fully working CLI mid-migration.

## 2. Locked decisions (don't re-litigate)

- **Scope = the 8 writes only.** `device set`, `device set-console`,
  `device set-power-mode`, `device set-poll-interval`, `device set-max-offline`,
  `device name`, `container install`, `container uninstall`. Everything else stays
  db-backed for now.
- **Transport.** The S1 endpoints, unchanged in shape:
  `POST /api/nodes/{sel}/commands`, `POST /api/nodes/{sel}/containers` (multipart),
  `PATCH /api/nodes/{sel}`. JSON `{ok,data,error}` envelope + HTTP status.
- **Server address.** New persistent flag `--server` + env `$PORTA_SERVER`, default
  `http://localhost:6970` (matches `serve`'s default `--http-port`). Only the 8 write
  commands consume it. The existing `--db` persistent flag stays for `serve` and the
  still-db-backed commands.
- **Selector passthrough.** The CLI no longer resolves `-d`/`--device` locally; it
  passes the raw selector (name or MAC) into the URL path and the **server**
  resolves it (`{sel}`). The CLI write path stops importing `store`/`control`.
- **Preserve `EnsureNode` (server-side).** Today each write command calls
  `st.EnsureNode(id)` before enqueuing, so a command can be pre-queued for a
  well-formed MAC that has never checked in (bench pre-provisioning). The S1 write
  handlers do **not** ensure the node. S2 adds `EnsureNode` to the apisrv **write**
  path so this behavior is preserved (reads stay non-creating).
- **`node_id` in write responses.** The three write responses gain a `node_id` field
  (the server-resolved 12-hex id) so CLI confirmation lines can still lead with the
  resolved MAC — keeping output identical *and* giving a useful resolution check
  (catches a typo'd/ambiguous name).
- **Auth unchanged.** Inherits the S1 CIDR allowlist; localhost when co-located.
- **Behavior change accepted:** the CLI now **requires `porta serve` to be running**
  to mutate (today it could queue into the db while serve was down). This is the
  point of "one path"; the client surfaces a friendly "is `porta serve` running?"
  error on a connection failure.

## 3. Architecture

```
  porta CLI (cobra)                       porta server (serve)
  ┌───────────────────┐   HTTP            ┌──────────────────────────┐
  │ write commands     │  POST/PATCH ────▶ │ apisrv → control → store │
  │  → internal/       │   {verb,args} /   │  (EnsureNode on write)   │
  │    apiclient       │   multipart       └──────────────────────────┘
  └───────────────────┘  ◀── {ok,data:{command_id,node_id,…},error}
```

### 3.1 New package `internal/apiclient`

The write-side mirror of `internal/control`: a thin, cobra-free, store-free HTTP
client. One focused file + tests.

- `type Client struct { baseURL string; http *http.Client }`
- `func New(baseURL string) *Client` — trims a trailing slash; default
  `http.Client` (with sane timeouts).
- `type InstallOpts struct { Lifecycle string; Runlevel int; IntervalS int64; Triggers []string }`
  (the client-facing install knobs; CRC + size are server-computed).
- Methods (one per S1 write endpoint):
  - `Command(sel, verb string, args any) (cmdID, nodeID, err)` — marshals
    `{verb,args}`, POSTs to `/api/nodes/{sel}/commands`, decodes the envelope.
  - `Install(sel, name string, image io.Reader, opts InstallOpts) (cmdID, nodeID, size, err)`
    — builds the multipart body (`image` file part named `<name>.bin` + `name`,
    `lifecycle`, `runlevel`, `interval`, repeatable `trigger` fields), POSTs to
    `/api/nodes/{sel}/containers`.
  - `PatchNode(sel string, name *string, maxOfflineS *int64) (nodeID, err)` — marshals
    only the present fields, PATCHes `/api/nodes/{sel}`.
- **Envelope decoding** (one helper): read body, JSON-decode `{ok,data,error}`. On a
  non-2xx status or `ok=false`, return an error whose message is the envelope's
  `error` (so CLI output reads the same as the old control errors). On a transport
  error (e.g. connection refused), wrap with: *"could not reach porta server at
  `<baseURL>` — is `porta serve` running?"*.
- The selector is URL-path-escaped before interpolation.

### 3.2 Server resolution helper (in `portacli`)

A small `serverURL()` that returns the `--server` flag value, falling back to
`$PORTA_SERVER`, then `http://localhost:6970`. Wired as a persistent flag in
`root.go` alongside `--db`.

### 3.3 Re-pointed write commands

Each of the 8 commands' `RunE` is rewritten to:
1. Build `apiclient.New(serverURL())`.
2. Call the matching client method with the **raw** `-d` selector and the
   command-specific args.
3. Print the existing confirmation line, using `command_id` + `node_id` from the
   response.

Mapping:

| command | client call | endpoint |
|---|---|---|
| `device set <app> <key> <value>` | `Command(sel,"set",{app,key,value})` (value via `config.InferScalar`) | POST /commands |
| `device set-console <on\|off>` | `Command(sel,"set-console",{state})` | POST /commands |
| `device set-power-mode <mode>` | `Command(sel,"set-power-mode",{mode})` | POST /commands |
| `device set-poll-interval <dur>` | `Command(sel,"set-poll-interval",{interval:<raw string>})` (server parses) | POST /commands |
| `device set-max-offline <dur>` | `PatchNode(sel, nil, &secs)` (client parses dur→seconds) | PATCH /nodes |
| `device name <new-name>` | `PatchNode(sel, &name, nil)` | PATCH /nodes |
| `container install <name> <file.bin>` | `Install(sel,name,file,opts)` | POST /containers |
| `container uninstall <name>` | `Command(sel,"stop",{name})` | POST /commands |

`config.InferScalar` stays a **client-side** concern (it turns the operator's string
into a typed scalar for `device set`); the typed value rides in the JSON `value`
field and the server's `coerceScalar` handles the `json.Number` round-trip.

### 3.4 Server-side changes (`internal/apisrv`)

1. **`EnsureNode` on write.** After `resolveSel` in `handleCommand`,
   `handleContainerInstall`, and `handlePatchNode`, call `h.st.EnsureNode(id, h.now())`
   before dispatching. Reads (`handleListNodes`, `handleNodeDetail`,
   `handleNodeCommands`) are unchanged (non-creating).
2. **`node_id` in write responses.** Add `"node_id": id` to the success `data` of the
   three write handlers (`{command_id, node_id}`, install `{command_id, node_id, size}`,
   patch `{node_id}`). Additive; the S1 spec §4 is updated to match.

These are small, backward-compatible additions to the S1 surface.

## 4. Output (parity notes)

- Confirmation lines lead with the resolved MAC (from `node_id`), matching today —
  e.g. `aabbccddeeff: enqueued set sampler.interval=30 (command #5)`.
- `container install` confirmation drops the CRC32 from the line (the server computes
  and owns it; it remains visible via `porta log`); it uses `size` from the response:
  `aabbccddeeff: registered blink (40960 B); enqueued run (command #6)`.
- The "no triggers given" early warning stays client-side (printed before the call).

## 5. Error handling

- API `ok=false` / non-2xx → the CLI command returns an error carrying the server's
  `error` string (cobra prints it as today).
- Connection failure → the friendly "is `porta serve` running?" wrap.
- Bad client-side input (e.g. unparseable duration for `set-max-offline`,
  `set-console` state other than on/off) is still validated where it must be: the
  server validates `set-console`/`set-power-mode`; `set-max-offline`'s duration parse
  happens client-side (it's an int64 field), so a bad duration errors before any HTTP
  call.

## 6. Testing (host-only, no device)

- **`apiclient` unit tests** against `httptest.NewServer` with a stub handler:
  assert request method/path/body shape (JSON envelope, multipart parts), status →
  error mapping, transport-error wrap, and that `command_id`/`node_id`/`size` decode.
- **End-to-end**: stand up the **real** `apisrv.Handler` over a temp store behind
  `httptest.NewServer`, point a re-pointed cobra command at it via `--server`, run
  it, and assert (a) the command landed in the store (`NextUndelivered`) and (b) the
  confirmation output. True CLI→HTTP→apisrv→store coverage.
- **`EnsureNode`-on-write**: a new apisrv test — POST a command for a well-formed but
  unseen MAC → node row is created and the command is queued.
- **`node_id` echo**: assert the three write responses carry the resolved id.
- Existing `mutate_test.go` core-function tests (`runDeviceSet`, etc.) are updated or
  replaced to reflect the client path (the testable cores now call the client, not
  `control`).

## 7. Out of scope (S2 scope guard / YAGNI)

- Re-pointing reads, `run` (S3), `monitor`/console+telemetry streaming (S5). Each is
  its own later phase; `serve` always keeps the db.
- The `device get` arbitrary-app config gap (S1 detail returns only the
  lexically-first app's config) — surfaces only when reads are re-pointed; not S2.
- Bearer-token auth / per-caller identity (deferred since S1).
- Removing the `--db` flag or the `store`/`control` imports from `portacli` — they
  remain until the deferred commands are migrated.

## 8. Open implementation details to pin (not design risks)

- Exact `http.Client` timeouts (a short connect + a generous overall for the
  multipart upload).
- Whether `serverURL()` lives in `root.go` or a small `client.go` in `portacli`.
- The precise confirmation-string formats (carry the existing wording; only the
  CRC-drop and `node_id` source change).
