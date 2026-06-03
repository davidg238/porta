# S3 — `porta run` over the control-plane API (Design)

**Status:** Approved 2026-06-02. Part of the server-as-workhorse / CLI-as-client
re-architecture (S1–S6). Predecessors: S1 (control-plane HTTP API, `internal/apisrv`,
merged @b429436) and S2 (CLI-as-API-client, `internal/apiclient`, merged @b53f049).

## 1. Goal

Make `porta run` the **last major write path** to flow through the server. Compile and
relocate stay client-side (the local `toit` SDK, the full project on disk — the `jag run`
model). The node's reported SDK is read over the S1 API, and the built `.bin` + `run`
command are delivered via the S1 multipart install. After S3, `porta run` no longer opens
the local store: the server is the single writer and the audit trail (`issued_by="api"`)
is complete.

## 2. Background

`porta run` (the `/tools/toit` Phase-1 command, `internal/portacli/run.go`) currently
bypasses the server entirely:

- `openStore()` → `store.Open("porta.db")` (direct db access; needs `--db` / co-location)
- `resolveNodeID(st, device)` (local selector resolution)
- `st.GetNode(id).Sdk` (reads the node's reported SDK from the store)
- `control.Install(st, …)` (registers payload + enqueues `run` directly on the store)
- `control.SetPowerMode(st, …)` (optional, direct on the store)

The compile/relocate half already lives client-side in `internal/toolchain` (the narrating
`Executor`, `SDKVersion`, `CheckSDK`, `Build`) and is unchanged by S3.

Everything S3 needs already exists on the server side:

- `GET /api/nodes/{sel}` (`apisrv.handleNodeDetail`) returns `chip` and `sdk` — the
  handler comment already states *"The SDK guard in `porta run` reads chip/sdk from here."*
- `apiclient.Install(sel, name, image, opts)` (multipart) and `apiclient.Command(sel, verb,
  args)` already exist from S2.

The only gap: `apiclient` is **write-only** today (S2 deliberately deferred all reads).
S3 introduces the first read — a narrow one, scoped to the SDK guard.

## 3. Locked decisions

These were confirmed during brainstorming (recommended option taken in each case):

1. **SDK guard = client reads, client checks.** The build SDK (`toit version`) is known
   only client-side; the node's reported SDK is known only server-side. The CLI GETs the
   node's `sdk`, then runs `toolchain.CheckSDK` locally before building. No API/protocol
   change; the server stays dumb. Matches the decomposition's "SDK guard reads via S1".
2. **Narrow identity read only.** Add a focused `NodeIdentity(sel) → (chip, sdk, err)`
   method to `apiclient` for the guard. The full `device get` / node-detail read stays
   db-backed and deferred (per S2 — that surface is bug-prone and buys nothing for S3).
3. **Fully over HTTP, no store.** `porta run` drops `openStore`/`resolveNodeID`/`control`
   entirely. The raw `-d` selector is passed to the server, which resolves it (mirrors the
   8 S2 writes). Requires `porta serve` running.

## 4. Design

### 4.1 `apiclient.NodeIdentity` (new, the one S3 read)

```go
// NodeIdentity fetches just the node's reported chip/sdk (GET /api/nodes/{sel}),
// for porta run's SDK guard. The full node-detail read stays deferred (S2).
func (c *Client) NodeIdentity(sel string) (chip, sdk string, err error)
```

- GETs `c.baseURL + "/api/nodes/" + url.PathEscape(sel)`.
- Reuses the existing `do()` envelope decoder, so:
  - transport failure → the same friendly `"could not reach porta server … is porta serve
    running?"` wrap;
  - unknown node → the server's 404 error string verbatim;
  - non-JSON body → the same "invalid response" diagnostic.
- Decodes only `chip` and `sdk` from the detail payload (a struct with just those two
  `json:"chip"`/`json:"sdk"` fields; other fields ignored).
- The package doc comment is updated to note it now carries one read (the SDK guard),
  no longer strictly write-only.

A node that exists but has not yet reported returns `sdk == ""` (not an error); the
"hasn't reported identity yet" decision stays in `runDeploy` (§4.2), preserving the
Phase-1 message.

### 4.2 `runDeploy` re-point

Signature moves from store-backed to client-backed. The `now int64` parameter is dropped
(the server stamps the timestamp and `issued_by="api"`):

```go
func runDeploy(out io.Writer, c *apiclient.Client, ex *toolchain.Executor,
               sel, appPath string, opts deployOpts, force bool) error
```

Flow (unchanged order — **guard before the expensive build**, fail fast):

1. `chip, sdk, err := c.NodeIdentity(sel)` — return on error.
2. If `sdk == ""`: return the Phase-1 block message (*"node … hasn't reported its
   firmware identity yet — wait for a check-in (or flash it via `porta flash`) before
   deploying"*). This block applies **even with `--force`** (Phase-1 parity: `--force`
   overrides only an SDK *mismatch*, not unknown identity).
3. `active, err := toolchain.SDKVersion(ex)` — the local `toit version`.
4. If `!force`: `toolchain.CheckSDK(sdk, active)` — return on mismatch.
5. `img, err := toolchain.Build(ex, appPath)` — local compile + relocate.
6. `cmdID, nodeID, size, err := c.Install(sel, opts.Name, bytes.NewReader(img),
   apiclient.InstallOpts{Lifecycle: opts.Lifecycle, Runlevel: opts.Runlevel,
   Triggers: opts.Triggers})`.
7. Confirmation line leads with the resolved `nodeID` and the server-returned `size`:
   `"<nodeID>: built <name> (<size> B), enqueued run (command #<cmdID>)"`.
8. If `opts.PowerMode != ""`: `c.Command(sel, "set-power-mode",
   map[string]any{"mode": opts.PowerMode})` — return on error.

`runDeploy` loses its `store` and `control` imports. `deployOpts` is unchanged.

### 4.3 `newRunCmd` re-point

- Drop `openStore()` + `defer st.Close()` and `resolveNodeID(st, device)`.
- Build `c := apiclient.New(serverURL())` (same `--server`/`$PORTA_SERVER` resolver the
  S2 writes use).
- Pass the **raw `device` selector** into `runDeploy` (server resolves it).
- Keep the client-side bits: `--name` defaulting to the source-file stem, the lifecycle
  prompt (`promptChoice`), and the trigger prompt (`promptTriggers`).
- The `toolchain.NewExecutor(toolchain.ExecRunner{}, cmd.OutOrStdout(), verbose)` build is
  unchanged.
- After the edit, `run.go` imports `apiclient` + `toolchain` (+ stdlib `bytes`, `fmt`,
  `io`, `os`, `bufio`, `path/filepath`, `strings`, cobra) — no `store`, no `control`.

Flag set is unchanged: `-d/--device`, `--name`, `--lifecycle`, `--trigger`, `--runlevel`,
`--power-mode`, `--force`, `-v/--verbose`.

## 5. Behavior changes (accepted, parity with S2)

- `porta run` now **requires `porta serve` running**; connection refused yields the
  friendly hint.
- The queued `run` command is logged with `issued_by="api"` (not `"cli"`).
- The reported `size` in the confirmation line comes from the server (the stored image
  size), not a local `len(img)`.
- The CRC is server-computed (already true since S2's install path); visible via
  `porta log`.

## 6. Error handling

- Transport / server-down → friendly "is `porta serve` running?" (from `do()`).
- Unknown node selector → server 404 error string surfaces verbatim.
- Node exists but no reported SDK → client-side block message (§4.2 step 2).
- SDK mismatch → `CheckSDK` error naming both versions; `--force` overrides.
- `toit` not on PATH / compile failure → surfaces through the narrated toolchain step
  (`Executor` prints the rerunnable command).

## 7. Testing

Rewrite `internal/portacli/run_test.go` to mirror S2's `mutate_test.go`:

- Stand up the **real `apisrv.Handler` over a temp `store.Store` behind `httptest`**;
  point `apiclient.New(srv.URL)` at it. The selector is resolved server-side.
- Keep the `stubRunner` for the toolchain: `toit version` → a fixed SDK string;
  `snapshot-to-image -o <path>` → writes canned `IMG` bytes.
- Cases:
  - **happy path** — seed a node identity in the store (matching SDK) → `runDeploy`
    succeeds → assert a `run` command is queued via `store.NextUndelivered` and the
    command-log row has `issued_by="api"`.
  - **unknown identity** — seed a node with no reported `sdk` → blocked; assert the block
    holds even with `force == true`.
  - **SDK mismatch** — stub `toit version` ≠ seeded `sdk` → refused; `force == true`
    overrides and the `run` lands.
  - **flag registration** — `TestNewRunCmdRegistersFlags` stays.
- Host-only; no device, no real `toit`.

## 8. Scope / non-goals

- **In scope:** the one narrow `apiclient` read; re-pointing `runDeploy` + `newRunCmd`;
  the rewritten test.
- **Out of scope (deferred):** the full `device get` / node-detail read migration (S2
  defers it); `porta monitor` (S5); panic decode (S6); `porta flash` / envelope fetch
  (toolchain Phase 2). The nodus-side `chip`/`sdk` report emit is a separate-repo
  companion already tracked; until a node reports identity, `porta run` correctly blocks.
