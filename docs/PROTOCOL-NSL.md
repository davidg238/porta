# Porta ↔ nsl node protocol — the space-sync model

**Status: design direction, pre-implementation** (written 2026-07-11, distilled from
the tuvm architecture discussion of the same date; companion background in
`tuvm/review/WHY.md` §"The model, named" and §"Paths extend across the network").

---

## ADDENDUM 2026-07-13 — tuvm#32 decided; two things below are now WRONG

This document's central contradiction has been resolved. **tuvm#32 is CLOSED: the node's
space owns the value-holding channel kind (option A).** It shipped as tuvm rung 9
(`tuvm 0cadc2c` · `nsl-tuvm aa21b71` · `nsl-tests 8c9536c`). Two corrections to what follows,
which is otherwise unchanged and still correct:

**1. The word "retained" is retracted. Say CELL and STREAM.** "Retained" is MQTT's word for a
broker-side *delivery trick*; what we mean is a *state semantics* — **this name has a value**.
Read every `retained` below as **cell**, and every `event` as **stream**:

- a **cell** — a pigeonhole that HOLDS the latest letter. `put:value:` overwrites it,
  `peek:ifAbsent:` reads it without removing it. (`goal/**`, `obs/**`)
- a **stream** — a pigeonhole you EMPTY by reading. Each letter delivered once. (`tel/**`,
  `sys/**`, `dbg/*`, `goal/do/*`)

**2. §3.1's "No VM changes" is false, and that was the whole of tuvm#32.** §2 declares the
value-holding kind "per path prefix" (a property of *the space*) while §3.1 says the bridge is
an ordinary nsl job needing no VM change (which would make it a property of the bridge's
*private Map*). Both could not hold. **The space won**, because the space has `post` and `take`
and **no *call*** — a job cannot ask another job for a value — so a cell in the bridge's Map is
invisible to the supervisor reading `goal/apps/**`, exactly as §3.2 and §3.3 require it not to be.
The VM changed: a value slot on `SpaceChannel`, prims 105–107 (`spaceKind:` / `spacePeek:` /
`spacePut:value:`), and `Channel>>put:value:` / `peek:ifAbsent:`.

**3. There is no `clear:` and no tombstone.** David's rule, and it deleted a whole mechanism:
*"in industrial control there is no clear — there is a start button and a separate stop button,
and stop always wins."* A stopped loop is `auto = false`; an un-deployed app is
`runlevel: #stopped`. **Absence means only "nobody has ever said anything about this name"**, and
the safe answer to that is the fail-safe default. Where this document says *"delete = tombstone"*,
read: **the operator writes the stopped state explicitly**. If a record must ever be *removed*
(not stopped — removed), that is an **action** — a letter on a `do/` stream — not a property of
the pigeonhole.

**Still true, and still porta's problem:** the **compaction-vs-cursor hazard**. A node whose
cursor falls behind the point where porta forgot its history is silently stranded. Unanswered.

Full argument + the four interaction diagrams: `tuvm/review/cells-and-streams-v3.html`.
The option-A/option-C code diff: `tuvm/review/cells-sketch.ns`.

---

Scope split, decided 2026-07-11:

- **`kind: "toit"` nodes keep `PROTOCOL.md` (v1) unchanged.** The nodus fleet
  conforms to it and it works; v1 is frozen for that kind, not deprecated.
- **`kind: "nsl"` nodes (nsl-tuvm / nRF52840 / Zephyr) speak the protocol in this
  document.** Porta carries both, gated by the existing per-node `kind` column —
  the heterogeneity seam built for exactly this.
- The v1 `st` kind description ("Smalltalk → Berry `.bec`") predates the nsl-tuvm
  pivot and is superseded by this document for the Zephyr node class.

---

## 1. Why a second protocol — the convergence argument

This is not a redesign for taste. Reading v1 with the nsl node's tuple-space model
in hand, **v1 has been converging on a space protocol one revision at a time**, and
each revision was the discovery of a rule the space model has natively:

| v1 mechanism (and its bespoke rules) | What it is in space vocabulary |
|---|---|
| "Commands are declarative and absolute, last write wins" | a **retained latest-value channel** per target |
| `reboot`'s exception paragraph (imperative, exactly-once, terminal on delivery, no convergence) | an **event channel** — the state/event split, native |
| `node_config` echo: "only on cold boot and on change; absent never clobbers cache" | retained channel with publish-on-change; per-path values need no clobber rules |
| `chip`/`sdk`/`kind` "absent never clobbers known identity" | same — retained per-path values |
| gateway self-heal (diff desired vs reported config, re-enqueue divergent `set`s) | **anti-entropy between a `goal/` tree and an `obs/` tree** |
| `set-mode` atomicity rule (accept whole or reject whole) | one record at one path is atomic **by construction** |
| `set-forward` "the command is the whole policy, not a patch" | a retained record simply *is* the whole value |
| FATAL/panic "must-deliver subset" | per-channel delivery class |
| `commands?id=` drain-until-empty; `debug?id=` "same drain pattern" | `take:`-until-empty — the transport surface was already channels |

The structural diagnosis: **v1's unit of extension is the verb/schema, which is
multiplicative** — each new piece of node state touches the command codec, the report
shape, echo rules, self-heal, the conformance section, and every node kind. **A
space's unit of extension is the path, which is additive**: new state = new path +
a pinned record schema. v1's own evolution rule (G8-style "add optional fields
only") becomes "add paths" — the version of that rule that scales.

The nsl node makes this nearly free on its end: the node's space already exists
(`out:` / `take:` / `whenever:`, prefix-granted capabilities). The porta client on
the node is an ordinary **bridge job**, not a protocol stack.

### The one law

**Location transparency must not become failure transparency** (Waldo, Wyant,
Wollrath & Kendall, *A Note on Distributed Computing*, Sun Labs TR-94-29). The wire
does not make remote look local; it makes remote *nameable* while keeping its
delivery contract explicit:

- `out:` across the wire is **best-effort, batched, eventually delivered**;
- `whenever:` across the wire is a **subscription** (sync of changes since cursor);
- **`take:` never crosses the wire.** A distributed destructive read is a consensus
  problem (where JavaSpaces grew transactions); neither side destructively reads the
  other's space. Each side publishes into its own; the other syncs.

Remoteness lives **in the grant**: node jobs never learn that `goal/**` values come
from porta, but the bridge is the single ingress where overflow policy, auth, and
version skew are enforced — one door, same rules as any other event source.

---

## 2. The model — one picture

```
        PORTA (gateway)                            NSL NODE
  ┌──────────────────────────┐             ┌──────────────────────────┐
  │  per-node space mirror   │   check-in  │  the node's own space    │
  │                          │  exchange   │                          │
  │  node/<id>/goal/**  ─────┼────────────▶│  goal/**   (retained in) │
  │  node/<id>/obs/**   ◀────┼─────────────│  obs/**    (retained out)│
  │  node/<id>/tel/**   ◀────┼─────────────│  tel/**    (events out)  │
  │  node/<id>/sys/**   ◀────┼─────────────│  sys/**    (events out)  │
  │  node/<id>/dbg/req  ─────┼────────────▶│  dbg/req   (events in)   │
  │  node/<id>/dbg/resp ◀────┼─────────────│  dbg/resp  (events out)  │
  └──────────────────────────┘             └──────────────────────────┘
       porta writes ONLY goal/** and dbg/req;    the bridge job holds the
       the node writes ONLY its own node/<id>/** radio capability + these grants
```

Two channel kinds, declared per path prefix (this replaces v1's per-verb rules):

- **retained** — the path holds a latest value; sync transfers values changed since
  a cursor; a delete is a tombstone. State lives here (`goal/**`, `obs/**`).
- **event** — an append-only queue; sync transfers entries once, in order, deduped
  by sequence number. Actions and streams live here (`tel/**`, `sys/**`, `dbg/*`,
  `goal/do/*`).

Every value on the wire is a **CBOR record** from the nsl message algebra (null,
bool, int, float, string, bytes, symbol, list, map, record — the L6/cbor.md
encoding). Schemas are pinned **per path prefix**; evolution is additive (new
paths, new optional record fields).

---

## 3. The node end

### 3.1 The bridge job

An ordinary supervised nsl job, granted: the radio/transport capability, write
access to `goal/**` and `dbg/req` (inbound), and read/reaction access to `obs/**`,
`tel/**`, `sys/**`, `dbg/resp` (outbound). No VM changes; crash of the bridge is a
contained fault and porta sees a late check-in, nothing worse.

On each check-in (cadence per `obs/mode`, exactly as v1 derives liveness):

1. **Pull**: request `goal/**` (+ `dbg/req`) entries with `seq > cursor`; apply each
   to the local space (retained paths become local retained values; `goal/do/*`
   events are `out:`ed once); persist the new cursor (NVS) only after apply.
2. **Push**: send retained `obs/**` values changed since last acked push, plus
   queued `tel/**` / `sys/**` / `dbg/resp` events, each batch tagged with a
   node-side sequence for gateway dedup.

Cold boot: cursor may reset to 0 → porta replays the full retained `goal/**` tree.
**This replaces v1's boot-echo drift-healing with full-state resync by
construction** — a reflashed node converges with no special rules.

### 3.2 Node-side path map (what replaces the nine verbs)

| v1 verb / report field | nsl path | kind | record shape (sketch) |
|---|---|---|---|
| `run` | `goal/apps/<name>` | retained | `{crc:, size:, triggers:, runlevel:, lifecycle:, arguments:}` |
| `stop` | *(tombstone of the above)* | retained | delete = tombstone; reconcile uninstalls |
| `set-mode` | `goal/mode` | retained | `{mode:, minAwakeS:, maxAwakeS:, maxAsleepS:, loopSleepS:}` — atomic because it is one record |
| `set-name` | `goal/name` | retained | string |
| `set-forward` | `goal/forward` | retained | whole-policy record, as v1 already demands |
| `set` (per-app key) | `goal/config/<app>/<key>` | retained | scalar; per-key last-write-wins is per-path, native |
| `reboot` | `goal/do/reboot` | **event** | `{}` — exactly-once via cursor; v1's exception paragraph dissolves |
| `debug attach/detach` | `goal/debug/<app>` | retained | `{action:}` — plus the `dbg/req`/`dbg/resp` event pair |
| `profile` | `goal/profile/<app>` | retained | `{action:, durationS:, continuous:}` |
| report `apps` | `obs/apps/<name>` | retained | observed `{crc:, runlevel:, lifecycle:, triggers:}` |
| report `config` echo | `obs/config/<app>/<key>` | retained | scalar echo |
| `node_config` echo | `obs/mode`, `obs/name`, `obs/forward` | retained | publish-on-change is what retained channels do; no echo-cadence rules |
| `health` | `obs/health` | retained | `{uptimeUs:, wakes:, pollTimeouts:}` |
| `health.reset` (fault) | `sys/reset` | event | `{category:, code:}` — v1's "data_log on first fault" is just an event |
| telemetry `print`/`log` | `tel/print`, `tel/log` | event | `{text:}` / `{level:, text:}` |
| telemetry `metric` | `tel/m/<name>` | event | `{value:, ts:}` |
| telemetry `panic` | `sys/panic` | event, **must-deliver** | `{blob:}` (bytes) — delivery class is per-channel, not a prose rule |
| debug lines | `dbg/req` ↓ / `dbg/resp` ↑ | event | `{line:}` — porta stays a stateless relay, session lives in the node (unchanged from v1 §8) |

On the node these are the *same paths* its local jobs already use — the supervisor
reacts to `goal/apps/**` exactly as it reacts to any channel. There is no separate
"protocol handler": **the protocol is the space, filtered by grant.**

### 3.3 What stays OFF the space

- **Bulk image payload** — data plane, not control plane (same law as drivers: big
  bytes never ride channels). Delivery stays a bulk transfer selected by content
  hash, referenced from the `goal/apps/<name>` record. v1 §5's raw-bytes+CRC model
  carries over as-is (transport per node class: TFTP today, CoAP blockwise on
  Thread).
- **Auth/identity (G5)** — the grant machinery cannot ride the thing it guards.
  Node identity + session auth wrap the exchange at the transport layer; prefix
  authority is then enforced *inside*: porta may write only `goal/**` and
  `dbg/req`; the node may write only its own `node/<id>/**` mirror.
- **Profiler blobs** — opaque and potentially large; delivered like payloads
  (referenced, pulled in bulk), not as events.

---

## 4. The porta end

### 4.1 Store

One mechanism replaces the per-feature tables (commands queue, report cache,
config-desired projection, debug_response, …):

- `space_retained(node_id, path, seq, value_cbor, tombstone, ts)` — latest value
  per path, monotonic per-node `seq` assigned on write; tombstones retained until
  the node's cursor passes them.
- `space_events(node_id, path, seq, value_cbor, ts, direction)` — append-only;
  inbound (node→porta) rows are the telemetry/forensics log, outbound
  (porta→node) rows are the command/debug queues.

Convergence is **structural**: `goal/X` vs `obs/X` is a tree diff — the UI's
desired-vs-observed view and the self-heal loop are the same query. Self-heal as a
*mechanism* disappears: a node whose obs diverges simply hasn't applied the goal
seq yet (visible), and a node that lost state resyncs from cursor 0 (automatic).

### 4.2 Operator surface

CLI/API verbs become path writes and tree reads — the vocabulary stays
operator-friendly while the plumbing unifies:

- `porta app run <node> <name> …` → write `goal/apps/<name>` record (+ stage image)
- `porta mode <node> deep-sleep …` → write `goal/mode`
- `porta reboot <node>` → append `goal/do/reboot` event
- `porta get <node> [prefix]` → dump the retained tree (goal + obs, diffed)
- `porta tail <node> [prefix]` → follow `tel/**` / `sys/**` events
- `porta debug send/poll` → `dbg/req` append / `dbg/resp` read-after-cursor (v1 §8
  semantics preserved verbatim)

Liveness derivation is unchanged from v1: `offline = k × cadence`, cadence read
from the retained `obs/mode`. The heartbeat *is* the exchange.

### 4.3 Wire framing (first cut — to be ratified at phase-3 design)

Keep the node-initiated, gateway-passive shape (it is deep-sleep-correct and
transport-agnostic):

- **Pull**: node requests `space?id=<id>&cursor=<n>` → body = CBOR array of
  `[seq, path, kind, value|tombstone]` for outbound entries with `seq > n`, plus
  the new high-water. Empty body = up to date.
- **Push**: node sends CBOR array of `[nodeSeq, path, kind, value]`; gateway
  dedupes on `nodeSeq` high-water, acks the high-water.
- Transport: whatever the node class carries — the v1 TFTP surface can carry these
  two resources unchanged on day one; CoAP (blockwise, observe) is the natural
  Thread evolution. Framing is transport-independent by construction.

---

## 5. Coexistence & migration

- Porta serves v1 and this protocol side-by-side, selected by node `kind`. No Toit
  node ever changes.
- First implementation target: the **G10 host fleet-sim** (N host tuvm VMs + porta
  over localhost UDP) — prove exchange, cursor resync, kill-mid-transfer recovery
  there before any radio is involved.
- The nsl node's conformance doc is this file; the bridge job + C seam land with
  the phase-3 (CBOR wire) rungs in `tuvm/review/program-sequence.md`.

## 6. Open questions (for the phase-3 design doc)

1. Cursor persistence granularity on the node (NVS write per check-in vs
   per-batch; flash-wear budget, G7 discipline).
2. Tombstone retention window at porta (until cursor passes vs time-boxed).
3. Event-queue overflow at the *gateway* mirror (a chatty node vs porta storage) —
   per-prefix retention policy, mirroring the node's own G2 vocabulary.
4. `dbg` session flow-control (v1 relies on poll pacing; keep, or add a window?).
5. Auth handshake concretely (G5): what wraps the exchange on localhost UDP for the
   sim vs Thread in the field; where the node's porta-identity pairing lives (G7).
6. Whether `obs/**` pushes are per-path or snapshot-batched on constrained MTUs
   (Thread fragmentation vs chattiness) — measure in the fleet-sim.
7. Record schemas per prefix: pin in a `PROTOCOL-NSL-RECORDS.md` (the L6/cbor.md
   companion) before first implementation; additive evolution only.

---

---

## Addendum 2026-07-12 — two internal contradictions, found while pinning the records

Read before implementing. Neither invalidates the model; both need a decision.

**1. §2 vs §3.1 — where do retained channels live? (tuvm#32, BLOCKS phase 3)**

§2 defines retained as a **channel kind**, "declared per path prefix" — a property of *the
space*. §3.1 promises the bridge job needs "**No VM changes**". **These cannot both hold.**
The node's space today is a pure event **queue** (`tuvm/src/space.c`: `space_post` /
`space_take` / `spaceRegister`) — it has no latest-value slot, no per-path sequence, no
tombstone, and no changed-since-cursor query. Something must provide those four things.

The deciding question is **not** cost — an nsl `Map` in the bridge job can hold all four
cheaply. It is **who besides the bridge needs a retained path.** This document assumes others
do, twice: §3.2 has the supervisor reacting to `goal/apps/**` *"exactly as it reacts to any
channel"*, and §3.3's tagline is *"the protocol **is** the space, filtered by grant"*. Both
claims are only true if the **space** owns retained. If instead the bridge owns it privately,
the supervisor must ask the bridge — jobs then learn the bridge exists, the bridge becomes a
second authority beside the space, and §1's convergence argument (the whole reason this
protocol beats v1) quietly stops being true.

So: an nsl library is a bet that **the bridge is the only consumer, forever**; a C channel kind
is the bet that it is not, paid up front in prims, GC roots and footprint. Decision material:
`tuvm/review/retained-channels.html`.

**2. §3.2 vs §3.3 — `sys/panic` cannot carry a blob**

§3.2 gives `sys/panic` the shape `{blob: bytes}` and marks it *must-deliver*; §3.3 says bulk
never rides a channel. Both cannot hold. **Resolved in `PROTOCOL-NSL-RECORDS.md` §2.4**: a
bounded inline summary (`reason`, `bootId`, `monoMs`, truncated `detail`) plus a `BlobRef` for
the core dump. Truncation is legal *here and nowhere else*, because a partial reason beats no
reason — and the point is that a node dying of OOM can still **report** that it died of OOM,
since the report is ~60 bytes and needs no buffer to build.

**Also new:** `PROTOCOL-NSL-RECORDS.md` pins the per-prefix schemas this document defers, and
the **size law** that §3.3 states only as an intent. The binding constraint is the *radio*, not
the heap: 6LoWPAN fragments above ~102 B and a datagram survives only if every fragment does,
so at the bench fleet's measured worst-node loss (4.6%/datagram) a 1 KB batch arrives ~54% of
the time. This makes §4.3's wire framing a real constraint, not a free choice — see open
question 6 there.

**3. Vocabulary: "retained" should be renamed to *cell*.**

The word is borrowed from MQTT, where a *retained message* is a broker-side **delivery trick**
(the broker keeps the last message on a topic so late subscribers receive one). It names a
**delivery side-effect**, when what this protocol means is a **state semantics**: *this name
has a value*. The borrowed word actively invites the misreading that "retained" is a flag one
could sprinkle onto an existing channel — which is the single most important thing to *not*
believe about it.

Proposed, and used in `tuvm/review/retained-channels-v2.html`: a name resolves either to a
**cell** (it *has* a value; read is non-destructive, write overwrites, delete tombstones) or a
**stream** (it *delivers* occurrences; read consumes, write appends). Every system that has
solved this uses the first word or a synonym — a *file* (Plan 9), a *znode* (ZooKeeper), a
*key* (etcd), a *resource* (REST). None of them say "retained".

**4. What this protocol actually is, in the literature's terms.**

Worth recording, because it tells us which prior art to steal from and which to ignore:

- The **naming** model is Plan 9's — the namespace *is* the interface, `walk` ≈ `resolve:`, and
  Plan 9's `fid` is precisely our `Channel` capability. Their lesson for §2's "two channel
  kinds" is: **don't add a *kind*, add a *server*** — keep the verbs uniform and let whoever
  serves a name supply the semantics (`/net`: you open TCP by writing to a file).
- But **we cannot use Plan 9's remote model.** A mount means every read is a round trip to a
  server that must be up *right now*; our node sleeps on a duty cycle, loses 2–5% of datagrams,
  and must keep running while partitioned. **Disconnected operation is a requirement**, so we
  cannot fetch on demand, so we must **replicate** — and *that* is where `seq`, cursors and
  tombstones come from. They are forced by the radio, not chosen for elegance.
- Which means **we are building etcd, not 9P**: their `revision` is our per-path `seq`, their
  *watch-from-revision* is our `changedSince: cursor`, their tombstones are ours — and **their
  compaction hazard is ours too** (porta#24: a node whose cursor falls behind the retention
  point is silently stranded). Steal their vocabulary *and* their scars.
- **LOCUS** is the cautionary bookend to §1's "one law": Plan 9 got away with far more
  transparency than LOCUS largely because its interface was *file I/O, which everyone already
  knows can fail*.

---

*Provenance: this design was distilled from the 2026-07-11 architecture review
discussion (actors-at-the-grain / Linda-at-the-joints framing; see
`tuvm/review/WHY.md`). The convergence table in §1 cites v1 behaviors verbatim from
`PROTOCOL.md` as of the same date.*
