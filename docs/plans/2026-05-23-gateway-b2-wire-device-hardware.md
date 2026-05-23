# Gateway B2 — wire + device + hardware Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete the Porta Toit gateway's command-queue control plane end-to-end — a store-backed TFTP request handler, a `gateway serve` daemon, and a rewritten device supervisor that drains commands, pulls payloads on demand, applies them to its NVS goal state, and reports observed state each wake — and hardware-verify it on node `fwkb`.

**Architecture:** B1 already built the TFTP-free core (`store.toit`, `command.toit`, the CLI). B2 wires that core to the network and the device. On the host, `gateway/handler.toit` implements the tftp package's evolved `Storage` interface (Spec A): it parses `?id=<mac>` from the raw request path, serves the oldest-undelivered command on RRQ `commands`, serves payload BLOBs on RRQ `payload`, accepts a state report on WRQ `report`, and marks a command delivered on the RRQ transfer-complete event. `gateway/serve.toit` opens the store, wraps it in the handler, and runs a `TFTPServer`. On the device, the supervisor replaces its single `GET goal` with: drain `commands` until a zero-byte body, apply each command to a goal map seeded from the persistent `Inventory`, reconcile (fetch missing images via `payload`, stop/uninstall removed apps), then PUT a `report` before deep-sleeping at a node-local, command-configurable poll cadence.

**Tech Stack:** Toit. Host gateway code runs under the prebuilt `toit-sqlite` binary (`sqlite` + `pkg-cli` + `pkg-host` + the `tftp` package). Device code targets the jag/ESP32 SDK and depends on `tftp` (path). Core libs: `encoding.json`, `crypto.crc`, `io`, `io.buffer`, `system.containers`, `system.storage`, `esp32`.

---

## Conventions for every task

- **Two toolchains.** Host gateway tests/CLI run under the prebuilt `toit-sqlite` binary (the system `toit` is SDK alpha-192; `sqlite` pins alpha-193). Set once per shell and run from `porta/gateway/`:

  ```bash
  export TS=~/workspaceToit/sqlite/build/bin/toit-sqlite
  $TS version    # sanity: prints a v2.0.0-alpha.x line
  ```

  Device modules that do **not** import `esp32`/`system.*` (the pure decode/format/build helpers and their tests) run under `$TS` too, from `porta/device/` — see each task. The full supervisor (imports `esp32`, `system.containers`) is built for hardware and is **analyze-only** on host (`$TS analyze`), then verified on `fwkb` in Phase 3.

- **Toit conventions:** kebab-case functions/vars, `PascalCase` classes, `KEBAB-CASE` constants, 2-space indent, 4-space continuation; private members end `_`; comments are full sentences; Toitdoc method comments start with a third-person verb and reference code with `$name`. Filenames are lowercase, words joined (`handler.toit`); tests are suffixed `_test.toit` alongside source.

- **A test file** is `import expect show *` + a `main:` that asserts with `expect-equals` / `expect` / `expect-throw`; it passes iff the process exits 0. `$TS some_test.toit` runs it. There is no test framework.

- **B1 lessons (carry forward):** `{}` is an empty **Set**; an empty **Map** is `{:}`. Toit `Map`/`List` `==` is identity — assert structure with `expect-structural-equals`. `expect-throw` matches the thrown string **exactly**. `json.encode` returns a `ByteArray`; to store JSON in a sqlite TEXT column store `(json.encode m).to-string` and decode `json.decode s.to-byte-array` (the store's `encode-json_`/`decode-json_` already do this). `int.parse` uses `--if-error=` (not the deprecated `--on-error`). A multi-line call ending in trailing named args can misparse — hoist into a local.

## The Spec A dependency (read before starting)

B2's handler implements the tftp package's `Storage` **as evolved by Spec A**
(`~/workspaceToit/tftp`, branch `spec/engine-transport-split`). Spec A is being
built by a separate agent. At the time this plan was written, `tftp/src/channel.toit`
(the `Peer`/`Datagram`/`PacketChannel`/`UdpChannel`/`UdpPeer` split) was **already
landed**, but `tftp/src/storage.toit` had **not yet** grown the `Request` context,
the `--req` parameters, or the `on-transfer-complete` hook. **Task 1 gates the whole
plan on those being present.** The pinned contract B2 consumes (from Spec A's spec):

```
// In the tftp package, after Spec A:
class Request:
  peer/Peer          // the transport peer that issued the request
  raw-path/string    // full un-stripped resource name incl. "?id=…&name=…&crc=…"
  constructor --.peer --.raw-path:

abstract class Storage:
  exists     name/string --req/Request?=null -> bool
  size       name/string --req/Request?=null -> int?
  reader-for name/string --req/Request?=null -> io.CloseableReader
  writer-for name/string --req/Request?=null --tsize-hint/int?=null -> io.CloseableWriter
  on-transfer-complete --op/int --resource/string --peer/Peer --bytes/int --ok/bool -> none  // default no-op
  reads-allowed -> bool: return true
  writes-allowed -> bool: return true
```

Opcodes `RRQ ::= 0x01` and `WRQ ::= 0x02`, the sentinel strings
`STORAGE-FILE-NOT-FOUND` / `STORAGE-ACCESS-DENIED`, and `TFTPServer --storage --port
--logger` (with `.start`) are all exported from `tftp` (verified against the live
package). `on-transfer-complete` fires once per transfer: `ok=true` on a clean
RRQ/WRQ success, `ok=false` on abort.

## File structure (final state after B2)

```
porta/
  gateway/
    package.yaml         + tftp path dependency (added in Task 2)
    handler.toit         NEW  StoreBackedHandler extends tftp.Storage  + helpers
    handler_test.toit    NEW
    serve.toit           NEW  cmd-serve: open store → handler → TFTPServer
    command.toit         MODIFY  Command.run gains --size (Task 8)
    command_test.toit    MODIFY
    gateway.toit         MODIFY  wire the `serve` subcommand; install passes size
    (store.toit, crc32/duration/names + their tests: unchanged from B1)
  device/
    package.yaml         (unchanged: already deps tftp path)
    node_id.toit         NEW  mac-to-id (pure)            + node_id_test.toit
    node_command.toit    NEW  on-device command decode + apply-to-goal + node_command_test.toit
    report.toit          NEW  build-report (inventory → JSON body) + report_test.toit
    inventory.toit       MODIFY  inventory-to-goal-map helper (Task 12)
    inventory_test.toit  MODIFY
    transport.toit       MODIFY  GatewayClient.put (WRQ)
    schedule_store.toit  MODIFY  wake counter (Task 14)
    supervisor.toit      REWRITE drain/apply/fetch/remove/report + node-local poll (Task 15)
  host/
    serve.toit           RETIRED at end of Phase 3 (Task 16)
```

---

# Phase 1 — Gateway host: handler + daemon

### Task 1: Gate on the Spec A Storage evolution

**Files:** none (a precondition check).

- [ ] **Step 1: Confirm the evolved `Storage` contract is present in the tftp package**

Run:
```bash
grep -n "class Request\|on-transfer-complete\|--req/Request" ~/workspaceToit/tftp/src/storage.toit
```
Expected: matches for **all three** — a `class Request`, the `on-transfer-complete` method, and at least one `--req/Request` parameter.

- [ ] **Step 2: Decide**

If all three are present, proceed to Task 2. **If any is missing, STOP** — Spec A's Storage evolution has not landed yet. B2's handler cannot compile against the old `Storage`. Report this and wait for Spec A to complete `tftp/src/storage.toit`. (The `channel.toit` split alone is not sufficient.)

---

### Task 2: Add the tftp dependency to the gateway package

**Files:**
- Modify: `gateway/package.yaml`

- [ ] **Step 1: Add the tftp path dependency**

Append under `dependencies:` in `gateway/package.yaml` (keep the existing `sqlite`/`cli`/`host` entries):

```yaml
  tftp:
    path: /home/david/workspaceToit/tftp
```

- [ ] **Step 2: Verify the package still resolves and existing tests still pass**

Run (from `porta/gateway`):
```bash
export TS=~/workspaceToit/sqlite/build/bin/toit-sqlite
$TS run store_test.toit && echo OK
```
Expected: `OK`. (This confirms `toit-sqlite`'s bundled SDK resolves the new `tftp` path dependency alongside `sqlite`. If `tftp` fails to resolve under this SDK, STOP and report — the host-side handler tests need `tftp` to compile under `toit-sqlite`.)

- [ ] **Step 3: Commit**

```bash
cd ~/workspaceToit/porta
git add gateway/package.yaml && git commit -m "build(gateway): depend on tftp for the B2 request handler"
```

---

### Task 3: Resource-path parsing helper

**Files:**
- Create: `gateway/handler.toit`
- Test: `gateway/handler_test.toit`

- [ ] **Step 1: Write the failing test**

`gateway/handler_test.toit`:
```toit
import expect show *
import .handler show parse-resource_

main:
  // No query → base only, empty params.
  bare := parse-resource_ "commands"
  expect-equals "commands" bare[0]
  expect-structural-equals {:} bare[1]

  // Query → base + decoded params (insertion order irrelevant for a Map).
  full := parse-resource_ "payload?id=a0b1c2d3e4f5&name=blink&crc=12345"
  expect-equals "payload" full[0]
  expect-structural-equals {"id": "a0b1c2d3e4f5", "name": "blink", "crc": "12345"} full[1]

  // A bare key with no '=' maps to the empty string.
  flag := parse-resource_ "report?id=abc&verbose"
  expect-equals "report" flag[0]
  expect-equals "abc" flag[1]["id"]
  expect-equals "" flag[1]["verbose"]
```

- [ ] **Step 2: Run it to verify it fails**

Run (from `porta/gateway`): `$TS run handler_test.toit`
Expected: FAIL — `handler.toit` / `parse-resource_` does not exist yet.

- [ ] **Step 3: Write the minimal implementation**

`gateway/handler.toit` (no imports yet — `parse-resource_` is pure string handling; Task 4 prepends the import block):
```toit
// gateway/handler.toit — the store-backed TFTP request handler. Implements the
// tftp package's Storage (as evolved by Spec A): it parses the "?id=<mac>&…"
// query the node sends, serves the oldest-undelivered command and payload BLOBs
// on RRQ, ingests a state report on WRQ, and marks a command delivered on the
// RRQ transfer-complete event.

/**
Splits a raw resource name "base?k=v&k2=v2" into [base/string, params/Map].

A key with no "=" maps to the empty string. The node sends its identity and
  payload selector as this query suffix (e.g. "payload?id=<mac>&name=<n>&crc=<c>").
*/
parse-resource_ raw/string -> List:
  q := raw.index-of "?"
  if q < 0: return [raw, {:}]
  base := raw[..q]
  params := {:}
  (raw[q + 1..].split "&").do: | kv/string |
    eq := kv.index-of "="
    if eq < 0: params[kv] = ""
    else: params[kv[..eq]] = kv[eq + 1..]
  return [base, params]
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `$TS run handler_test.toit`
Expected: PASS (process exits 0, no output).

- [ ] **Step 5: Commit**

```bash
git add gateway/handler.toit gateway/handler_test.toit
git commit -m "feat(gateway): resource-path query parser for the request handler"
```

---

### Task 4: In-memory `CloseableReader` and the `commands`/`payload` readers

**Files:**
- Modify: `gateway/handler.toit`
- Test: `gateway/handler_test.toit`

- [ ] **Step 1: Write the failing test** (append to `handler_test.toit`'s `main`, and add the import)

Set the test's imports to the full set used across Tasks 4–6 (replace the single `import .handler show parse-resource_` line so there is exactly one import per module — Tasks 5 and 6 add no new imports):
```toit
import expect show *
import .handler show StoreBackedHandler parse-resource_
import .store show Store decode-json_
import .command show Command
import tftp show Peer RRQ STORAGE-FILE-NOT-FOUND STORAGE-ACCESS-DENIED
```
(`Peer` is used by Task 6's `FakePeer`; importing it now keeps the test's import block stable. An unused-import warning before Task 6 is harmless.) Append to `main`:
```toit
  store := Store.open ":memory:"
  handler := StoreBackedHandler store
  now := 1000

  // Unknown node, empty queue: a "commands" RRQ yields a zero-byte body (drain sentinel).
  r0 := handler.reader-for "commands?id=aabbccddeeff"
  expect-equals null r0.read   // immediate EOF == zero bytes
  r0.close

  // Enqueue one command; the next "commands" RRQ serves its exact wire bytes.
  store.ensure-node "aabbccddeeff" --now=now
  cmd := Command.set-poll-interval --interval-s=1
  store.enqueue-command "aabbccddeeff" cmd --issued-by="test" --now=now
  r1 := handler.reader-for "commands?id=aabbccddeeff"
  expect-structural-equals cmd.encode r1.read
  expect-equals null r1.read
  r1.close

  // Register a payload; a "payload" RRQ for its crc streams the image bytes.
  store.register-payload --crc=999 --name="blink" --image=#[1, 2, 3, 4]
  rp := handler.reader-for "payload?id=aabbccddeeff&name=blink&crc=999"
  expect-equals #[1, 2, 3, 4] rp.read
  rp.close

  // A payload RRQ for an unknown crc throws the not-found sentinel.
  expect-throw STORAGE-FILE-NOT-FOUND: handler.reader-for "payload?id=aabbccddeeff&name=blink&crc=7"

  // exists/size: commands always readable (size unknown); payload sized by the BLOB.
  expect handler.exists "commands?id=aabbccddeeff"
  expect-equals null (handler.size "commands?id=aabbccddeeff")
  expect handler.exists "payload?id=x&crc=999"
  expect-equals 4 (handler.size "payload?id=x&crc=999")
  expect-equals null (handler.size "payload?id=x&crc=7")
  store.close
```

- [ ] **Step 2: Run it to verify it fails**

Run: `$TS run handler_test.toit`
Expected: FAIL — `StoreBackedHandler` does not exist yet.

- [ ] **Step 3: Write the minimal implementation**

First, **prepend** the import block at the top of `handler.toit` (just under the header comment):
```toit
import io
import io.buffer show Buffer
import tftp show Storage Request Peer RRQ STORAGE-FILE-NOT-FOUND STORAGE-ACCESS-DENIED
import .store show Store encode-json_ decode-json_
import .command show Command
```
(`Buffer`, `encode-json_`/`decode-json_`, and `STORAGE-ACCESS-DENIED` are consumed in Task 5; importing them now keeps the block in one place. If `$TS` warns about an unused import between Tasks 4 and 5, ignore it — it resolves in Task 5.) Then append the reader code:

```toit
/**
An $io.CloseableReader over a fixed $ByteArray (or zero bytes when given null).

The engine reads the body once; a null payload yields immediate EOF, which is
  how the handler signals "command queue drained" — a zero-byte RRQ body.
*/
class BytesReader_ extends io.CloseableReader:
  bytes_/ByteArray?
  constructor .bytes_:
  read_ -> ByteArray?:
    b := bytes_
    bytes_ = null
    return b
  close_ -> none:

/** Current wall-clock time in epoch seconds. */
now_ -> int: return Time.now.s-since-epoch

/** A $Storage backed by the gateway's sqlite $Store. */
class StoreBackedHandler extends Storage:
  store_/Store
  constructor .store_:

  exists name/string --req/Request?=null -> bool:
    base := (parse-resource_ name)[0]
    return base == "commands" or base == "payload"

  size name/string --req/Request?=null -> int?:
    parsed := parse-resource_ name
    if parsed[0] != "payload": return null   // "commands" body is dynamic.
    crc := int.parse (parsed[1].get "crc" --if-absent=: "") --if-error=: return null
    p := store_.payload crc
    return p == null ? null : p["size"]

  reader-for name/string --req/Request?=null -> io.CloseableReader:
    parsed := parse-resource_ name
    base := parsed[0]
    params := parsed[1]
    id := params.get "id"
    if id != null: store_.touch-node id --now=now_
    if base == "commands":
      next := id == null ? null : store_.next-undelivered id
      if next == null: return BytesReader_ null
      return BytesReader_ (Command next["verb"] next["args"]).encode
    if base == "payload":
      crc := int.parse (params.get "crc" --if-absent=: "") --if-error=: throw STORAGE-FILE-NOT-FOUND
      p := store_.payload crc
      if p == null: throw STORAGE-FILE-NOT-FOUND
      return BytesReader_ p["image"]
    throw STORAGE-FILE-NOT-FOUND
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `$TS run handler_test.toit`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add gateway/handler.toit gateway/handler_test.toit
git commit -m "feat(gateway): handler serves commands (with drain sentinel) and payloads"
```

---

### Task 5: The report writer (WRQ → `insert-report`)

**Files:**
- Modify: `gateway/handler.toit`
- Test: `gateway/handler_test.toit`

- [ ] **Step 1: Write the failing test** (append to `main`)

```toit
  // A WRQ to "report" buffers the body and, on close, records observed apps + health.
  store2 := Store.open ":memory:"
  h2 := StoreBackedHandler store2
  store2.ensure-node "aabbccddeeff" --now=2000
  body := #[]
  body = "{\"apps\":{\"blink\":{\"crc\":999,\"runlevel\":3}},\"health\":{\"wakes\":4}}".to-byte-array
  w := h2.writer-for "report?id=aabbccddeeff"
  w.write body
  w.close
  reps := store2.reports "aabbccddeeff"
  expect-equals 1 reps.size
  observed := decode-json_ reps[0]["observed_state"]
  expect-equals 999 observed["apps"]["blink"]["crc"]
  health := decode-json_ reps[0]["health"]
  expect-equals 4 health["wakes"]
  // The node row's cached observed_state was refreshed too.
  node := store2.node "aabbccddeeff"
  expect ((decode-json_ node["observed_state"])["apps"].contains "blink")
  // A WRQ to anything but "report" is refused.
  expect-throw STORAGE-ACCESS-DENIED: h2.writer-for "payload?id=aabbccddeeff&crc=1"
  store2.close
```

- [ ] **Step 2: Run it to verify it fails**

Run: `$TS run handler_test.toit`
Expected: FAIL — `writer-for` is not implemented.

- [ ] **Step 3: Write the minimal implementation** (append to `handler.toit`; add `writer-for` to `StoreBackedHandler` and the `ReportWriter_` class)

Add this method to `StoreBackedHandler`:
```toit
  writer-for name/string --req/Request?=null --tsize-hint/int?=null -> io.CloseableWriter:
    parsed := parse-resource_ name
    if parsed[0] != "report": throw STORAGE-ACCESS-DENIED
    id := parsed[1].get "id"
    if id == null: throw STORAGE-ACCESS-DENIED
    store_.touch-node id --now=now_
    return ReportWriter_ store_ id now_
```
Add this class:
```toit
/**
An $io.CloseableWriter that buffers a WRQ "report" body and, on close, splits it
  into the observed-app state and the health struct and records both via
  $Store.insert-report. The body is one JSON object {"apps":{…}, "health":{…}}.
*/
class ReportWriter_ extends io.CloseableWriter:
  store_/Store
  id_/string
  now_/int
  buffer_/Buffer := Buffer
  constructor .store_ .id_ .now_:

  try-write_ data/io.Data from/int to/int -> int:
    buffer_.write data from to
    return to - from

  close_ -> none:
    obj := decode-json_ buffer_.bytes.to-string
    apps := obj.get "apps" --if-absent=: {:}
    health := obj.get "health" --if-absent=: {:}
    store_.insert-report id_
        --observed-state=(encode-json_ {"apps": apps})
        --health=(encode-json_ health)
        --now=now_
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `$TS run handler_test.toit`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add gateway/handler.toit gateway/handler_test.toit
git commit -m "feat(gateway): handler ingests a WRQ state report into the store"
```

---

### Task 6: Mark delivered on transfer-complete

**Files:**
- Modify: `gateway/handler.toit`
- Test: `gateway/handler_test.toit`

The handler serves the **oldest undelivered** command on each `commands` RRQ and marks it delivered when that transfer completes. Because a single node drains serially (one `TFTPClient`, sequential reads) and a freshly enqueued command always gets a higher `id`, "the oldest undelivered at transfer-complete time" is exactly the command just served — so marking is correct without per-peer bookkeeping.

**Spec A assumption to confirm:** the marking parses the node id out of `on-transfer-complete`'s `resource` argument, which requires that `resource` be the **raw, un-stripped** request name (with the `?id=<mac>` query), matching the `name` passed to `reader-for`. The Spec A spec says the event "carries … resource (the request name)". Before implementing, confirm in `~/workspaceToit/tftp/src/exchange.toit` (or `tftp_server.toit`) that the value passed as `resource` to `on-transfer-complete` is the same raw filename string the server hands to `reader-for` — not a stripped form. If Spec A strips it, instead key the marking off `req.raw-path` by tracking the served command id per node id in a handler-local map (`served_/Map`, set in `reader-for "commands"`, read+cleared in `on-transfer-complete`). The test below assumes the raw-name form.

- [ ] **Step 1: Write the failing test**

Define a trivial peer at the top level of the test file (outside `main`); `Peer` is already imported from Task 4:
```toit
/** A minimal Peer for transfer-complete tests (the handler never dereferences it). */
class FakePeer implements Peer:
  operator == other/Peer -> bool: return other is FakePeer
  hash-code -> int: return 0
```
Append to `main`:
```toit
  // Two queued commands drain in FIFO order, each marked delivered on its RRQ complete.
  store3 := Store.open ":memory:"
  h3 := StoreBackedHandler store3
  peer := FakePeer
  store3.ensure-node "aabbccddeeff" --now=3000
  c1 := store3.enqueue-command "aabbccddeeff" (Command.set-poll-interval --interval-s=1) --issued-by="t" --now=3000
  c2 := store3.enqueue-command "aabbccddeeff" (Command.stop --name="blink") --issued-by="t" --now=3000

  // First drain step: serve + complete → c1 delivered, c2 still pending.
  (h3.reader-for "commands?id=aabbccddeeff").close
  h3.on-transfer-complete --op=RRQ --resource="commands?id=aabbccddeeff" --peer=peer --bytes=10 --ok=true
  expect-equals c2 (store3.next-undelivered "aabbccddeeff")["id"]

  // Second drain step → c2 delivered, queue now empty.
  (h3.reader-for "commands?id=aabbccddeeff").close
  h3.on-transfer-complete --op=RRQ --resource="commands?id=aabbccddeeff" --peer=peer --bytes=10 --ok=true
  expect-equals null (store3.next-undelivered "aabbccddeeff")

  // The drain-sentinel transfer (empty queue) marks nothing and does not throw.
  h3.on-transfer-complete --op=RRQ --resource="commands?id=aabbccddeeff" --peer=peer --bytes=0 --ok=true

  // A failed transfer (ok=false) never marks delivered.
  c3 := store3.enqueue-command "aabbccddeeff" (Command.stop --name="x") --issued-by="t" --now=3000
  h3.on-transfer-complete --op=RRQ --resource="commands?id=aabbccddeeff" --peer=peer --bytes=10 --ok=false
  expect-equals c3 (store3.next-undelivered "aabbccddeeff")["id"]

  // A payload transfer-complete is not a command delivery (must not mark c3).
  h3.on-transfer-complete --op=RRQ --resource="payload?id=aabbccddeeff&crc=1" --peer=peer --bytes=4 --ok=true
  expect-equals c3 (store3.next-undelivered "aabbccddeeff")["id"]
  store3.close
```

- [ ] **Step 2: Run it to verify it fails**

Run: `$TS run handler_test.toit`
Expected: FAIL — `on-transfer-complete` is still the inherited no-op, so c1/c2 stay undelivered.

- [ ] **Step 3: Write the minimal implementation** (add the method to `StoreBackedHandler`)

```toit
  on-transfer-complete --op/int --resource/string --peer/Peer --bytes/int --ok/bool -> none:
    if not ok: return
    if op != RRQ: return
    parsed := parse-resource_ resource
    if parsed[0] != "commands": return
    id := parsed[1].get "id"
    if id == null: return
    next := store_.next-undelivered id
    if next == null: return   // drain-sentinel transfer: nothing to mark.
    store_.mark-delivered next["id"] --now=now_
```
Note the test passes `--peer=null`; the parameter type is `Peer` but the handler never dereferences it, so a null is accepted at runtime. Keep the declared type `Peer` to match the Spec A contract.

- [ ] **Step 4: Run the test to verify it passes**

Run: `$TS run handler_test.toit`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add gateway/handler.toit gateway/handler_test.toit
git commit -m "feat(gateway): mark a command delivered on its RRQ transfer-complete"
```

---

### Task 7: The `serve` daemon and CLI wiring

**Files:**
- Create: `gateway/serve.toit`
- Modify: `gateway/gateway.toit` (import + a `serve` subcommand)

- [ ] **Step 1: Write the daemon**

`gateway/serve.toit`:
```toit
// gateway/serve.toit — the gateway daemon. Opens the sqlite store, wraps it in
// the StoreBackedHandler, and runs a TFTPServer over UDP. Replaces host/serve.toit.
import cli
import log
import tftp show TFTPServer
import .store show Store
import .handler show StoreBackedHandler

/** Default unprivileged UDP port (port 69 needs root); the node must match it. */
DEFAULT-PORT ::= 6969

/** Opens the store and serves the command queue + payloads until killed. */
cmd-serve parsed/cli.Parsed -> none:
  db := parsed["db"]
  port := parsed["port"]
  store := Store.open db
  handler := StoreBackedHandler store
  server := TFTPServer --storage=handler --port=port --logger=log.default
  print "gateway: serving command queue + payloads on UDP/$port (db=$db)"
  server.start
```

- [ ] **Step 2: Wire the subcommand into the CLI**

In `gateway/gateway.toit`, add the import near the other `.`-imports:
```toit
import .serve show cmd-serve DEFAULT-PORT
```
In `build-command`, before the final `return cli.Command "gateway" …`, add:
```toit
  serve-cmd := cli.Command "serve"
      --help="Run the gateway daemon: serve the command queue and payloads over TFTP/UDP."
      --options=[ cli.OptionInt "port" --help="UDP port to listen on." --default=DEFAULT-PORT ]
      --run=:: cmd-serve it
```
and add `serve-cmd` to the `--subcommands` list:
```toit
      --subcommands=[ serve-cmd, scan-cmd, ping-cmd, device-cmd, container-cmd, log-cmd ]
```

- [ ] **Step 3: Verify it compiles**

Run (from `porta/gateway`): `$TS analyze gateway.toit`
Expected: exit 0, no errors.

- [ ] **Step 4: Smoke-test the daemon boots and binds** (manual, time-boxed)

Run:
```bash
$TS run gateway.toit -- --db=/tmp/b2-smoke.db serve --port=6969 &
SRV=$!; sleep 1
# Confirm it printed the serving line and is listening, then stop it.
kill $SRV 2>/dev/null
```
Expected: the process prints `gateway: serving command queue + payloads on UDP/6969 …` and stays up until killed (no crash, no stack trace). Remove `/tmp/b2-smoke.db` afterward.

- [ ] **Step 5: Commit**

```bash
git add gateway/serve.toit gateway/gateway.toit
git commit -m "feat(gateway): serve daemon (TFTPServer over the store-backed handler)"
```

---

### Task 8: Add `size` to the `run` command

The device's `ImageStreamWriter` needs the image size up front (to size the install and verify length). It rides in the `run` command (as `image_writer.toit` already documents: "metadata rides in the command now"). The gateway knows the size at install time.

**Files:**
- Modify: `gateway/command.toit`
- Test: `gateway/command_test.toit`
- Modify: `gateway/gateway.toit` (`cmd-container-install` passes the size)

- [ ] **Step 1: Write the failing test** (append to `command_test.toit`'s `main`)

```toit
  // run carries size so the device can size its image writer from the command alone.
  rc := Command.run --name="blink" --crc=999 --size=2048 --triggers={"interval": 30}
  expect-equals 2048 rc.size
  decoded := Command.decode rc.encode
  expect-equals 2048 decoded.size
  expect-equals 999 decoded.crc
```

- [ ] **Step 2: Run it to verify it fails**

Run: `$TS run command_test.toit`
Expected: FAIL — `Command.run` has no `--size`, and `Command.size` does not exist.

- [ ] **Step 3: Implement** in `gateway/command.toit`

Change `Command.run`'s signature and args map to include `size`:
```toit
  static run --name/string --crc/int --size/int --triggers/Map --runlevel/int=3 --arguments/List=[] -> Command:
    return Command VERB-RUN {
      "name": name,
      "crc": crc,
      "size": size,
      "triggers": triggers,
      "runlevel": runlevel,
      "arguments": arguments,
    }
```
Add the accessor alongside the others (e.g. after `crc -> int?`):
```toit
  size -> int?: return args.get "size"
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `$TS run command_test.toit`
Expected: PASS.

- [ ] **Step 5: Update the install caller** in `gateway/gateway.toit` `cmd-container-install`

Change the `run-cmd` line to pass the size:
```toit
  run-cmd := Command.run --name=name --crc=crc --size=image.size --triggers=triggers --runlevel=parsed["runlevel"]
```

- [ ] **Step 6: Verify everything still compiles and the suite is green**

Run (from `porta/gateway`):
```bash
$TS analyze gateway.toit && for t in command_test store_test handler_test integration_test; do $TS run $t.toit && echo "$t PASS"; done
```
Expected: analyze clean; each test prints `… PASS` (or `integration OK`).

- [ ] **Step 7: Commit**

```bash
git add gateway/command.toit gateway/command_test.toit gateway/gateway.toit
git commit -m "feat(gateway): run command carries image size for the device writer"
```

---

# Phase 2 — Device: drain / apply / fetch / report

### Task 9: Base-MAC → node id (pure)

**Files:**
- Create: `device/node_id.toit`
- Test: `device/node_id_test.toit`

- [ ] **Step 1: Write the failing test**

`device/node_id_test.toit`:
```toit
import expect show *
import .node_id show mac-to-id

main:
  expect-equals "a0b1c2d3e4f5" (mac-to-id #[0xa0, 0xb1, 0xc2, 0xd3, 0xe4, 0xf5])
  expect-equals "000000000001" (mac-to-id #[0x00, 0x00, 0x00, 0x00, 0x00, 0x01])
  expect-equals "ffffffffffff" (mac-to-id #[0xff, 0xff, 0xff, 0xff, 0xff, 0xff])
```

- [ ] **Step 2: Run it to verify it fails**

Run (from `porta/device`): `$TS run node_id_test.toit`
Expected: FAIL — `node_id.toit` does not exist.

- [ ] **Step 3: Implement**

`device/node_id.toit`:
```toit
// device/node_id.toit — the node's stable identity for ?id=<mac> requests.

/**
Formats a base MAC ($mac, 6 bytes from esp32.mac-address) as 12 lowercase hex
  digits with no separators (e.g. #[0xa0,…] → "a0b1c2d3e4f5"). The base MAC is
  stable across deep-sleep and reflash, so it is the node's primary key.
*/
mac-to-id mac/ByteArray -> string:
  hex := "0123456789abcdef"
  out := ByteArray (mac.size * 2)
  mac.size.repeat: | i |
    b := mac[i]
    out[i * 2] = hex[(b >> 4) & 0xf]
    out[i * 2 + 1] = hex[b & 0xf]
  return out.to-string
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `$TS run node_id_test.toit`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add device/node_id.toit device/node_id_test.toit
git commit -m "feat(device): mac-to-id node identity helper"
```

---

### Task 10: On-device command decode + apply-to-goal

The device receives one command JSON per drain step and applies it to a goal-app
map (the same shape `GoalState.parse` consumes). This mirrors the gateway's
`project`, but is the device package's own minimal **decoder + applier** (no encode).
The wire form must stay in sync with `gateway/command.toit`.

**Files:**
- Create: `device/node_command.toit`
- Test: `device/node_command_test.toit`

- [ ] **Step 1: Write the failing test**

`device/node_command_test.toit`:
```toit
import expect show *
import .node_command show NodeCommand apply-to-goal

main:
  // Decode a run command from its wire JSON.
  run := NodeCommand.decode "{\"verb\":\"run\",\"name\":\"blink\",\"crc\":999,\"size\":2048,\"triggers\":{\"interval\":30},\"runlevel\":3,\"arguments\":[]}".to-byte-array
  expect-equals "run" run.verb
  expect-equals "blink" run.name

  // run inserts/replaces the app in the goal map with the fields GoalState needs.
  goal := {:}
  apply-to-goal goal run
  expect-structural-equals
      {"blink": {"size": 2048, "crc": 999, "triggers": {"interval": 30}, "runlevel": 3, "arguments": []}}
      goal

  // A later run for the same name wins (absolute/idempotent).
  run2 := NodeCommand.decode "{\"verb\":\"run\",\"name\":\"blink\",\"crc\":1000,\"size\":4096,\"triggers\":{\"boot\":1},\"runlevel\":2,\"arguments\":[]}".to-byte-array
  apply-to-goal goal run2
  expect-equals 1000 goal["blink"]["crc"]
  expect-equals 4096 goal["blink"]["size"]

  // stop removes the app.
  stop := NodeCommand.decode "{\"verb\":\"stop\",\"name\":\"blink\"}".to-byte-array
  apply-to-goal goal stop
  expect-structural-equals {:} goal

  // set-poll-interval does not touch the goal; it exposes its interval.
  spi := NodeCommand.decode "{\"verb\":\"set-poll-interval\",\"interval\":5}".to-byte-array
  expect spi.is-set-poll
  expect-equals 5 spi.interval-s
  apply-to-goal goal spi
  expect-structural-equals {:} goal
```

- [ ] **Step 2: Run it to verify it fails**

Run (from `porta/device`): `$TS run node_command_test.toit`
Expected: FAIL — `node_command.toit` does not exist.

- [ ] **Step 3: Implement**

`device/node_command.toit`:
```toit
// device/node_command.toit — on-device decode of a gateway command and its
// application to the node's goal-app map. The wire form mirrors
// gateway/command.toit (decode only; the device never encodes commands).
import encoding.json

VERB-RUN ::= "run"
VERB-STOP ::= "stop"
VERB-SET-POLL-INTERVAL ::= "set-poll-interval"

/** One command pulled from the gateway, as a verb plus its argument map. */
class NodeCommand:
  verb/string
  args/Map
  constructor .verb .args:

  /** Decodes a command from its JSON wire form $bytes. */
  static decode bytes/ByteArray -> NodeCommand:
    obj := json.decode bytes
    a := {:}
    obj.do: | key value | if key != "verb": a[key] = value
    return NodeCommand obj["verb"] a

  name -> string?: return args.get "name"
  crc -> int?: return args.get "crc"
  size -> int?: return args.get "size"
  interval-s -> int?: return args.get "interval"
  is-set-poll -> bool: return verb == VERB-SET-POLL-INTERVAL

/**
Applies $command to the goal-app map $goal (name → {"size","crc","triggers",
  "runlevel","arguments"}, the shape GoalState.parse consumes). A run sets/replaces
  its app; a stop removes it; set-poll-interval does not affect the app set.
*/
apply-to-goal goal/Map command/NodeCommand -> none:
  if command.verb == VERB-RUN:
    goal[command.args["name"]] = {
      "size": command.args["size"],
      "crc": command.args["crc"],
      "triggers": command.args.get "triggers" --if-absent=: {:},
      "runlevel": command.args.get "runlevel" --if-absent=: 3,
      "arguments": command.args.get "arguments" --if-absent=: [],
    }
  else if command.verb == VERB-STOP:
    goal.remove command.args["name"]
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `$TS run node_command_test.toit`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add device/node_command.toit device/node_command_test.toit
git commit -m "feat(device): decode gateway commands and apply them to the goal map"
```

---

### Task 11: Build the state-report body

**Files:**
- Create: `device/report.toit`
- Test: `device/report_test.toit`

- [ ] **Step 1: Write the failing test**

`device/report_test.toit`:
```toit
import expect show *
import encoding.json
import uuid
import .report show build-report
import .inventory show Inventory InstalledApp
import .triggers show Triggers

main:
  app := InstalledApp
      --name="blink"
      --id=(uuid.Uuid #[0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15])
      --size=2048
      --crc=999
      --triggers=(Triggers --interval-s=30)
      --runlevel=3
  inv := Inventory {"blink": app}

  body := build-report inv --uptime-us=1_000_000 --wakes=7
  obj := json.decode body
  expect-equals 999 obj["apps"]["blink"]["crc"]
  expect-equals 3 obj["apps"]["blink"]["runlevel"]
  expect-equals 30 obj["apps"]["blink"]["triggers"]["interval"]
  expect-equals 7 obj["health"]["wakes"]
  expect-equals 1_000_000 obj["health"]["uptime_us"]

  // An empty inventory still produces a well-formed report.
  empty := build-report Inventory.empty --uptime-us=5 --wakes=1
  expect-structural-equals {:} (json.decode empty)["apps"]
```

- [ ] **Step 2: Run it to verify it fails**

Run (from `porta/device`): `$TS run report_test.toit`
Expected: FAIL — `report.toit` does not exist.

- [ ] **Step 3: Implement**

`device/report.toit`:
```toit
// device/report.toit — builds the per-wake state-report body the node PUTs to
// the gateway (WRQ "report?id=<mac>"): observed apps + a small health struct.
import encoding.json
import .inventory show Inventory InstalledApp

/**
Builds the report body as a JSON object {"apps":{name:{crc,runlevel,triggers}},
  "health":{uptime_us,wakes}}. Carries no per-app logs and is bounded by the app
  count (M1's soft cap lives in the supervisor). $uptime-us is monotonic time;
  $wakes is the cumulative wake count.
*/
build-report inventory/Inventory --uptime-us/int --wakes/int -> ByteArray:
  apps := {:}
  inventory.apps.do: | name/string a/InstalledApp |
    apps[name] = {
      "crc": a.crc,
      "runlevel": a.runlevel,
      "triggers": a.triggers.to-map,
    }
  return json.encode {
    "apps": apps,
    "health": {"uptime_us": uptime-us, "wakes": wakes},
  }
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `$TS run report_test.toit`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add device/report.toit device/report_test.toit
git commit -m "feat(device): build the per-wake state-report body"
```

---

### Task 12: Seed the goal map from the inventory

When a wake drains commands, it applies them on top of the node's **current** goal,
which is reconstructed from the persistent `Inventory`. This helper does that
reconstruction so re-applying an already-applied `run` is a no-op (idempotent).

**Files:**
- Modify: `device/inventory.toit` (add `to-goal-map`)
- Test: `device/inventory_test.toit`

- [ ] **Step 1: Write the failing test** (append to `inventory_test.toit`'s `main`; reuse its existing imports for `Inventory`/`InstalledApp`/`Triggers`/`uuid`, adding any it lacks)

```toit
  // to-goal-map reconstructs the goal-app map (GoalState.parse shape) from inventory.
  a := InstalledApp
      --name="blink"
      --id=(uuid.Uuid #[0,1,2,3,4,5,6,7,8,9,10,11,12,13,14,15])
      --size=2048 --crc=999
      --triggers=(Triggers --interval-s=30) --runlevel=3
  gm := (Inventory {"blink": a}).to-goal-map
  expect-equals 2048 gm["blink"]["size"]
  expect-equals 999 gm["blink"]["crc"]
  expect-equals 30 gm["blink"]["triggers"]["interval"]
  expect-equals 3 gm["blink"]["runlevel"]
  expect-structural-equals {:} Inventory.empty.to-goal-map
```

- [ ] **Step 2: Run it to verify it fails**

Run (from `porta/device`): `$TS run inventory_test.toit`
Expected: FAIL — `to-goal-map` does not exist.

- [ ] **Step 3: Implement** — add this method to the `Inventory` class in `device/inventory.toit`

```toit
  /**
  Reconstructs the goal-app map (name → {"size","crc","triggers","runlevel",
    "arguments"}, the shape GoalState.parse consumes) from the installed apps, so a
    wake can apply freshly drained commands on top of the node's current goal.
  */
  to-goal-map -> Map:
    goal := {:}
    apps.do: | name/string a/InstalledApp |
      goal[name] = {
        "size": a.size,
        "crc": a.crc,
        "triggers": a.triggers.to-map,
        "runlevel": a.runlevel,
        "arguments": [],
      }
    return goal
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `$TS run inventory_test.toit`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add device/inventory.toit device/inventory_test.toit
git commit -m "feat(device): reconstruct the goal map from the inventory"
```

---

### Task 13: Add a WRQ `put` to the device transport

**Files:**
- Modify: `device/transport.toit`

- [ ] **Step 1: Add `put` to the `GatewayClient` interface**

In `device/transport.toit`, add to the `GatewayClient` interface (after `fetch`):
```toit
  /** Writes $bytes to the gateway under resource $name (a WRQ, e.g. the report). */
  put name/string bytes/ByteArray -> none
```

- [ ] **Step 2: Implement it on `TftpGatewayClient`**

Add to the `TftpGatewayClient` class (after `fetch`):
```toit
  put name/string bytes/ByteArray -> none:
    client_.write-bytes bytes --filename=name
```

- [ ] **Step 3: Verify it compiles** (against the device SDK that builds the firmware)

Run (from `porta/device`):
```bash
$TS analyze transport.toit
```
Expected: exit 0. (This uses `toit-sqlite`'s SDK only to type-check; the file imports `tftp` + `io`, both resolvable. The hardware build in Phase 3 is the real gate.)

- [ ] **Step 4: Commit**

```bash
git add device/transport.toit
git commit -m "feat(device): WRQ put on the gateway client (for the state report)"
```

---

### Task 14: A cumulative wake counter in the schedule store

The report's health carries a wake count. RTC user memory survives deep-sleep, so
the counter lives next to `last-poll-us`.

**Files:**
- Modify: `device/schedule_store.toit`

- [ ] **Step 1: Extend the RTC layout with a wake counter**

In `device/schedule_store.toit`, update the layout comment and the class. Add a
counter at byte offset 12 (`int64`, little-endian), bumped once per construction:

Replace the class body with:
```toit
class ScheduleStore:
  static MAGIC_ ::= 0x50_4f_52_54  // "PORT"
  rtc_/ByteArray

  constructor:
    rtc_ = esp32.rtc-user-bytes
    if (LITTLE-ENDIAN.uint32 rtc_ 0) != MAGIC_:
      // Cold boot: initialise. last-poll = 0 so the first wake polls; wakes = 0.
      LITTLE-ENDIAN.put-uint32 rtc_ 0 MAGIC_
      LITTLE-ENDIAN.put-int64 rtc_ 4 0
      LITTLE-ENDIAN.put-int64 rtc_ 12 0
    // Count this wake.
    LITTLE-ENDIAN.put-int64 rtc_ 12 (LITTLE-ENDIAN.int64 rtc_ 12) + 1

  last-poll-us -> int:
    return LITTLE-ENDIAN.int64 rtc_ 4

  last-poll-us= value/int -> none:
    LITTLE-ENDIAN.put-int64 rtc_ 4 value

  /** Cumulative wake count since the last cold boot (incl. this wake). */
  wakes -> int:
    return LITTLE-ENDIAN.int64 rtc_ 12
```
Update the layout comment block to document `[12:20] wake-count (int64, little-endian)` and that the counter increments on each construction.

- [ ] **Step 2: Verify it compiles**

Run (from `porta/device`): `$TS analyze schedule_store.toit`
Expected: exit 0.

- [ ] **Step 3: Commit**

```bash
git add device/schedule_store.toit
git commit -m "feat(device): cumulative wake counter in the RTC schedule store"
```

---

### Task 15: Rewrite the supervisor — drain / apply / fetch / remove / report

This replaces the single `GET goal` with the full wake cycle. It is **analyze-only**
on host; Phase 3 verifies it on hardware.

**Files:**
- Rewrite: `device/supervisor.toit`

- [ ] **Step 1: Rewrite `supervisor.toit`**

```toit
// device/supervisor.toit
import esp32
import encoding.json
import system.storage
import system.containers

import .goal_state show GoalState
import .inventory show Inventory InstalledApp
import .node_command show NodeCommand apply-to-goal
import .node_id show mac-to-id
import .report show build-report
import .image_writer show ImageStreamWriter
import .flash_image show ContainerImageInstaller
import .transport show WifiTransport GatewayClient
import .schedule_store show ScheduleStore clock-us

/** Gateway LAN address. Adjust to the host running `gateway serve`. */
GATEWAY-HOST ::= "192.168.0.175"
GATEWAY-PORT ::= 6969

/** Fallback poll cadence (seconds) before the node has been told otherwise. */
DEFAULT-POLL-S ::= 30
/** How long to stay awake observing started payloads before sleeping. */
OBSERVE ::= Duration --s=5

/** NVS bucket + keys for persistent node-local state. */
BUCKET-NAME ::= "porta"
INVENTORY-KEY ::= "inventory"
POLL-INTERVAL-KEY ::= "poll_interval_s"

/**
One supervisor wake: identify, poll the gateway if due (drain commands → apply →
  reconcile → fetch/remove → report), start the installed payloads, then deep-sleep
  for the node-local poll interval. Deep-sleep wakes via full reboot, so $main is
  linear and the reboot is the loop.
*/
main:
  print "supervisor: awake (cause=$esp32.wakeup-cause)"
  id := mac-to-id esp32.mac-address
  bucket := storage.Bucket.open --flash BUCKET-NAME
  inventory := load-inventory bucket
  poll-interval-s := bucket.get POLL-INTERVAL-KEY --if-absent=: DEFAULT-POLL-S
  store := ScheduleStore
  now := clock-us

  // Poll on cold boot (empty inventory) or once the poll interval has elapsed.
  cold := inventory.apps.is-empty
  poll-due := cold or (now - store.last-poll-us) >= (poll-interval-s * 1_000_000)
  if poll-due:
    // Never strand the node awake on a transient failure: trace and still sleep.
    catch --trace:
      poll-interval-s = poll-and-reconcile bucket inventory id poll-interval-s store
      store.last-poll-us = now

  start-installed inventory
  arm-wakeups inventory

  print "supervisor: observing for $OBSERVE"
  sleep OBSERVE
  print "supervisor: deep-sleeping for $(poll-interval-s)s"
  esp32.deep-sleep (Duration --s=poll-interval-s)

/** Loads the inventory from NVS, or an empty one if none/garbage. */
load-inventory bucket/storage.Bucket -> Inventory:
  tree := bucket.get INVENTORY-KEY --if-absent=: null
  if tree == null: return Inventory.empty
  return Inventory.decode tree

save-inventory bucket/storage.Bucket inventory/Inventory -> none:
  bucket[INVENTORY-KEY] = inventory.encode

/**
Connects, drains the command queue applying each command to a goal seeded from the
  current inventory, reconciles (fetch new/changed images, remove dropped apps),
  reports observed state, and returns the (possibly updated) poll interval.
*/
poll-and-reconcile bucket/storage.Bucket inventory/Inventory id/string poll-interval-s/int store/ScheduleStore -> int:
  print "supervisor: polling $GATEWAY-HOST:$GATEWAY-PORT as id=$id"
  client/GatewayClient := (WifiTransport --host=GATEWAY-HOST --port=GATEWAY-PORT).connect
  try:
    goal-map := inventory.to-goal-map
    // Drain: each "commands" RRQ returns the oldest undelivered command, or a
    // zero-byte body when the queue is exhausted.
    while true:
      bytes := client.fetch-bytes "commands?id=$id"
      if bytes.is-empty: break
      command := NodeCommand.decode bytes
      if command.is-set-poll:
        poll-interval-s = command.interval-s
        bucket[POLL-INTERVAL-KEY] = poll-interval-s
        print "supervisor: poll interval now $(poll-interval-s)s"
      else:
        apply-to-goal goal-map command
        print "supervisor: applied $command.verb $(command.name)"

    goal := GoalState.parse (json.encode {"apps": goal-map})
    recon := inventory.reconcile goal
    recon.to-fetch.do: | app |
      print "supervisor: fetching $app.name ($app.size B, crc=$app.crc)"
      installer := ContainerImageInstaller
      writer := ImageStreamWriter installer --size=app.size --crc=app.crc
      client.fetch "payload?id=$id&name=$app.name&crc=$app.crc" --to-writer=writer
      image-id := writer.commit
      inventory.apps[app.name] = InstalledApp --name=app.name --id=image-id --size=app.size --crc=app.crc --triggers=app.triggers --runlevel=app.runlevel
      print "supervisor: installed $app.name -> $image-id"
    recon.to-remove.do: | a/InstalledApp |
      print "supervisor: removing $a.name"
      catch --trace: containers.uninstall a.id
      inventory.apps.remove a.name
    save-inventory bucket inventory

    // Report observed state before sleeping (audit + convergence).
    body := build-report inventory --uptime-us=clock-us --wakes=store.wakes
    client.put "report?id=$id" body
    print "supervisor: reported $(inventory.apps.size) app(s)"
    return poll-interval-s
  finally:
    client.close

/** Starts every installed app (deep-sleep cleared running state each wake). */
start-installed inventory/Inventory -> none:
  inventory.apps.do: | name/string a/InstalledApp |
    containers.start a.id
    print "supervisor: started $name ($a.id)"

/** Re-arms GPIO (ext1) wake sources declared by installed apps' triggers. */
arm-wakeups inventory/Inventory -> none:
  mask := 0
  inventory.apps.do: | _ a/InstalledApp | mask |= a.triggers.ext1-high-mask
  if mask != 0:
    esp32.enable-external-wakeup mask true
    print "supervisor: armed ext1 wake mask=0x$(%x mask)"
```

- [ ] **Step 2: Verify it analyzes clean**

Run (from `porta/device`): `$TS analyze supervisor.toit`
Expected: exit 0, no errors. (If `containers.uninstall` is not the exact SDK name, resolve it via the `toit-code` skill — search `system.containers` for the uninstall primitive — and adjust. Likewise confirm `Triggers` accepts `--interval-s` as used in tests.)

- [ ] **Step 3: Run the full device + gateway host suites once more**

Run:
```bash
cd ~/workspaceToit/porta/device && for t in node_id_test node_command_test report_test inventory_test goal_state_test triggers_test image_writer_test; do $TS run $t.toit && echo "$t PASS"; done
cd ~/workspaceToit/porta/gateway && for t in command_test store_test handler_test integration_test; do $TS run $t.toit && echo "$t PASS"; done
```
Expected: every line ends `PASS` (or `integration OK`).

- [ ] **Step 4: Commit**

```bash
cd ~/workspaceToit/porta
git add device/supervisor.toit
git commit -m "feat(device): supervisor drains commands, reconciles, and reports each wake"
```

---

# Phase 3 — Hardware verification on `fwkb`

> This phase runs against real hardware and the live gateway. Use the
> **toit-jag-dev** skill for flashing/running and **toit-exe** for command syntax.
> Do not claim success without the observed serial output and the gateway rows.

### Task 16: End-to-end hardware verification, then retire `host/serve.toit`

**Files:**
- Delete (at the end): `host/serve.toit`

- [ ] **Step 1: Set the gateway host address**

Confirm `GATEWAY-HOST`/`GATEWAY-PORT` in `device/supervisor.toit` match the machine that will run `gateway serve` on the `fwkb` LAN. Edit if needed (do not commit a machine-specific change unless it is the intended default).

- [ ] **Step 2: Start the gateway daemon**

Run on the gateway host:
```bash
export TS=~/workspaceToit/sqlite/build/bin/toit-sqlite
cd ~/workspaceToit/porta/gateway
$TS run gateway.toit -- --db=/tmp/porta-fwkb.db serve --port=6969
```
Expected: `gateway: serving command queue + payloads on UDP/6969 …`. Leave it running.

- [ ] **Step 3: Build the firmware and flash `fwkb`**

Use the **toit-jag-dev** skill to build the supervisor firmware and flash it to
`fwkb` (classic ESP32/Xtensa, per CLAUDE.md). The node has no inventory yet, so it
cold-boots and immediately polls. Watch the serial log for `supervisor: awake`,
`polling … as id=<mac>`, and the drained-empty path (no commands yet), then a
`reported 0 app(s)` line.

- [ ] **Step 4: Confirm first contact registered the node**

On the gateway host, in another shell:
```bash
$TS run gateway.toit -- --db=/tmp/porta-fwkb.db scan --include-never-seen
```
Expected: one row whose `DEVICE` is `fwkb`'s base MAC, an auto-assigned `NAME`, a recent `LAST-SEEN`, and `online`. Record the MAC as `$MAC`.

- [ ] **Step 5: Tighten the poll cadence for the dev loop**

```bash
$TS run gateway.toit -- --db=/tmp/porta-fwkb.db device set-poll-interval -d $MAC 1s
```
Expected: `…: enqueued set-poll-interval 1s (command #N)`. On the next wake the
serial log shows `poll interval now 1s`; subsequent wakes are ~1 s apart.

- [ ] **Step 6: Install a container and watch it converge**

Use a known-good prebuilt image (e.g. the loader spike's captured `blink.bin`, or
build one with the **toit-envelope**/**toit-exe** skills). Then:
```bash
$TS run gateway.toit -- --db=/tmp/porta-fwkb.db container install blink ./blink.bin -d $MAC --interval=30s
```
Expected serial sequence on the next wake: `applied run blink` → `fetching blink (… B, crc=…)` → `installed blink -> <uuid>` → `started blink (<uuid>)` → `reported 1 app(s)`, then the payload's own output. Confirm the gateway side:
```bash
$TS run gateway.toit -- --db=/tmp/porta-fwkb.db container list -d $MAC   # shows blink@crc
$TS run gateway.toit -- --db=/tmp/porta-fwkb.db log -d $MAC              # run command marked delivered
$TS run gateway.toit -- --db=/tmp/porta-fwkb.db device show -d $MAC      # observed state has blink; queue undelivered = 0
```
Expected: `container list` lists `blink` with its crc; `log` shows the `run` row with a non-empty `DELIVERED`; `device show` shows the observed app and `queued (undelivered): 0`.

- [ ] **Step 7: Verify idempotent redelivery / self-healing**

Re-issue the same install (`container install blink ./blink.bin -d $MAC --interval=30s`).
Expected: the node applies it as a no-op (`blink` already at that crc → reconcile
yields no fetch), the report still shows `blink@crc`, and the new command is marked
delivered. This confirms absolute commands are safe to redeliver.

- [ ] **Step 8: Verify uninstall**

```bash
$TS run gateway.toit -- --db=/tmp/porta-fwkb.db container uninstall blink -d $MAC
```
Expected serial: `removing blink` on the next wake; the payload stops running;
`container list -d $MAC` no longer lists it and `device show` reports 0 apps.

- [ ] **Step 9: Verify a production-cadence deep-sleep cycle**

```bash
$TS run gateway.toit -- --db=/tmp/porta-fwkb.db device set-poll-interval -d $MAC 30s
```
Expected: the node sleeps ~30 s between wakes, re-polls (empty queue → zero-byte
drain), and re-reports — confirming the deep-sleep → wake → re-poll loop and a fresh
report row each wake (check `device show` `last-seen` advancing).

- [ ] **Step 10: Retire `host/serve.toit`**

With the daemon proven, remove the throwaway harness:
```bash
cd ~/workspaceToit/porta
git rm host/serve.toit
```
Then check whether `host/goal.toit` / `host/goal_test.toit` (the goal-builder the old
`serve.toit` used) are now unreferenced; if nothing imports them, remove them too. If
the `host/` package becomes empty of source, note it for a later cleanup rather than
deleting the package wholesale here.

- [ ] **Step 11: Commit**

```bash
git add -A host/
git commit -m "chore(host): retire serve.toit — superseded by the gateway serve daemon"
```

- [ ] **Step 12: Record the hardware-verified result**

Update the project memory (`porta-toit-gateway.md` + `MEMORY.md`) to mark B2
code-complete and hardware-verified on `fwkb`, with the commit range, and note that
M1 (the whole Spec B M1) is now done end-to-end. (Per the memory conventions: one
file, update the existing entry rather than adding a duplicate.)

---

## Self-review notes (spec coverage)

- **Wire protocol step 1 (drain + delivery)** → Tasks 4, 6 (serve oldest-undelivered, zero-byte sentinel, mark-delivered on transfer-complete) + Task 15 (device drain loop).
- **Step 2 (payload on demand)** → Task 4 (handler `payload` reader) + Task 15 (device `payload?id=&name=&crc=` fetch). Size rides in the command (Task 8) so the writer is sized without relying on tsize.
- **Step 3 (apply + reconcile)** → Tasks 10, 12, 15 (apply-to-goal, seed-from-inventory, reconcile incl. `to-remove`).
- **Step 4 (report)** → Tasks 5, 11, 13, 14 (handler ingest, report body, WRQ put, wake counter).
- **Step 5 (sleep at node-local cadence)** → Tasks 14/15 (persisted `poll_interval_s`, `set-poll-interval` command).
- **CLI `serve`** → Task 7. **Node identity `?id=<mac>`** → Tasks 9, 15.
- **Hardware verify on `fwkb` + retire `host/serve.toit`** → Task 16.
- **Spec A dependency** is gated explicitly in Task 1 (and the package wiring in Task 2).
- Deferred by design (not in B2): M2 telemetry/`monitor`, M3 `.pod`, M4 `.toit`, M5 MCP, smalltalk nodes, free-heap in health (health is uptime+wakes for M1).
