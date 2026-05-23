# TFTP engine / transport / handler split — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Refactor `~/workspaceToit/tftp` so its RFC-1350 block engine is transport-neutral, the request handler sees the peer + raw query, and a transfer-complete event fires — without changing the hardware-verified `TFTPClient` API or wire behavior.

**Architecture:** Inject a `PacketChannel` (with an opaque `Peer` token) under the *unchanged* `Exchange` drive/retry/TID/OACK loop; evolve the existing `Storage` interface in place with a `Request` context and an `ok`-tagged `on-transfer-complete` hook. No second transport and no pure-stepped-engine rewrite. See spec `docs/specs/2026-05-23-tftp-engine-transport-handler-split-design.md`.

**Tech Stack:** Toit (SDK v2.0.0-alpha.192), `toit run` for tests, `pkg-test`/`expect` for assertions, package deps `pkg-host`/`pkg-cli`.

**Conventions (toit-conventions skill):** kebab-case methods/vars, PascalCase classes, KEBAB-CASE constants, `_` suffix = private, 2-space indent / 4-space continuation, Toitdocs start with a 3rd-person verb and use `$` for code refs.

**Where things live:** package source is `~/workspaceToit/tftp/src/`; tests are `~/workspaceToit/tftp/tests/` (a separate package with a `path: ..` dep on the parent — so tests import `tftp` normally). All test commands run **from `~/workspaceToit/tftp/tests/`** because tests read relative paths.

**Baseline facts (verified):** `toit` is at `/home/david/.local/bin/toit`, `toit version` → `v2.0.0-alpha.192`. There is no `toit test` subcommand; run a test with `toit run <file>`. The existing tests (`roundtrip_test`, `options_test`, etc.) require an *external* TFTP server and are **not** part of this gate; the deterministic gate is the new in-process test from Task 1.

---

## File structure (created / modified)

| File | Responsibility | Tasks |
|---|---|---|
| `src/channel.toit` | **NEW.** `Peer`, `Datagram`, `PacketChannel` interface, `UdpPeer`, `UdpChannel`. The transport seam. | 2 |
| `src/exchange.toit` | **MODIFY.** Retype socket→`channel_`, `SocketAddress`→`Peer`; logic unchanged. | 3 |
| `src/tftp_client.toit` | **MODIFY.** `ClientExchange` builds `UdpChannel`/`UdpPeer`; `is-acceptable-source_` reads `UdpPeer.ip`. Public API frozen. | 3 |
| `src/tftp_server.toit` | **MODIFY.** Per-transfer task builds `UdpChannel`; `ServerExchange` builds `Request`, passes it to `Storage`, tracks bytes, fires `on-transfer-complete`. | 3, 5 |
| `src/storage.toit` | **MODIFY.** Add `Request`; add optional `--req` to methods; add defaulted `on-transfer-complete`; adapt `FilesystemStorage`. | 4 |
| `src/tftp.toit` | **MODIFY.** Export the new public symbols. | 2, 4 |
| `tests/inproc_roundtrip_test.toit` | **NEW.** Self-contained server+client roundtrip — the refactor gate. | 1 |
| `tests/channel_test.toit` | **NEW.** `UdpPeer` equality + `UdpChannel` roundtrip/timeout. | 2 |
| `tests/fake_channel.toit` | **NEW.** `FakeChannel` test helper (scripted inbound, recorded outbound). | 5 |
| `tests/transfer_complete_test.toit` | **NEW.** `on-transfer-complete` + `Request` coverage (RRQ/WRQ success, abort). | 5 |

---

## Task 1: Self-contained in-process roundtrip test (the refactor gate)

This is a **characterization test** written before any refactor: it pins current behavior so every later task can prove it didn't regress. It exercises the real `TFTPClient` + `TFTPServer` in one process on an unprivileged port, including a multi-block transfer.

**Files:**
- Test: `tests/inproc_roundtrip_test.toit`

- [ ] **Step 1: Write the test**

Create `~/workspaceToit/tftp/tests/inproc_roundtrip_test.toit`:

```
// Copyright 2026 Ekorau LLC.
// Self-contained: runs a TFTPServer task + TFTPClient in-process on an
// unprivileged port. The deterministic gate for the engine/transport refactor.
import expect show *
import host.directory
import host.file
import tftp show FilesystemStorage TFTPServer TFTPClient

PORT ::= 6969
ROOT ::= "/tmp/tftp-inproc-roundtrip"

main:
  if not file.is-directory ROOT: directory.mkdir --recursive ROOT
  storage := FilesystemStorage --root=ROOT --allow-overwrite
  server := TFTPServer --storage=storage --port=PORT
  server-task := task:: server.start
  sleep --ms=200  // Let the listen socket bind.
  try:
    client := TFTPClient --host="127.0.0.1"
    client.port = PORT
    client.open
    try:
      // Single-block string roundtrip.
      small := "hello tftp roundtrip"
      client.write-string small --filename="small.txt"
      expect-equals small (client.read-bytes "small.txt").to-string

      // Multi-block roundtrip (>512 B forces several DATA blocks).
      big := ByteArray 3000: it & 0xff
      client.write-bytes big --filename="big.bin"
      got := client.read-bytes "big.bin"
      expect-equals big.size got.size
      expect (big == got)

      print "INPROC ROUNDTRIP OK (small=$small.size B, big=$big.size B)"
    finally:
      client.close
  finally:
    server.stop
    server-task.cancel
```

- [ ] **Step 2: Run the test to verify current behavior passes**

Run (from the tests directory):
```bash
cd ~/workspaceToit/tftp/tests && toit run inproc_roundtrip_test.toit
```
Expected: prints `INPROC ROUNDTRIP OK ...`, exit code 0, no exception. (This is the green baseline; it must stay green after every later task.)

- [ ] **Step 3: Commit**

```bash
cd ~/workspaceToit/tftp && git add tests/inproc_roundtrip_test.toit && git commit -m "test: self-contained in-process TFTP roundtrip gate"
```

---

## Task 2: `channel.toit` — `Peer` / `Datagram` / `PacketChannel` / `UdpPeer` / `UdpChannel`

New transport seam. Pure addition — nothing imports it yet, so the Task 1 gate stays green.

**Files:**
- Create: `src/channel.toit`
- Modify: `src/tftp.toit`
- Test: `tests/channel_test.toit`

- [ ] **Step 1: Write the failing test**

Create `~/workspaceToit/tftp/tests/channel_test.toit`:

```
// Copyright 2026 Ekorau LLC.
import expect show *
import net
import tftp show Datagram Peer UdpChannel UdpPeer

main:
  test-udp-peer-equality
  test-udp-channel-roundtrip
  test-udp-channel-timeout
  print "channel_test OK"

test-udp-peer-equality:
  a := UdpPeer (net.SocketAddress (net.IpAddress.parse "127.0.0.1") 6969)
  b := UdpPeer (net.SocketAddress (net.IpAddress.parse "127.0.0.1") 6969)
  c := UdpPeer (net.SocketAddress (net.IpAddress.parse "127.0.0.1") 7000)
  expect (a == b)
  expect-equals a.hash-code b.hash-code
  expect (not a == c)

test-udp-channel-roundtrip:
  network := net.open
  rx := network.udp-open --port=6971   // Fixed unprivileged port (loopback proven in Task 1).
  tx := network.udp-open
  try:
    rx-peer := UdpPeer (net.SocketAddress (net.IpAddress.parse "127.0.0.1") 6971)
    (UdpChannel tx).send #[1, 2, 3] --to=rx-peer
    dgram/Datagram? := (UdpChannel rx).receive --deadline-us=(Time.monotonic-us + 1_000_000)
    expect (dgram != null)
    expect (#[1, 2, 3] == dgram.bytes)
  finally:
    rx.close
    tx.close
    network.close

test-udp-channel-timeout:
  network := net.open
  s := network.udp-open
  try:
    dgram := (UdpChannel s).receive --deadline-us=(Time.monotonic-us + 50_000)
    expect (dgram == null)
  finally:
    s.close
    network.close
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
cd ~/workspaceToit/tftp/tests && toit run channel_test.toit
```
Expected: FAIL — compile error, `tftp` does not export `Datagram`/`Peer`/`UdpChannel`/`UdpPeer` (they don't exist yet).

- [ ] **Step 3: Create `src/channel.toit`**

Create `~/workspaceToit/tftp/src/channel.toit`:

```
// Copyright 2026 Ekorau LLC.

import net
import net.udp

/**
An opaque transport peer.

The TFTP engine needs only to compare peers (RFC 1350 §4 transfer-ID
  enforcement) and hand one back to $PacketChannel.send. Concrete transports
  (UDP here; Thread/ESPnow later) provide their own address-bearing subclass.
*/
interface Peer:
  /** Whether $other denotes the same peer. */
  operator == other/Peer -> bool
  /** A hash consistent with $==. */
  hash-code -> int

/** A datagram received from a $Peer. */
class Datagram:
  bytes/ByteArray
  from/Peer
  constructor .bytes .from:

/**
A bidirectional, datagram-oriented channel beneath the TFTP engine.

Receive is a blocking pull (recvfrom semantics). Implementations own the wire:
  $UdpChannel here; Thread/ESPnow/serial channels reuse this interface.
*/
interface PacketChannel:
  /** Sends $bytes to $to. */
  send bytes/ByteArray --to/Peer -> none
  /**
  Receives the next datagram, blocking until one arrives or $deadline-us
    (an absolute $Time.monotonic-us value) passes. Returns null on timeout.
  */
  receive --deadline-us/int -> Datagram?
  /** Closes the underlying wire. */
  close -> none

/** A $Peer wrapping a UDP endpoint. */
class UdpPeer implements Peer:
  socket-address/net.SocketAddress

  constructor .socket-address:

  /** The peer's IP, used by the UDP client's source-acceptance check. */
  ip -> net.IpAddress: return socket-address.ip

  operator == other/Peer -> bool:
    return other is UdpPeer and (other as UdpPeer).socket-address == socket-address

  hash-code -> int: return socket-address.hash-code

/** A $PacketChannel over a UDP socket the caller owns. */
class UdpChannel implements PacketChannel:
  socket_/udp.Socket

  constructor .socket_:

  send bytes/ByteArray --to/Peer -> none:
    socket_.send (udp.Datagram bytes (to as UdpPeer).socket-address)

  receive --deadline-us/int -> Datagram?:
    remaining-us := deadline-us - Time.monotonic-us
    if remaining-us <= 0: return null
    msg/udp.Datagram? := null
    err := catch:
      with-timeout --us=remaining-us:
        msg = socket_.receive
    if err == DEADLINE-EXCEEDED-ERROR or msg == null:
      return null
    if err != null: throw err
    return Datagram msg.data (UdpPeer msg.address)

  close -> none:
    socket_.close
```

- [ ] **Step 4: Export from `src/tftp.toit`**

Modify `~/workspaceToit/tftp/src/tftp.toit` to add the channel import (keep `export *`):

```
import .channel
import .packets
import .sdcard
import .sha256-summer
import .storage
import .tftp-client
import .tftp-server

export *
```

- [ ] **Step 5: Run the test to verify it passes**

```bash
cd ~/workspaceToit/tftp/tests && toit run channel_test.toit
```
Expected: prints `channel_test OK`, exit 0.

- [ ] **Step 6: Run the Task 1 gate (still green)**

```bash
cd ~/workspaceToit/tftp/tests && toit run inproc_roundtrip_test.toit
```
Expected: `INPROC ROUNDTRIP OK ...`.

- [ ] **Step 7: Commit**

```bash
cd ~/workspaceToit/tftp && git add src/channel.toit src/tftp.toit tests/channel_test.toit && git commit -m "feat: PacketChannel/Peer transport seam (UdpChannel)"
```

---

## Task 3: Retype `Exchange` to `PacketChannel` + `Peer` (engine core)

A **logic-preserving refactor**: move the socket behind `PacketChannel` and retype addresses `net.SocketAddress`→`Peer`. The `drive_`/retry/TID/OACK control flow is unchanged. There is no new test here — the Task 1 gate is the safety net.

**Files:**
- Modify: `src/exchange.toit`
- Modify: `src/tftp_client.toit`
- Modify: `src/tftp_server.toit`

- [ ] **Step 1: Retype `Exchange` fields, imports, and constructor (`src/exchange.toit`)**

Replace the imports block (currently `import io` / `import log` / `import net` / `import net.udp` / `import .packets`) with:

```
import io
import log

import .channel
import .packets
```

Change the field `socket_/udp.Socket` to:

```
  channel_/PacketChannel
```

Change the `peer-tid_` field type from `net.SocketAddress?` to `Peer?` (keep the Toitdoc) and `dest_` from `net.SocketAddress?` to `Peer?` (keep the Toitdoc). Change the constructor `constructor .socket_ .logger_:` to:

```
  constructor .channel_ .logger_:
```

- [ ] **Step 2: Retype `send_`, `receive_`, `send-unknown-tid_`, `is-acceptable-source_` (`src/exchange.toit`)**

Replace `send_`:

```
  /** Sends $payload to $dest_. */
  send_ payload/ByteArray -> none:
    channel_.send payload --to=dest_
```

Replace `is-acceptable-source_`'s signature (body/Toitdoc unchanged, default still returns true):

```
  is-acceptable-source_ source/Peer -> bool:
    return true
```

Replace the whole `receive_` method with (timeout handling now delegated to the channel):

```
  receive_ -> Packet:
    deadline-us := Time.monotonic-us + DEFAULT-TIMEOUT-MS_ * 1000
    while true:
      dgram/Datagram? := channel_.receive --deadline-us=deadline-us
      if dgram == null: return PacketTIMEOUT
      if peer-tid_ == null:
        if not is-acceptable-source_ dgram.from:
          send-unknown-tid_ dgram.from
          continue
        peer-tid_ = dgram.from
        dest_ = dgram.from
      else if dgram.from != peer-tid_:
        send-unknown-tid_ dgram.from
        continue
      packet := Packet.deserialize (io.Reader dgram.bytes)
      if packet == null: continue
      return packet
```

Replace `send-unknown-tid_`:

```
  send-unknown-tid_ source/Peer -> none:
    err := PacketERROR 5 "Unknown transfer ID"
    channel_.send err.serialize --to=source
```

(No change to `drive_`, `retry-or-abort_`, `schedule-retransmit_`, `exit-error_`, `apply-oack_`, `on-tsize_`.)

- [ ] **Step 3: Update `ClientExchange` (`src/tftp_client.toit`)**

In the imports, add `import .channel` (alongside the existing `import .exchange` / `import .packets`).

Change the constructor (currently `super client_.socket_ client_.logger_`):

```
  constructor .client_/TFTPClient:
    super (UdpChannel client_.socket_) client_.logger_
```

In `start-with-wrq`, change `dest_ = client_.server-address_` to:

```
    dest_ = UdpPeer client_.server-address_
```

In `start-with-rrq`, change `dest_ = client_.server-address_` to:

```
    dest_ = UdpPeer client_.server-address_
```

Replace `is-acceptable-source_`:

```
  is-acceptable-source_ source/Peer -> bool:
    return source is UdpPeer and (source as UdpPeer).ip == client_.host-ip_
```

- [ ] **Step 4: Update the server dispatcher + `ServerExchange` construction (`src/tftp_server.toit`)**

In the imports, add `import .channel`.

In `dispatch_`, the per-transfer task currently constructs `ServerExchange packet msg.address storage_ ephemeral logger_`. Wrap the ephemeral socket in a `UdpChannel`:

```
    task::
      try:
        ephemeral := network_.udp-open
        try:
          exchange := ServerExchange packet msg.address storage_ (UdpChannel ephemeral) logger_
          exchange.run
        finally:
          ephemeral.close
      finally:
        capacity_.release
```

Change the `ServerExchange` constructor to accept a `PacketChannel` and set `peer-tid_`/`dest_` as `UdpPeer`s. Replace the constructor body's `super socket logger` / `peer-tid_ = source` / `dest_ = source` tail:

```
  constructor initial/Packet source/net.SocketAddress storage/Storage channel/PacketChannel logger/log.Logger:
    initial_ = initial
    source_ = source
    storage_ = storage
    if initial is PacketRRQ:
      filename_ = (initial as PacketRRQ).filename
      mode_ = (initial as PacketRRQ).mode
    else:
      filename_ = (initial as PacketWRQ).filename
      mode_ = (initial as PacketWRQ).mode
    super channel logger
    peer-tid_ = UdpPeer source
    dest_ = UdpPeer source
```

(`send-error_` uses `socket_` today — see Step 5.)

- [ ] **Step 5: Fix `ServerExchange.send-error_` to use the channel (`src/tftp_server.toit`)**

`send-error_` currently does `socket_.send (udp.Datagram err.serialize peer-tid_)`. Replace with the channel send (and it no longer needs the `net.udp` import for this):

```
  send-error_ code/int msg/string -> none:
    catch:
      err := PacketERROR code msg
      channel_.send err.serialize --to=peer-tid_
```

If `net.udp` / `net` imports become unused in `tftp_server.toit` after this, leave them only if still referenced (e.g. `net.SocketAddress` in the `ServerExchange` constructor signature and `net.Interface`/`udp.Socket` in `TFTPServer` — those remain, so keep `import net` and `import net.udp`).

- [ ] **Step 6: Run the Task 1 gate (must stay green) + channel test**

```bash
cd ~/workspaceToit/tftp/tests && toit run inproc_roundtrip_test.toit && toit run channel_test.toit
```
Expected: `INPROC ROUNDTRIP OK ...` then `channel_test OK`. If either fails, the retype changed behavior — fix before committing.

- [ ] **Step 7: Commit**

```bash
cd ~/workspaceToit/tftp && git add src/exchange.toit src/tftp_client.toit src/tftp_server.toit && git commit -m "refactor: inject PacketChannel under Exchange; retype peers to Peer"
```

---

## Task 4: Evolve `Storage` — `Request` context + `on-transfer-complete`

Add the per-request context and the completion hook, both optional/defaulted so `FilesystemStorage` adapts mechanically with no behavior change.

**Files:**
- Modify: `src/storage.toit`
- Modify: `src/tftp.toit` (export `Request`)
- Test: `tests/transfer_complete_test.toit` (created here, extended in Task 5)

- [ ] **Step 1: Write the failing test (Storage-level)**

Create `~/workspaceToit/tftp/tests/transfer_complete_test.toit`:

```
// Copyright 2026 Ekorau LLC.
import expect show *
import io
import net
import tftp show Request Storage UdpPeer

// A minimal Storage that records the Request handed to exists. reader-for /
// writer-for are never invoked by the storage-level tests (they throw to make
// that explicit); on-transfer-complete uses the abstract default no-op.
class RecordingStorage extends Storage:
  last-req/Request? := null

  exists name/string --req/Request?=null -> bool:
    last-req = req
    return true
  size name/string --req/Request?=null -> int?: return null
  reader-for name/string --req/Request?=null -> io.CloseableReader: throw "unused"
  writer-for name/string --req/Request?=null --tsize-hint/int?=null -> io.CloseableWriter: throw "unused"

main:
  test-request-fields
  test-default-on-complete-is-noop
  print "transfer_complete_test (storage-level) OK"

test-request-fields:
  peer := UdpPeer (net.SocketAddress (net.IpAddress.parse "127.0.0.1") 5000)
  req := Request --peer=peer --raw-path="commands?id=abc"
  s := RecordingStorage
  s.exists "commands" --req=req
  expect-equals "commands?id=abc" s.last-req.raw-path
  expect (s.last-req.peer == peer)

test-default-on-complete-is-noop:
  // The inherited default must be callable without an override.
  s := RecordingStorage
  s.on-transfer-complete --op=1 --resource="x" --peer=(UdpPeer (net.SocketAddress (net.IpAddress.parse "127.0.0.1") 1)) --bytes=0 --ok=true
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
cd ~/workspaceToit/tftp/tests && toit run transfer_complete_test.toit
```
Expected: FAIL — `tftp` does not export `Request`, and `Storage` methods don't accept `--req` / lack `on-transfer-complete`.

- [ ] **Step 3: Add `Request` + evolve `Storage` (`src/storage.toit`)**

At the top of `src/storage.toit`, add the channel import (alongside `import host.directory` / `import host.file` / `import io`):

```
import .channel
```

Add the `Request` class just above `abstract class Storage`:

```
/** Per-request context handed to the $Storage backend. */
class Request:
  /** The transport peer that issued the request. */
  peer/Peer
  /**
  The full, un-stripped resource name from the RRQ/WRQ, including any
    "?id=<mac>&name=...&crc=..." query suffix the client appended.
  */
  raw-path/string

  constructor --.peer --.raw-path:
```

Change the four abstract method signatures to add the optional `--req` (keep their Toitdocs):

```
  abstract exists name/string --req/Request?=null -> bool
  abstract size name/string --req/Request?=null -> int?
  abstract reader-for name/string --req/Request?=null -> io.CloseableReader
  abstract writer-for name/string --req/Request?=null --tsize-hint/int?=null -> io.CloseableWriter
```

Add the defaulted completion hook (place it after `writer-for`, before `reads-allowed`):

```
  /**
  Notifies the backend that a transfer for $resource finished.

  Reports the opcode $op ($RRQ or $WRQ), the issuing $peer, the number of
    $bytes moved, and whether the transfer completed cleanly ($ok true) or was
    aborted ($ok false). The default does nothing; store-backed handlers
    override it (e.g. to mark a command delivered). Fires exactly once.
  */
  on-transfer-complete --op/int --resource/string --peer/Peer --bytes/int --ok/bool -> none:
```

- [ ] **Step 4: Adapt `FilesystemStorage` signatures (`src/storage.toit`)**

Add `--req/Request?=null` to each overriding method; the bodies ignore it. Replace the four method headers:

```
  exists name/string --req/Request?=null -> bool:
```
```
  size name/string --req/Request?=null -> int?:
```
```
  reader-for name/string --req/Request?=null -> io.CloseableReader:
```
```
  writer-for name/string --req/Request?=null --tsize-hint/int?=null -> io.CloseableWriter:
```
(Leave each method body exactly as-is.)

- [ ] **Step 5: Export `Request` — already covered**

`src/tftp.toit` does `import .storage` + `export *`, so `Request` is exported automatically once it's a public class in `storage.toit`. No edit needed.

- [ ] **Step 6: Run the new test + the Task 1 gate**

```bash
cd ~/workspaceToit/tftp/tests && toit run transfer_complete_test.toit && toit run inproc_roundtrip_test.toit
```
Expected: `transfer_complete_test (storage-level) OK` then `INPROC ROUNDTRIP OK ...`.

- [ ] **Step 7: Commit**

```bash
cd ~/workspaceToit/tftp && git add src/storage.toit tests/transfer_complete_test.toit && git commit -m "feat: Storage Request context + on-transfer-complete hook"
```

---

## Task 5: Wire `ServerExchange` — pass `Request`, track bytes, fire `on-transfer-complete`

The server now hands the `Request` to `Storage` on every resolution and fires the `ok`-tagged completion event exactly once per transfer.

**Files:**
- Modify: `src/tftp_server.toit`
- Create: `tests/fake_channel.toit`
- Modify: `tests/transfer_complete_test.toit` (add success + abort coverage)

- [ ] **Step 1: Add the `FakeChannel` test helper**

Create `~/workspaceToit/tftp/tests/fake_channel.toit`:

```
// Copyright 2026 Ekorau LLC.
import tftp show Datagram PacketChannel Peer

/**
A scripted $PacketChannel for deterministic engine tests.

$inbound is a queue of datagrams handed to the engine in order; when empty,
  $receive returns null (timeout) immediately, so retry/abort paths run fast
  with no real waiting. Every $send is appended to $sent.
*/
class FakeChannel implements PacketChannel:
  inbound/List   // of Datagram
  sent/List := []  // of ByteArray
  closed/bool := false

  constructor --.inbound=[]:

  send bytes/ByteArray --to/Peer -> none:
    sent.add bytes

  receive --deadline-us/int -> Datagram?:
    if inbound.is-empty: return null
    return inbound.remove-last  // inbound is built reversed by the caller.

  close -> none:
    closed = true
```

- [ ] **Step 2: Write the failing tests (success + abort)**

Append to `~/workspaceToit/tftp/tests/transfer_complete_test.toit` — add three calls in `main` and the test bodies. Change `main` to:

```
main:
  test-request-fields
  test-default-on-complete-is-noop
  test-rrq-success
  test-wrq-success
  test-abort-ok-false
  print "transfer_complete_test OK"
```

Add these imports at the top of the file (alongside the existing `expect`/`io`/`net` imports):

```
import host.directory
import host.file
import log
import monitor
import tftp show FilesystemStorage PacketRRQ Peer RRQ ServerExchange TFTPServer TFTPClient WRQ
import .fake_channel
```

Add the test bodies. `WatchedStorage` extends the real `FilesystemStorage`, so its readers/writers are genuine and the success/abort transfers actually run:

```
PORT ::= 6970
ROOT ::= "/tmp/tftp-xfer-complete"

// A real FilesystemStorage that records the Request seen on reader-for and the
// completion event. The Latch lets the main task wait for the server-side
// event, which fires slightly after the client call returns.
class WatchedStorage extends FilesystemStorage:
  op/int? := null
  resource/string? := null
  ok/bool? := null
  bytes/int? := null
  req-path/string? := null
  signal/monitor.Latch := monitor.Latch

  constructor --root/string: super --root=root --allow-overwrite

  reader-for name/string --req/Request?=null -> io.CloseableReader:
    if req: req-path = req.raw-path
    return super name --req=req

  on-transfer-complete --op/int --resource/string --peer/Peer --bytes/int --ok/bool -> none:
    this.op = op
    this.resource = resource
    this.ok = ok
    this.bytes = bytes
    signal.set true

test-rrq-success:
  if not file.is-directory ROOT: directory.mkdir --recursive ROOT
  file.write-contents "rrq-payload-bytes" --path="$ROOT/r.txt"
  storage := WatchedStorage --root=ROOT
  server := TFTPServer --storage=storage --port=PORT
  t := task:: server.start
  sleep --ms=200
  try:
    client := TFTPClient --host="127.0.0.1"
    client.port = PORT
    client.open
    try:
      client.read-bytes "r.txt"
    finally:
      client.close
    storage.signal.get  // Wait for the server-side completion event.
    expect-equals RRQ storage.op
    expect-equals "r.txt" storage.resource
    expect-equals "r.txt" storage.req-path
    expect storage.ok
    expect (storage.bytes > 0)
  finally:
    server.stop
    t.cancel

test-wrq-success:
  if not file.is-directory ROOT: directory.mkdir --recursive ROOT
  storage := WatchedStorage --root=ROOT
  server := TFTPServer --storage=storage --port=(PORT + 1)
  t := task:: server.start
  sleep --ms=200
  try:
    client := TFTPClient --host="127.0.0.1"
    client.port = PORT + 1
    client.open
    try:
      client.write-string "written-bytes" --filename="w.txt"
    finally:
      client.close
    storage.signal.get
    expect-equals WRQ storage.op
    expect-equals "w.txt" storage.resource
    expect storage.ok
  finally:
    server.stop
    t.cancel

test-abort-ok-false:
  // Drive a ServerExchange directly with a FakeChannel that delivers nothing
  // after the initial RRQ, so every retransmit times out instantly -> abort.
  // The event must still fire, with ok=false.
  if not file.is-directory ROOT: directory.mkdir --recursive ROOT
  file.write-contents "abort-payload" --path="$ROOT/x.txt"
  storage := WatchedStorage --root=ROOT
  initial := PacketRRQ "x.txt" "octet" --options={:}
  source := net.SocketAddress (net.IpAddress.parse "127.0.0.1") 5555
  chan := FakeChannel --inbound=[]  // every receive returns null immediately.
  exchange := ServerExchange initial source storage chan log.default
  exchange.run
  expect-equals false storage.ok
  expect-equals RRQ storage.op
```

> Note: `ServerExchange`, `PacketRRQ`, `Peer`, and the `RRQ`/`WRQ` opcode constants are public in the package and reachable through `export *`, so the `tftp show …` import resolves them. If any later change makes one private, expose it for tests rather than weakening production code.

- [ ] **Step 3: Run the tests to verify they fail**

```bash
cd ~/workspaceToit/tftp/tests && toit run transfer_complete_test.toit
```
Expected: FAIL — the server does not yet pass `--req` (so `req-path` stays null) nor call `on-transfer-complete` (so `storage.op`/`complete-ok` stay null and `signal.get` blocks or the asserts fail).

- [ ] **Step 4: Pass `Request` into storage calls (`src/tftp_server.toit`)**

Add a helper on `ServerExchange` to build the request, and a bytes counter field. Add the fields near the existing `ServerExchange` fields (`storage-writer_` etc.):

```
  bytes-moved_/int := 0
```

Add a private helper method on `ServerExchange`:

```
  request_ -> Request:
    return Request --peer=(UdpPeer source_) --raw-path=filename_
```

In `run-wrq_`, change `storage_.writer-for filename_ --tsize-hint=tsize-hint` to:

```
    storage-writer_ = storage_.writer-for filename_ --req=request_ --tsize-hint=tsize-hint
```

In `run-rrq_`, change `storage_.reader-for filename_` to:

```
    storage-reader_ = storage_.reader-for filename_ --req=request_
```

In `build-oack_`, the RRQ branch calls `storage_.size filename_`. Change it to:

```
          size := storage_.size filename_ --req=request_
```

- [ ] **Step 5: Track bytes moved (`src/tftp_server.toit`)**

In `next-data-frame_` (server send path), after computing `chunk`, add the byte count. Replace the method:

```
  next-data-frame_ -> ByteArray:
    chunk := bytes-to-send_ blksize_
    if chunk.size < blksize_: drained_ = true
    bytes-moved_ += chunk.size
    cached_ = (PacketDATA block-num_ chunk).serialize
    return cached_
```

In `handle-data_` (server receive path), where it does `storage-writer_.write data.data` for an in-order block, add the count immediately after that write:

```
      storage-writer_.write data.data
      bytes-moved_ += data.data.size
```

- [ ] **Step 6: Fire `on-transfer-complete` exactly once in `run` (`src/tftp_server.toit`)**

Replace the entire `run` method. The new version emits the event once, with `ok=true` only when the transfer completed without throwing, and `ok=false` otherwise. It avoids early `return` (which would skip the event):

```
  /** Drives the request to completion. Maps storage exceptions to TFTP errors. */
  run -> none:
    ok := false
    op := initial_ is PacketWRQ ? WRQ : RRQ
    err := catch --trace=false:
      if validate-request_:
        if initial_ is PacketWRQ:
          run-wrq_ (initial_ as PacketWRQ)
        else:
          run-rrq_ (initial_ as PacketRRQ)
        ok = true
    storage_.on-transfer-complete
        --op=op
        --resource=filename_
        --peer=(UdpPeer source_)
        --bytes=bytes-moved_
        --ok=ok
    if err != null:
      if peer-gone_:
        logger_.warn "transfer abandoned: peer gone"
            --tags={"peer": source_, "block": block-num_}
      else:
        handle-storage-error_ err
```

- [ ] **Step 7: Run the tests to verify they pass**

```bash
cd ~/workspaceToit/tftp/tests && toit run transfer_complete_test.toit
```
Expected: prints `transfer_complete_test OK`, exit 0.

- [ ] **Step 8: Run the full local gate**

```bash
cd ~/workspaceToit/tftp/tests && toit run inproc_roundtrip_test.toit && toit run channel_test.toit && toit run transfer_complete_test.toit
```
Expected: `INPROC ROUNDTRIP OK ...`, `channel_test OK`, `transfer_complete_test OK` — all green.

- [ ] **Step 9: Commit**

```bash
cd ~/workspaceToit/tftp && git add src/tftp_server.toit tests/fake_channel.toit tests/transfer_complete_test.toit && git commit -m "feat: server passes Request to Storage and fires on-transfer-complete"
```

---

## Task 6: Back-compat verification + Porta consumer check

Prove the refactor preserved the wire protocol against Porta's host server and (on hardware) the already-flashed device.

**Files:**
- Verify (no change expected): `porta/host/serve.toit`, `porta/device/transport.toit`

- [ ] **Step 1: Confirm Porta's host server still compiles against the refactored package**

`porta/host/serve.toit` constructs `FilesystemStorage --root=ROOT --read-only` and `TFTPServer --storage=storage --port=PORT --logger=...`. Both constructors are unchanged by this refactor. Compile-check it:

```bash
cd ~/workspaceToit/porta/host && toit run serve.toit < /dev/null || echo "(serve blocks listening — Ctrl-C/timeout is fine; we only need a clean compile + 'serving ... on UDP/6969')"
```
Expected: it compiles and prints its `serving ...` line (then blocks listening — that's correct; stop it). A *compile* error here is a regression to fix.

- [ ] **Step 2: Host-side wire back-compat (automated proxy)**

The Task 1 in-process roundtrip already exercises the refactored `TFTPClient` ↔ refactored `TFTPServer` over real UDP, including multi-block transfers and option negotiation defaults. Re-run it as the host-side back-compat proof:

```bash
cd ~/workspaceToit/tftp/tests && toit run inproc_roundtrip_test.toit
```
Expected: `INPROC ROUNDTRIP OK ...`.

- [ ] **Step 3: (Hardware, manual) device-client smoke against the refactored host server**

This proves the **already-flashed** `fwkb` (running the *old* package, unchanged on the device) still interoperates with the *new* refactored `host/serve.toit` — i.e. the wire protocol is byte-compatible, no reflash. Run only when `fwkb` is available:

1. On the host, prepare and start the refactored server:
   ```bash
   cd ~/workspaceToit/porta/host && toit run serve.toit
   ```
   (It serves `goal` + `payload` on UDP/6969 from `/tmp/porta-tftp`, per its `main`.)
2. Power `fwkb`; let it wake and poll the gateway.
3. Confirm on the host log that the device issues RRQ for `goal` then streams `payload`, and the transfer completes — identical to pre-refactor behavior.

Expected: the device fetches `goal` and `payload` successfully; no wire-level errors. If the device cannot read what it read before, the refactor changed the wire protocol — stop and fix.

- [ ] **Step 4: Final commit (if any verification fixes were needed)**

If Steps 1–3 required no code changes, there is nothing to commit. If a fix was needed:

```bash
cd ~/workspaceToit/tftp && git add -A && git commit -m "fix: <describe back-compat fix>"
```

---

## Self-review notes (for the executor)

- **Spec coverage:** channel/Peer seam (Task 2) ✓; engine retype, logic-preserving (Task 3) ✓; `Request` + `on-transfer-complete` with explicit `ok` (Tasks 4–5) ✓; `FilesystemStorage` adapted (Task 4) ✓; back-compat gate — host tests proxy + device smoke (Tasks 1, 6) ✓; `Peer` opaque token applied now (Task 2) ✓; parallel-sequencing and ESPnow generalization are spec-level (no task — ESPnow is explicitly out of scope).
- **Not in scope (per spec non-goals):** pure stepped engine, a second transport (Thread/ESPnow/serial), IPv6 listener, any `TFTPClient` public-API change.
- **If a `toit` toolchain quirk blocks a test** (e.g. parameter-type narrowing in an override, or an export not visible), prefer the least-invasive fix that keeps the production interface as specified, and note it — do not weaken the production API to satisfy a test.
```
