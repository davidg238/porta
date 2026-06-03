# S6 — Client-side panic decode (design)

Date: 2026-06-03
Status: approved
Series: control-plane API (S1–S6); this is the final sub-project.

## 1. Goal

When a Toit payload on a node throws an uncaught exception, the node's system
process emits a **base64-encoded trace** ("system message") that is unreadable
until run through `jag decode`, which symbolicates it against the program's
`.snapshot`. Two things have blocked surfacing this in porta:

1. **Reach** — the blob is printed to the *serial line*. porta's telemetry path
   ("console forward") ships the structured **telemetry buffer**, not serial
   stdout, so panic blobs never cross the network today.
2. **Provenance** — `jag decode` only reads snapshots from its cache
   (`~/.local/state/toit/snapshots/<uuid>.snapshot`). `porta run` compiles a
   snapshot, relocates it to a `.bin`, and **deletes the snapshot**, so even a
   captured blob can't be decoded.

S6 closes both: the node captures and forwards panic traces as a dedicated
telemetry kind, `porta run` retains the snapshot into jag's decode cache, and
`porta monitor` auto-decodes panic rows inline.

This spans two repos (coupled only over the wire, per `docs/PROTOCOL.md`):
**porta** (retain + decode + the canonical contract doc) and **nodus** (capture).
porta owns the contract; nodus is pointed at it and built separately.

**S6 deliverables in this (porta) sub-project:**
1. `docs/PANIC-REPORTING.md` — the canonical, node-facing panic-report contract
   (porta-owned, like `PROTOCOL.md`), plus the `kind:"panic"` row added to
   `PROTOCOL.md §6`. This is the handoff artifact for the nodus work.
2. porta snapshot retention into jag's decode cache.
3. porta `monitor` auto-decode, verified against **synthetic** `kind:"panic"`
   rows (no nodus dependency to build/test the porta side).

The **nodus capture** work (§4) is launched separately, consuming
`docs/PANIC-REPORTING.md`; hardware end-to-end happens once it lands.

## 2. Locked decisions (don't re-litigate)

- **Wire label: `kind:"panic"`.** The node knows it is a panic, so it labels the
  forwarded entry as such — a dedicated telemetry kind, not an overloaded
  generic `log`. Additive: `data_log.kind` is a free-form `TEXT` column with no
  validation on the porta side; ingest, store, query, API, and the `--kind`
  filter all pass it through verbatim. No schema or protocol-validation change.
- **Snapshot retention strategy: write into jag's decode cache** at deploy time
  (`~/.local/state/toit/snapshots/<uuid>.snapshot`), keyed by the snapshot's
  program UUID (from `toit tool snapshot uuid`). `jag decode` then resolves with
  zero porta-side store. (Rejected: a porta-owned durable snapshot store +
  lazy copy. More robust to cache cleaning but duplicative; re-deploy
  repopulates the cache, which fits the pre-1.0 crash-and-fix ethos.)
- **Retain only for deployed images**, not every local build — retention runs in
  `runDeploy` after a successful `Install`, so the cache only grows for images
  actually running on a node.
- **Detection: dedicated marker, not regex.** `porta monitor` decodes rows where
  `Kind == "panic"`. No base64 sniffing of arbitrary log text (rejected: false
  positives, brittle).
- **Auto-decode is default-on** in `porta monitor`, with a `--no-decode` opt-out.
- **Snapshot stays client-side.** Decode works on the machine that built/deployed
  the image. An image deployed from another machine (or a raw `.bin` web upload)
  surfaces the blob but can't auto-decode there; the fallback hint says so. The
  gateway stays language-agnostic — no snapshots server-side.
- **Serial behavior unchanged.** The node's trace handler returns the message to
  the system after forwarding, so the blob still prints to serial for USB
  debugging.
- **porta owns the contract doc.** The panic-report contract is canonical in
  porta (`docs/PANIC-REPORTING.md` + `PROTOCOL.md §6`), normative wire shape
  separated from recommended Toit implementation, so heterogeneous nodes
  (future Smalltalk) conform via the wire contract alone. nodus consumes the doc;
  it is not a porta dependency.

## 3. Architecture & data flow

```
payload throws (any process)
   → Toit system routes the trace to the registered TraceService          [nodus]
   → handle-trace: base64(message); buffer.add {kind:"panic", text:<b64>};
     return message  (system still prints to serial)                      [nodus]
   → flush-telemetry_ ships the buffer when console-forward is on          [nodus]
   → PUT data?id=<node>  →  apisrv ingest  →  data_log row (kind="panic")  [porta]
   → GET /api/nodes/{sel}/telemetry  (S5 windowed/after cursor)           [porta]
   → porta monitor: row.Kind=="panic" → jag decode <text> → print trace   [porta]
   → porta run earlier retained <uuid>.snapshot in jag's cache → resolves [porta]
```

## 4. nodus — panic capture (separate effort; consumes `docs/PANIC-REPORTING.md`)

> Built and launched separately, pointed at the porta-owned contract doc. The
> porta side (§5–§6) does **not** depend on this landing — it is verified against
> synthetic `kind:"panic"` rows. Hardware e2e (§8) needs this piece.


- New `TraceServiceProvider` implementing `system.api.trace.TraceService`
  (`handle-trace message/ByteArray -> ByteArray?`, SELECTOR uuid
  `41c6019e-…`, major 0 / minor 2).
- Registered in the **spawned remoting process** (`spawn-remoting_`,
  `supervisor.toit`), alongside `TelemetryServiceProvider`, sharing that
  process's `TelemetryBuffer` instance — so `handle-trace` can `buffer.add`
  directly. Registered **before** payloads start (the remoting process already
  comes up before any payload can emit).
- `handle-trace message`:
  - `text := base64.encode message`
  - `buffer.add {"kind": "panic", "text": text}`
  - **return `message`** — let the system's built-in handler still print to
    serial (USB behavior unchanged).
  - The whole body is wrapped so an exception while forwarding falls through to
    returning `message` (default handling); never swallow the trace silently.
- Forwarded by the existing `flush-telemetry_` when `console_forward` is on,
  bounded by the existing buffer cap (oldest dropped at capacity).
- The system routes **every process's** trace to the registered service, so
  payload panics in other processes are captured. A panic in the remoting
  process itself (or before registration) falls back to serial only — acceptable.

## 5. porta — snapshot retention

- `toolchain.Build` keeps the snapshot available to the caller instead of
  discarding it (return the image bytes **and** the snapshot path/uuid, or split
  out a step that produces both). Temp dir lifetime extends until retention runs.
- New `toolchain.RetainSnapshot(ex, snapshotPath)` (or equivalent):
  1. `uuid := toit tool snapshot uuid <snapshotPath>` (trimmed stdout).
  2. copy `<snapshotPath>` → `<cacheDir>/<uuid>.snapshot`.
  - `cacheDir` defaults to `~/.local/state/toit/snapshots` (resolved from
    `$HOME`), overridable via an env var (e.g. `$PORTA_SNAPSHOT_DIR`) for tests.
- `runDeploy` calls retention **after** a successful `Install` (deployed-only).
  Best-effort: a retention failure prints a warning to stderr and returns
  success — a failed decode-cache copy must never fail a deploy.

## 6. porta — auto-decode in monitor

- A small **decoder seam** mirroring the existing `telemetryReader` interface, so
  `runMonitor` stays unit-testable with a fake:
  ```go
  type panicDecoder interface { Decode(blob string) (string, error) }
  ```
  The real implementation shells out to `jag decode <blob>` via the toolchain
  executor.
- In `runMonitor`, for each row:
  - `Kind == "panic"` → print a `‼ PANIC` header line (with ts), then
    `decoder.Decode(row.Text)`'s output indented; on decode error print the raw
    blob plus a one-line hint: *"no local snapshot for this image — built on
    another machine? run `jag decode <blob>` there."*
  - otherwise → `telemetry.FormatLine(toStoreRow(row))` as today (byte-for-byte
    unchanged for non-panic rows).
- `--no-decode` flag: when set, panic rows print the raw blob (with the
  `jag decode` hint) and skip the decoder. Default off (auto-decode on).
- Decode never breaks the tail loop: any decoder error degrades to raw-blob
  output and the loop continues.

## 7. Error handling / edge cases

- `jag` missing on PATH or decode non-zero exit → raw blob + hint, loop continues.
- Snapshot not in the local cache (image built elsewhere, or cache cleaned) →
  same fallback; re-deploying from this machine repopulates the cache.
- Oversized blob → existing telemetry buffer cap may drop entries (accepted).
- `--kind metric` excludes panics; `--kind panic` shows only panics (free `--kind`
  filter, already plumbed through S5).
- Retention failure during deploy → warn, deploy still succeeds.

## 8. Testing

**porta (host):**
- `RetainSnapshot`: temp `$HOME`/override dir + fake executor returning a uuid;
  assert `<uuid>.snapshot` written with the snapshot bytes.
- `runDeploy`: retention invoked after Install; retention error does not fail the
  deploy (warning only).
- `runMonitor`: fake decoder + a `kind:"panic"` row → asserts `Decode` called
  with the blob and the decoded text rendered under a `‼ PANIC` header;
  non-panic rows unchanged.
- decode-failure path → raw blob + hint rendered, loop continues.
- e2e via cobra `--server` against a fake apisrv (mirrors S5 `monitor_test`).

**nodus (host):**
- `TraceServiceProvider.handle-trace` adds a `{kind:"panic", text:<b64>}` entry to
  the buffer and returns the original `message`.
- forwarding-exception path returns `message` (no silent swallow).

**Hardware (e2e):** deploy a payload that throws via `porta run`, enable
`set-console on`, induce the panic, and confirm the decoded stack trace appears
in `porta monitor -d <node> --follow`. (Earlier panic-decode backlog example
failed precisely because the snapshot was absent — this verifies retention.)

## 9. Phasing

The porta retain + decode work is independently buildable and testable against a
synthetic `kind:"panic"` row; the nodus capture change is launched separately and
needed only for the live hardware e2e. Order:

**This (porta) sub-project:**
1. Contract docs — `docs/PANIC-REPORTING.md` + `PROTOCOL.md §6` `kind:"panic"`
   (the nodus handoff artifact). *(Done in the design/branch already.)*
2. porta — snapshot retention (`toolchain` + `runDeploy`).
3. porta — `porta monitor` auto-decode (decoder seam + render + `--no-decode`),
   verified against synthetic `kind:"panic"` rows.

**Separate, launched once the doc is available:**
4. nodus — `TraceServiceProvider` capture + forward, per `docs/PANIC-REPORTING.md`.
5. Hardware e2e (induce a payload panic → decoded trace in `porta monitor`).

## 10. Out of scope

- Server-side snapshot storage / decode (gateway stays language-agnostic).
- Decoding system/firmware-level panics that need the firmware envelope rather
  than a program snapshot (`jag decode --envelope`); S6 targets payload
  exceptions whose snapshot suffices.
- MCP / web-UI surfacing of decoded panics (CLI `monitor` only for S6).
- Regex/heuristic detection of blobs from non-nodus sources.
