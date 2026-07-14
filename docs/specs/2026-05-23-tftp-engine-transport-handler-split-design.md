# Spec A: TFTP engine / transport / handler split

**Date:** 2026-05-23
**Status:** Draft (brainstorming complete; pending user review → implementation plan)
**Package:** `~/workspaceToit/tftp`
**Dependency of:** the Porta Toit gateway
(`docs/specs/2026-05-23-porta-toit-gateway-design.md`, "Spec B"). Spec B's M1
command-queue control plane cannot mark commands *delivered*, parse `?id=<mac>`
node identity, or ingest reports until the `tftp` package grows three seams. This
spec is **self-contained and agent-facing**: it can be implemented by an agent
without Spec B's conversation context.

## Why this exists

The gateway needs three things the current `tftp` package does not provide:

1. A **transfer-complete event** — Spec B marks a command *delivered* the moment
   its TFTP read transfer completes (TFTP ACKs prove delivery, not execution).
2. **Handler access to the request's peer and raw query string** — the node sends
   its identity as a filename query suffix (`commands?id=<mac>`,
   `payload?id=<mac>&name=<n>&crc=<c>`); the handler must see it.
3. A **transport-neutral block engine** — Porta runs on WiFi/UDP now, with Thread
   and ESPnow on the roadmap. The RFC-1350 state machine must not be welded to the
   UDP socket.

## Prior art: this is the proven st-zephyr seam

The boundary this spec introduces is **not novel** — it is exactly where the
shipped st-zephyr jast Thread integration drew its transport line. From
`st-zephyr/docs/design/2026-03-29-thread-integration-design.md`
("Transport Abstraction"):

> - **Serial:** `send_packet()` → SLIP-encode, write via uart. `recv_packet()` →
>   read via uart, SLIP-decode.
> - **UDP:** `send_packet()` → `sendto()`. `recv_packet()` → `recvfrom()`.
> - *"Everything above the transport layer — TFTP client, command handling, …
>   is transport-agnostic."*

That is the `PacketChannel` of this spec: `send bytes --to peer` / `receive →
datagram`, placed **below TFTP and above the wire**, with a **blocking-pull**
receive (`recvfrom` semantics — Zephyr presents even OpenThread as a blocking
recvfrom, so no push/callback bridge is required). The same design also already
uses **"id in the request path"** for device identity and a **commands/results**
request pattern — both mirrored by Spec B. We are bringing a proven seam into the
Toit `tftp` package, not inventing one.

## Current architecture (what exists today)

```
packets.toit       wire codec: RRQ/WRQ/DATA/ACK/OACK/ERROR + (de)serialize. Pure, no socket.
exchange.toit      abstract Exchange: the per-transfer STATE MACHINE.
                     drive_ loop (send next-frame; handle receive_), retry,
                     peer-TID lock, RFC 2347/2348/2349 OACK negotiation.
                     *** owns the udp.Socket directly (socket_.send / socket_.receive). ***
tftp_client.toit   TFTPClient (public API) + ClientExchange extends Exchange.
tftp_server.toit   TFTPServer (listen → per-transfer task on ephemeral socket) +
                     ServerExchange extends Exchange + Capacity_ monitor.
storage.toit       abstract Storage (exists/size/reader-for/writer-for) + sentinel
                     errors + FilesystemStorage. reader-for serves RRQ; writer-for
                     returns a CloseableWriter that commits on close (WRQ).
tftp.toit          export *
```

Two facts make this a **small, low-risk** refactor rather than a rewrite:

- The **block engine already exists** as `Exchange`. The work is *inverting the
  socket out of it*, not writing a new state machine.
- **`Storage` is nearly the handler already** — it returns a reader for RRQ and a
  commit-on-close sink for WRQ. It lacks only the peer, the raw query, and a
  completion signal.

The only `Storage` implementor in the workspace is `FilesystemStorage`
(in-package); the only external consumer is Porta's `host/serve.toit`, which
*constructs* `FilesystemStorage` but never *implements* `Storage`. Blast radius is
contained to this package plus `host/serve.toit`.

## Goals

1. A **transport-neutral block engine**: `Exchange` references an injected
   `PacketChannel`, never a `udp.Socket`, and never `net.SocketAddress`.
2. A **transfer-complete event** delivered to the handler, distinguishing success
   from abort.
3. A **handler that sees the peer and the raw request path** (with query).
4. The hardware-verified `TFTPClient` API and on-the-wire behavior **unchanged**.

## Non-goals

- A **pure stepped engine** (`step(packet) → Action` control inversion). Rejected:
  it rewrites the proven `drive_` loop. We keep the blocking-pull loop and inject
  the channel under it.
- Building a **second transport** (Thread/ESPnow/serial). This spec only ships the
  UDP channel; it *proves* the seam admits the others (see Appendix).
- IPv6 listener support, changing the wire protocol, or changing the `TFTPClient`
  public API.

## Key decisions (locked during brainstorming)

1. **Engine/transport seam = inject a `PacketChannel`** (not a pure stepped
   engine). The `Exchange.drive_`/retry/TID/OACK logic is preserved byte-for-byte;
   only `socket_.send`/`socket_.receive` move behind the interface.
2. **Peer is an opaque `Peer` token, applied now** — not `net.SocketAddress`. The
   engine needs only equality (TID enforcement) and "hand it back to the channel."
   Doing this now means the engine is *genuinely* transport-neutral and is touched
   **once**, rather than re-edited when ESPnow (which addresses by MAC, no IP/port)
   arrives. Transport-specific address logic stays in the transport's `Exchange`
   subclass via the existing overridable hook.
3. **Handler = evolve `Storage` in place** (not a parallel `RequestHandler`
   hierarchy). Add an optional `Request` context and a defaulted
   `on-transfer-complete` hook; `FilesystemStorage` adapts mechanically with no
   behavior change.
4. **Transfer-complete fires with explicit `ok`** — `ok=true` on success-exit,
   `ok=false` on abort/error/peer-gone — so the handler can audit failed
   deliveries, not just successes.
5. **Sequencing relative to Spec B = parallel** (see "Sequencing").

## Architecture: three seams

```
packets.toit         wire codec (UNCHANGED) — pure
        ▲
exchange.toit        BLOCK ENGINE: Exchange (+ Client/ServerExchange).
        │ uses          drive_/retry/TID/OACK loop UNCHANGED;
        │                addresses retyped SocketAddress → Peer;
        │                socket calls → channel calls; fires on-transfer-complete.
   PacketChannel  ◄── channel.toit (NEW): interface PacketChannel + Peer
        ▲                concrete: UdpChannel (owns the udp.Socket), UdpPeer
        │
storage.toit         HANDLER: Storage evolved with Request context +
                       on-transfer-complete. FilesystemStorage adapted.
                       (Gateway's StoreBackedHandler lives in Spec B's gateway/.)
```

Each seam is independently testable: the **engine** against a `FakeChannel`; the
**channel** as plain UDP I/O; the **handler** against synthetic `Request`s.

## Seam 1 — `channel.toit` (new): `Peer` + `PacketChannel` + `UdpChannel`

```
/** An opaque transport peer. The engine needs only identity + send-back. */
interface Peer:
  operator == other/Peer -> bool
  hash-code -> int

/** A received datagram tagged with its source peer. */
class Datagram:
  bytes/ByteArray
  from/Peer
  constructor .bytes .from:

/**
A bidirectional, datagram-oriented channel under the TFTP engine.

Blocking-pull receive (recvfrom semantics). Concrete implementations own the
  wire (UDP socket, Thread/OpenThread socket, ESPnow service, serial/SLIP).
*/
interface PacketChannel:
  /** Sends $bytes to $to. */
  send bytes/ByteArray --to/Peer -> none
  /**
  Receives the next datagram, or null on timeout.

  Blocks until a datagram arrives or $deadline-us (monotonic microseconds) is
    reached.
  */
  receive --deadline-us/int -> Datagram?
  /** Closes the underlying wire. */
  close -> none

/** A $Peer wrapping a UDP endpoint. */
class UdpPeer implements Peer:
  socket-address/net.SocketAddress
  constructor .socket-address:
  ip -> net.IpAddress: return socket-address.ip
  operator == other/Peer -> bool:
    return other is UdpPeer and (other as UdpPeer).socket-address == socket-address
  hash-code -> int: return socket-address.hash-code

/** A $PacketChannel over a UDP socket. */
class UdpChannel implements PacketChannel:
  socket_/udp.Socket
  constructor .socket_:
  send bytes/ByteArray --to/Peer -> none:
    socket_.send (udp.Datagram bytes (to as UdpPeer).socket-address)
  receive --deadline-us/int -> Datagram?:
    // Wraps socket_.receive in with-timeout; returns null on DEADLINE-EXCEEDED.
    ...
  close -> none: socket_.close
```

`UdpChannel` does **not** own `net.open`/socket lifecycle policy beyond what it is
handed — the client and the server construct it over the socket they already open,
preserving today's lifecycle exactly.

## Seam 2 — `exchange.toit`: engine, logic unchanged, addresses retyped

`Exchange` changes are mechanical and logic-preserving:

- Field `socket_/udp.Socket` → `channel_/PacketChannel`.
- Fields `peer-tid_/net.SocketAddress?` and `dest_/net.SocketAddress?` →
  `peer-tid_/Peer?`, `dest_/Peer?`.
- `send_ payload` → `channel_.send payload --to=dest_`.
- `receive_` → `channel_.receive --deadline-us=…`; the returned `Datagram.from` is
  compared with `peer-tid_` via `==`; the unknown-TID error-5 reply is sent with
  `channel_.send … --to=from`. The peer-TID lock, the timeout/`PacketTIMEOUT`
  path, and the `MAX-TRIES_` retry are **unchanged**.
- `is-acceptable-source_ source/net.SocketAddress -> bool` →
  `is-acceptable-source_ source/Peer -> bool`. The default still returns true.
  `ClientExchange` overrides it as today, now reading `(source as UdpPeer).ip` —
  i.e. "accept a reply from my server's IP on any (ephemeral) port." **The one
  piece of transport-specific address logic stays in the UDP subclass**; the
  engine references only `Peer`.

`ClientExchange`/`ServerExchange` construct/receive a `UdpChannel` and `UdpPeer`s
instead of touching the socket directly. The `drive_` loop body, OACK
negotiation, block accounting, and all RFC-1350 §4 behavior are untouched.

### Transfer-complete event

The engine fires the handler's completion hook at the points where a transfer
ends, with explicit `ok`:

- **RRQ success**: after the final DATA block is ACKed → `ok=true`.
- **WRQ success**: after the writer commits (`close`) and the final ACK is sent →
  `ok=true`.
- **Abort/error/peer-gone**: at any non-success exit → `ok=false`.

It carries `op` (RRQ/WRQ), `resource` (the request name), the `Peer`, and the
byte-count. On the **server** this is the gateway's delivery-marking signal.
(`ClientExchange` has no handler; the event is a server-side concern wired through
`ServerExchange`, which already holds the `Storage`.) The hook fires exactly once
per transfer.

Rationale for an explicit event (not reusing `close`): for **WRQ** the sink's
`close` already means success (commit-on-close), but for **RRQ** the engine closes
the reader on *every* exit — success and abort alike — so RRQ delivery cannot be
inferred from `close`. RRQ delivery is precisely the gateway's primary use, hence
the explicit, `ok`-tagged event.

## Seam 3 — `storage.toit`: evolve `Storage`

```
/** Per-request context handed to the $Storage backend. */
class Request:
  /** The transport peer that issued the request. */
  peer/Peer
  /** The full, un-stripped resource name from the RRQ/WRQ, including any
      "?id=<mac>&name=…&crc=…" query suffix. */
  raw-path/string
  constructor --.peer --.raw-path:

abstract class Storage:
  exists     name/string --req/Request?=null -> bool
  size       name/string --req/Request?=null -> int?
  reader-for name/string --req/Request?=null -> io.CloseableReader
  writer-for name/string --req/Request?=null --tsize-hint/int?=null -> io.CloseableWriter

  /**
  Notifies the backend that a transfer for $resource (opcode $op, $RRQ or $WRQ)
    by $peer finished, having moved $bytes bytes. $ok is true on a clean
    transfer-complete, false on abort/error/peer-gone. Default does nothing.
  */
  on-transfer-complete --op/int --resource/string --peer/Peer --bytes/int --ok/bool -> none:

  reads-allowed -> bool: return true
  writes-allowed -> bool: return true
```

- `--req` is **optional/defaulted** and `on-transfer-complete` is a **defaulted
  no-op**, so `FilesystemStorage` only needs the mechanical signature additions
  (it ignores `req`) and gains nothing else — **no behavior change**.
- The server already passes the **raw, un-stripped filename** to `Storage`; it now
  also constructs and passes the `Request` (peer from `Datagram.from`, raw-path
  from the request packet's filename). Today's device sends plain names
  (`goal`, `payload`) with no query, so `FilesystemStorage.resolve_` is unaffected.
- The gateway's `StoreBackedHandler` (Spec B, in `gateway/`) implements `Storage`,
  parses `?id=<mac>&…` from `req.raw-path`, dispatches `commands`/`payload`/
  `report`, and uses `on-transfer-complete` (RRQ `commands`, `ok=true`) to mark a
  command delivered.

## File-level change map

| File | Change |
|---|---|
| `src/channel.toit` | **NEW**: `Peer`, `Datagram`, `PacketChannel`, `UdpPeer`, `UdpChannel`. |
| `src/exchange.toit` | Retype socket→`channel_`, `SocketAddress`→`Peer`; fire `on-transfer-complete`. Logic unchanged. |
| `src/tftp_client.toit` | `ClientExchange` builds a `UdpChannel`/`UdpPeer`; `is-acceptable-source_` reads `UdpPeer.ip`. Public `TFTPClient` API unchanged. |
| `src/tftp_server.toit` | Per-transfer task builds a `UdpChannel`; `ServerExchange` builds `Request`, passes it to `Storage`, and routes the completion event. |
| `src/storage.toit` | Add `Request`; add `--req` params + `on-transfer-complete`; adapt `FilesystemStorage`. |
| `src/tftp.toit` | Export `Peer`, `Datagram`, `PacketChannel`, `UdpChannel`, `UdpPeer`, `Request`. |

## Back-compat gate (the hard constraint)

The on-device `TFTPClient` is hardware-verified; its API and wire behavior must
not change. Spec A is **done only when**, with **no device reflash**:

1. **Existing host tests pass unchanged**: `roundtrip_test`, `large_transfer_test`,
   `options_test`, `blksize_perf_test` (and the shell-driven interop tests where a
   tftp toolchain is available).
2. **Device-client smoke**: the hardware-verified `TFTPClient` (Porta
   `device/transport.toit`) reads `goal` and streams `payload` from a refactored
   `host/serve.toit` — byte-identical wire exchange. `host/serve.toit` may need
   only its construction updated if at all (it constructs `FilesystemStorage` +
   `TFTPServer`, both API-stable here).
3. **New tests**:
   - `FakeChannel implements PacketChannel` driving an `Exchange` deterministically
     (scripted datagrams + timeouts), asserting block/retry/TID behavior.
   - `on-transfer-complete` coverage: RRQ success (`ok=true`), WRQ success
     (`ok=true`), and a forced abort (`ok=false`), each fired exactly once.
   - A `Request`-parsing handler test: a synthetic RRQ `x?id=abc` reaches the
     handler with `req.raw-path == "x?id=abc"` and the expected `peer`.

`TFTPClient`'s public surface is **frozen**: `open`, `close`, `read`,
`read-bytes`, `write-string`, `write-bytes`, `write-stream`, `last-tsize`, and the
`port` field.

## Sequencing (relative to Spec B)

**Parallel.** Only Spec B's gateway *handler* depends on these seams; the gateway's
store, command codec, and CLI do not. So: **pin this spec's signatures now** (the
`Peer`/`PacketChannel`/`Request`/`on-transfer-complete` contract), build the
gateway's TFTP-free store/command/CLI via TDD against it, and land this refactor
concurrently; integrate `StoreBackedHandler` last. A-first is the fallback only if
the contract proves unstable in practice.

## Appendix — transport compatibility check

The seam was validated against the two roadmap radios.

**Thread — drop-in, no interface change.** OpenThread under Zephyr presents a
blocking `recvfrom`, matching `PacketChannel.receive`. Thread MTU (~90–100 B per
802.15.4 frame) is absorbed by the engine's existing RFC-2348 blksize negotiation,
*below which the channel stays byte-blind*. Thread peers are IPv6 — `UdpPeer`
already wraps a `net.SocketAddress` of either family, and TID equality is unchanged.
A `ThreadChannel` is the only new code. (Their serial/SLIP transport is likewise
just another `PacketChannel`, so the seam covers wired links too.)

**ESPnow — fits; it is what made `Peer` opaque now.** ESPnow `receive` is also a
blocking pull, and its 250-byte payload cap is handled by blksize negotiation. Its
*one* difference is addressing: peers are a 6-byte **MAC, with no IP or port**.
That is why `Peer` is opaque rather than `net.SocketAddress` — an `EspnowPeer`
(MAC `==`/`hash-code`) and `EspnowChannel` slot in with **no engine edit**:

```
class EspnowPeer implements Peer:
  mac/ByteArray                                  // 6 bytes
  operator == other/Peer -> bool:
    return other is EspnowPeer and (other as EspnowPeer).mac == mac
  hash-code -> int: ...

class EspnowChannel implements PacketChannel:
  service_/espnow.Service
  send bytes/ByteArray --to/Peer -> none:
    service_.send bytes --address=(to as EspnowPeer).mac
  receive --deadline-us/int -> Datagram?:        // blocking pull
    ...                                          // returns Datagram --from=(EspnowPeer mac)
```

A future ESPnow client would override `is-acceptable-source_` against `EspnowPeer`
instead of `UdpPeer`. No other engine change is required — the generalization
needed for ESPnow is fully contained by `Peer` + the existing acceptance hook.

## Milestones

This spec is a single deliverable (call it **A1**): the channel/peer split, the
handler evolution, the transfer-complete event, `FilesystemStorage` adaptation,
and the back-compat test suite. There are no sub-milestones — it lands as one
reviewed change gated by the back-compat suite, then unblocks Spec B M1's handler.
