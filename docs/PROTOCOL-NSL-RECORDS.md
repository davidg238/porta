# Porta ↔ nsl — the record schemas

**Status: draft for review (2026-07-12).** Companion to `PROTOCOL-NSL.md`, which
defines the *model* (retained/event channels, cursor sync, the grant boundary). This
document pins the *values* — one schema per path prefix — and the size law that keeps
bulk data off the channels entirely.

The addendum in `tuvm/review/program-sequence.md` (2026-07-11) requires these schemas
be pinned **before first implementation**. That is what this is.

Nothing here presumes *where* retained channels are implemented (C space vs an nsl
library). Records are the same either way; that decision is deliberately left open.

---

## 1. The encoding contract

Every value on the wire is a **CBOR-encoded value of the nsl message algebra** — the
codec that shipped at rung 3 (`Cbor` in `nsl-tuvm/lib/kernel.ns`, RFC 8949). No second
codec, no bespoke framing, no JSON.

Inherited from that codec, and therefore **already true and already tested** — this
document adds no new encoding rules, it only *uses* them:

- **Deterministic** (RFC 8949 §4.2): shortest-form ints and floats, definite lengths
  only, map keys sorted bytewise over their *encoded* bytes. Two nodes encoding the
  same value produce byte-identical output — so a retained value can be compared, and
  a golden fixture can be pinned, by bytes.
- **Closed**: unknown tags and types are a decode *error*, not a pass-through. A
  hostile or mis-versioned peer cannot smuggle a value the node will faithfully relay.
- **Depth-bounded at decode** (32). A hostile peer cannot blow the node's stack.
- **Total**: every byte sequence yields exactly one value or exactly one of
  `not a value` / `message depth exceeded` / `invalid utf-8` / `integer out of range`.
- **Records** are CBOR tag 27 — `[record-name, {field-map}]`. **Symbols** are tag 39.
- **Integers** are the ratified L7 band, ±(2^64−1).

### Types used below

| notation | algebra type | CBOR |
|---|---|---|
| `int` | integer | major 0/1 |
| `str` | string (UTF-8) | major 3 |
| `bytes` | bytes | major 2 |
| `sym` | symbol | tag 39 |
| `bool` | boolean | simple 0xf4/0xf5 |
| `[T]` | list of T | major 4 |
| `Name{…}` | record | tag 27 |

**Enums are symbols, never strings.** `#deepSleep`, not `"deepSleep"`. A symbol is a
distinct algebra rank, so a typo'd enum fails to decode as the wrong *kind* of thing
rather than silently comparing unequal to every branch.

---

## 2. The size law — why bulk never rides a channel

This is the rule the rest of the catalogue is built to respect, so it comes first.

> **A channel carries a *reference* to bulk data. It never carries the bulk data.**

`PROTOCOL-NSL.md` §3.3 states the intent ("big bytes never ride channels"). This section
makes it enforceable: a number, a mechanism, and a record.

### 2.1 Why — three independent ceilings, and the radio is the binding one

**The radio.** An IEEE 802.15.4 frame carries ~102 bytes of payload after MAC headers.
6LoWPAN compresses IPv6 and then *fragments* anything larger, up to the 1280-byte IPv6
MTU. Fragmentation is not free and it is not graceful: **a datagram survives only if
every one of its fragments survives.** With per-datagram loss `p`, an `n`-fragment
datagram arrives with probability `(1−p)^n`.

That exponent is not theoretical. The bench soak fleet (2026-07-12, three nRF52840
nodes on a real OpenThread mesh, 32-byte payloads) measures **0–5% datagram loss**
depending on the node — the weakest, at Link Quality In = 2, sits around 4.6%. Take
that node at p = 0.05:

| payload | ≈ fragments | delivery |
|---|---|---|
| 32 B | 1 | 95% |
| 512 B | ~6 | 74% |
| 1024 B | ~12 | 54% |
| 4 KB | — | *cannot* be sent at all (> IPv6 MTU) |

A 1 KB check-in batch to a marginal node fails **half the time**, and every failure
costs a full retransmit of the whole batch. Small messages are not a stylistic
preference here; they are the difference between a fleet that converges and one that
thrashes.

**The heap.** The node's arena is 48 KB total (config-D, nRF52840). A decoded value is
a live object graph in that arena. A 200 KB firmware image is not "a large value" — it
is *four times the entire heap*. There is no cap that makes it work; it must never be
a value at all.

**Overflow policy (G2).** Channels have bounded depth and a defined overflow vocabulary
(coalesce/drop + a `sys/overrun` counter — tuvm#20). A single huge value defeats
per-entry accounting: you cannot coalesce half a firmware image.

### 2.2 The caps

| limit | value | rationale |
|---|---|---|
| `MAX_VALUE_BYTES` | **256** | one encoded value (a single retained value, or one event). Fits comfortably inside one 6LoWPAN fragment with room for the batch envelope. |
| `MAX_BATCH_BYTES` | **512** | one check-in datagram (many entries). ~6 fragments worst case; ~74% delivery on the *worst* bench node, ~97% on a healthy one. |

Both are enforced **at the bridge job**, on both legs:

- **Outbound**: a value that encodes larger than `MAX_VALUE_BYTES` is a **programming
  error** and crashes (L5 rule 2: let-it-crash — the supervisor is the handler). It is
  not truncated, and it is not silently dropped. If a job wants to publish something
  big, it publishes a `BlobRef` instead; that is a design decision, not a runtime
  accident. A batch that would exceed `MAX_BATCH_BYTES` is **split across check-ins**,
  never truncated — the cursor makes this free.
- **Inbound**: an over-size value from porta is a **decode error**, handled like any
  other malformed input (`ifInvalid:` → `sys/` event, cursor not advanced). One door,
  same rules (`PROTOCOL-NSL.md` §1, "the one law").

These numbers are a **first cut against measured mesh loss** and should be re-measured
on the G10 fleet-sim and the real mesh before they are frozen. The *structure* — a
per-value cap, a per-batch cap, and a reference record above them — is the part that
should not change.

### 2.3 The one indirection: `BlobRef`

```
BlobRef {
  hash:  bytes,   (* content hash; the identity AND the integrity check *)
  size:  int,     (* bytes, so the node can refuse before it starts *)
  alg:   sym,     (* #sha256 — pinned, so the hash is self-describing *)
}
```

Content-addressed, so it is **idempotent and cache-correct by construction**: a node
that already holds `hash` does nothing. A reflashed node, a node that resumes from
cursor 0, and a node seeing the value for the first time all take the same path. There
is no "have I already applied this?" state to get wrong — the hash *is* the answer.

The bulk bytes are then fetched **out of band**, over the transport the node class
already has (TFTP today; CoAP blockwise the natural Thread evolution). The space never
sees them. Verifying the fetched bytes against `hash` is what makes the data plane
trustworthy without putting auth on the control plane.

### 2.4 What is a blob, and what is not

| data | why |
|---|---|
| **firmware / `.oimg` images** | blob. Kilobytes to hundreds of kilobytes. |
| **profiler dumps** | blob. Opaque and unbounded. |
| **panic cores** | **blob** — see below. |
| sensor readings, health counters, config scalars, log lines | values. Bounded and small. |

**`sys/panic` is the one place `PROTOCOL-NSL.md` contradicts itself,** and this is the
proposed resolution. §3.2 gives `sys/panic` the shape `{blob: bytes}` and marks it
*must-deliver*, while §3.3 says bulk never rides a channel. Both cannot hold.

Resolution: **`sys/panic` carries a bounded summary inline, and a `BlobRef` for the
rest.** The summary is the part you need at 3am and the part that must survive a node
that is about to die; the core dump is the part you want *if* the node comes back.

```
Panic {
  reason:  sym,            (* #oom | #hardFault | #watchdog | #assert | #stackOverflow *)
  bootId:  int,
  monoMs:  int,            (* uptime at death *)
  detail:  str,            (* bounded; TRUNCATED to fit MAX_VALUE_BYTES, and truncation
                              is fine here — this is the ONLY place truncation is legal,
                              because a partial reason beats no reason *)
  core:    BlobRef | nil,  (* nil if there is no retained core, or no room to keep one *)
}
```

Note what this buys: a node that dies of OOM can still *report* that it died of OOM,
because the report is 60 bytes and does not require allocating a buffer. Compare the
silent boot-OOM that shipped a dead dongle through rungs 7–8 — the failure there was
precisely that the node had no bounded way to say what happened.

---

## 3. Time (G6): monotonic only

**Nodes have no clock, and never pretend to.** Every timestamp on the wire is
monotonic-only; porta maps it to wall-clock at ingest using porta's own clock.

- `monoMs` — milliseconds since boot (`monotonicMillis`, real on target since rung 8).
- `bootId` — a counter incremented once per boot and persisted (NVS, G7).

The pair `(bootId, monoMs)` totally orders every observation a node ever makes, across
reboots, **without a clock**. A node that reboots does not emit timestamps that appear
to travel backwards, which is what a naive monotonic-only scheme does.

`bootId` rides the **batch envelope, not every record** — it is constant for every entry
in a check-in, and paying 3–9 bytes per entry for it would be real money against a 512-byte
batch. Individual records carry only `monoMs` where they need a stamp.

---

## 4. The record catalogue

Kind is `R` (retained — path holds a latest value) or `E` (event — append-only queue).
Direction is from the node's point of view.

### 4.1 `goal/**` — what porta wants (retained; porta writes, node reads)

| path | kind | record |
|---|---|---|
| `goal/apps/<name>` | R | `App { image: BlobRef, triggers: [sym], runlevel: int, lifecycle: sym, arguments: {…} }` |
| *(tombstone of above)* | R | delete = uninstall. The absence *is* the instruction. |
| `goal/mode` | R | `Mode { mode: sym, minAwakeS: int, maxAwakeS: int, maxAsleepS: int, loopSleepS: int }` |
| `goal/name` | R | `str` |
| `goal/forward` | R | `Forward { … }` — the whole policy, never a patch |
| `goal/config/<app>/<key>` | R | scalar (`int`/`str`/`bool`/`sym`) |
| `goal/debug/<app>` | R | `Debug { action: sym }` — `#attach` / `#detach` |
| `goal/profile/<app>` | R | `Profile { action: sym, durationS: int, continuous: bool }` |
| `goal/do/reboot` | **E** | `Reboot {}` — exactly-once via cursor |

`goal/apps/<name>` is where the size law earns its keep: the image is a `BlobRef`, so
this record is ~80 bytes regardless of whether the app is 4 KB or 400 KB.

**`set-mode`'s atomicity rule dissolves.** v1 needed a paragraph demanding "accept the
whole thing or reject it"; here `Mode` is *one record at one path*, so it is atomic by
construction. There is nothing to enforce.

### 4.2 `obs/**` — what the node observes (retained; node writes, porta reads)

| path | kind | record |
|---|---|---|
| `obs/apps/<name>` | R | `AppObs { image: BlobRef, runlevel: int, lifecycle: sym, triggers: [sym] }` |
| `obs/config/<app>/<key>` | R | scalar echo |
| `obs/mode` / `obs/name` / `obs/forward` | R | same shapes as their `goal/` twins |
| `obs/health` | R | `Health { uptimeMs: int, bootId: int, wakes: int, pollTimeouts: int, heapFree: int, heapLargest: int, overruns: int }` |

**`obs/X` deliberately reuses the shape of `goal/X`.** Convergence is then a *structural*
diff — `goal/mode` vs `obs/mode` — rather than a bespoke comparison per feature. v1's
self-heal loop and the UI's desired-vs-observed view become the same query, which is the
whole argument of `PROTOCOL-NSL.md` §1.

`heapFree` / `heapLargest` come from `heapStats` (prim 104, rung 4). `heapLargest` is
the fragmentation signal — the number that tells you a years-long node is dying slowly
(G3), which `heapFree` alone will not.

### 4.3 `tel/**` — telemetry (event, node → porta)

| path | kind | record |
|---|---|---|
| `tel/print` | E | `Print { text: str }` |
| `tel/log` | E | `Log { level: sym, text: str }` — `#debug`/`#info`/`#warn`/`#error` |
| `tel/m/<name>` | E | `Metric { value: int \| float, monoMs: int }` |

Lossy by policy: `tel/**` is the *first* thing dropped under G2 overflow pressure, and
the drop is counted (`Health.overruns`). Losing a log line to save a node is correct.

### 4.4 `sys/**` — the node's own faults (event, node → porta)

| path | kind | record |
|---|---|---|
| `sys/reset` | E | `Reset { reason: sym, bootId: int }` |
| `sys/overrun` | E | `Overrun { path: str, dropped: int }` — G2's counter |
| `sys/panic` | E, **must-deliver** | `Panic { … }` — §2.4 |
| `sys/metrics` | **C (cell)** | `Metrics { heapSize: int, liveBytes: int, heapHighWater: int, largestFreeBlock: int, gcCycles: int, handlesLive: int, handlesHighWater: int, queueHighWater: int, overruns: int }` — tuvm#24; ~70 B encoded, within §2.2 |

**Delivery class is a property of the channel, not a paragraph of prose.** `sys/panic`
is must-deliver; `tel/print` is drop-first. That is the whole rule.

**Kind C is a cell** (tuvm#32 option A, rung 9): the pigeonhole holds the latest
letter. A reader — porta, an agent, a second job — `peek:`s current state *by
name*, non-destructively, without draining a stream some other consumer is
counting on (the WHY-AGENTS rider, folded in here). The node app is the writer,
on a user-requested cadence (G9: no unrequested periodic work); as of tuvm#24
no periodic publisher exists — the record is built by `kernel metrics` and
published explicitly.

### 4.5 `dbg/*` — remote debug (event, both ways)

| path | kind | record |
|---|---|---|
| `dbg/req` | E, ↓ | `DbgReq { line: str }` |
| `dbg/resp` | E, ↑ | `DbgResp { line: str }` |

Porta stays a **stateless relay**; the debug session lives in the node (unchanged from
v1 §8). Long responses obey `MAX_VALUE_BYTES` like everything else — the debugger
paginates, which it must do for a serial console anyway.

---

## 5. Evolution (G8 — fleet version skew)

1. **Additive only.** New state = a new path, or a new *optional* field on an existing
   record. Never a changed field type, never a removed field, never a re-purposed name.
2. **The record name is the contract.** A breaking change gets a **new record name at a
   new path** — old and new nodes then simply do not overlap, instead of misreading each
   other. This is why there is no version field: a version integer only tells you that
   you cannot understand the value, which is exactly what an unknown path already tells
   you, with no extra mechanism.
3. **Consumers ignore unknown fields.** (The *codec* is closed to unknown tags and types;
   a record's field map is open to unknown keys. These are different layers, and
   conflating them is the easiest way to break rolling upgrades.)
4. **Consumers must not require fields added after their release.** Every field added
   post-hoc needs a defined absent-behaviour, per L5 rule 1 (`at:ifAbsent:`).

---

## 6. Open questions (to close at phase-3 design)

1. **The caps (§2.2) are a first cut.** 256/512 come from measured 6LoWPAN loss, but
   they should be re-derived on the G10 fleet-sim and on the real mesh. Specifically:
   is a smaller `MAX_BATCH_BYTES` (one fragment, ~80 B) better than a larger one plus
   retransmit, on a 5%-loss link? The soak fleet can answer this empirically.
2. **`arguments` in `App`** is an open map — the only unbounded-shape field in the
   catalogue. Cap it, or make it a `BlobRef` too?
3. **Cursor persistence granularity** (NVS write per check-in vs per batch; flash wear,
   G7) — `PROTOCOL-NSL.md` §6.1, unchanged.
4. **`Metric.value` admits int or float.** Float on a soft-float M4 is expensive. Is a
   fixed-point int + declared scale better for a sensor fleet?
5. **Does `obs/**` push per-path or snapshot-batched?** (§6.6 of the parent doc.) The
   size law says: whichever fits 512 bytes; measure.
6. **Tombstone retention at porta** — until the cursor passes, or time-boxed? **This is not a
   shrug; it is a live hazard — now porta#24.** A delete is only visible as a tombstone in the
   cursor stream, so a node that is away long enough for porta to reclaim tombstones past its
   cursor comes back, asks for `seq > N`, and porta **silently answers a question it cannot
   answer**: the node keeps running an app that was uninstalled, and nothing anywhere notices.
   This is exactly etcd's compaction-vs-watch hazard, and their fix is cheap and should just be
   taken: porta keeps a **compaction revision**, refuses an incremental sync from a cursor older
   than it, and forces a **full resync from cursor 0** — a path §3.1 already requires for cold
   boot and which therefore costs one comparison and one error code. Pin it in the §4.3 framing
   *before* first implementation, not after.

---

*Companion to `PROTOCOL-NSL.md`. The catalogue above is a proposal: it is the v1 verb
list (`PROTOCOL.md` §3) re-expressed as paths, plus the size law needed to make the
"protocol is the space" claim survive contact with a 127-byte radio frame and a 48 KB
heap.*
