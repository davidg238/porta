# Porta ↔ nsl node protocol — the space-sync model

**Status: design direction — *ratified* 2026-07-14, pre-implementation on the porta
side.** The node/VM half of the model shipped as tuvm **rung 9** (tuvm#32 closed,
option A: the node's space owns the value-holding channel kind — see §3.1); the
porta half (bridge job, store, wire) is unbuilt and lands with the phase-3 rungs in
`tuvm/review/program-sequence.md`. This document is the conformance spec for
`kind: "nsl"` nodes.

**Companions:**
- Record schemas per path prefix + the radio size law: [`PROTOCOL-NSL-RECORDS.md`](PROTOCOL-NSL-RECORDS.md).
- The Toit-node (v1) protocol this one sits beside: [`PROTOCOL.md`](PROTOCOL.md).
- Background / derivation: `tuvm/review/WHY.md` (§"The model, named", §"Paths extend across the network").
- How this doc reached its current form: [`PROTOCOL-NSL-draft-2026-07-11.md`](PROTOCOL-NSL-draft-2026-07-11.md) — the superseded genesis draft, kept as the reasoning trail.

---

## 0. Scope — one gateway, two node kinds

Porta carries two node protocols side by side, selected by the existing per-node
`kind` column (the heterogeneity seam built for exactly this):

- **`kind: "toit"` nodes speak `PROTOCOL.md` (v1), unchanged.** The nodus fleet
  conforms to it and it works; v1 is *frozen for that kind*, not deprecated. No Toit
  node changes because of this document.
- **`kind: "nsl"` nodes (nsl-tuvm / nRF52840 / Zephyr) speak the protocol in this
  document** — the **space-sync** model.

The v1 `st` kind ("Smalltalk → Berry `.bec`") predates the nsl-tuvm pivot and is
superseded by this document for the Zephyr node class.

> **North star (conditional, not a commitment).** If paths-over-verbs proves out as
> nsl-tuvm deploys under porta, the intent is to revisit the Toit `nodus` fleet and
> try to align it onto this same space model — ultimately *replacing* v1's verb
> protocol rather than carrying both forever. That is an aspiration to be earned by
> the nsl deployment, not a scheduled migration; §6 treats coexistence as a bridge
> toward it, not a permanent state.

---

## 1. Goals and non-goals

### Goals

- **Extension is additive, not multiplicative.** New node state = a new *path* + a
  pinned record schema. v1's unit of extension is the verb/schema, which touches the
  command codec, the report shape, echo rules, self-heal, the conformance section
  and every node kind at once. A path touches none of them. (The full v1→space
  convergence argument is §2.)
- **Disconnected, duty-cycled operation is a requirement, not a degraded mode.** A
  node sleeps on a duty cycle, loses 2–5 % of datagrams, and must keep running while
  partitioned. The protocol *replicates* state and reconciles on reconnect; it never
  fetches on demand.
- **Full-state resync by construction.** A reflashed or amnesiac node resyncs from
  cursor 0 with no special rules — cold boot replays the whole `goal/**` tree. v1's
  boot-echo drift-healing dissolves.
- **Convergence is a query, not a mechanism.** `goal/X` versus `obs/X` is a tree
  diff; the UI's desired-vs-observed view and the self-heal loop are the *same*
  query. There is no separate self-heal engine.
- **One store replaces the per-feature tables** (commands queue, report cache,
  config-desired projection, debug_response, …) — see §4.1.
- **Remoteness lives in the grant.** Node jobs never learn that `goal/**` values
  arrive from porta; the bridge is the single ingress where overflow policy, auth
  and version skew are enforced — one door, same rules as any other event source.

### Non-goals

- **No distributed destructive read.** `take:` never crosses the wire. A remote
  destructive read is a consensus problem (where JavaSpaces grew transactions);
  neither side destructively reads the other's space. Each side publishes into its
  own; the other syncs (§2, "the one law").
- **No bulk payloads on the space.** Firmware images and profiler blobs are *data
  plane*: delivered by a bulk transfer selected by content hash, merely *referenced*
  from a control-plane record. Big bytes never ride channels (the same law as
  drivers). See §3.3.
- **Auth does not ride the space it guards.** Node identity + session auth wrap the
  exchange at the transport layer; prefix authority is enforced *inside* (§3.3).
- **This is not a synchronous mount.** We are not building 9P: there is no fetch
  from a server that must be up *right now*. We replicate, precisely because the node
  must survive partition (§2, "which prior art").
- **No request/reply between jobs.** The space has `post`/`take`/`peek`, and no
  *call* — a job cannot ask another job for a value. This is load-bearing: it is
  *why* the value-holding kind had to live in the space and not in the bridge's
  private map (§3.1).

---

## 2. Why this shape — the convergence argument

This is not a redesign for taste. Read with the nsl node's tuple-space model in
hand, **v1 has been converging on a space protocol one revision at a time**, and
each revision was the rediscovery of a rule the space model has natively:

| v1 mechanism (and its bespoke rules) | What it is in space vocabulary |
|---|---|
| "Commands are declarative and absolute, last write wins" | a **cell** per target — the name *has* a value |
| `reboot`'s exception paragraph (imperative, exactly-once, terminal on delivery, no convergence) | a **stream** entry — the state/event split, native |
| `node_config` echo: "only on cold boot and on change; absent never clobbers cache" | a cell with publish-on-change; per-path values need no clobber rules |
| `chip`/`sdk`/`kind` "absent never clobbers known identity" | same — per-path cells |
| gateway self-heal (diff desired vs reported config, re-enqueue divergent `set`s) | **anti-entropy between a `goal/` tree and an `obs/` tree** |
| `set-mode` atomicity rule (accept whole or reject whole) | one record at one path is atomic **by construction** |
| `set-forward` "the command is the whole policy, not a patch" | a cell simply *is* the whole value |
| FATAL/panic "must-deliver subset" | a per-stream delivery class |
| `commands?id=` drain-until-empty; `debug?id=` "same drain pattern" | `take:ifEmpty:`-until-empty — the transport surface was already channels |

The structural diagnosis: **v1's unit of extension is the verb/schema, which is
multiplicative** — each new piece of node state touches the command codec, the
report shape, echo rules, self-heal, the conformance section and every node kind. **A
space's unit of extension is the path, which is additive**: new state = new path +
a pinned record schema. v1's own evolution rule ("add optional fields only") becomes
"add paths" — the version of that rule that scales.

The nsl node makes this nearly free on its end: the node's space already exists
(`out:` / `take:ifEmpty:` / `whenever:do:` / `put:value:` / `peek:ifAbsent:`,
prefix-granted capabilities). The porta client on the node is an ordinary **bridge
job**, not a protocol stack.

### The one law

**Location transparency must not become failure transparency** (Waldo, Wyant,
Wollrath & Kendall, *A Note on Distributed Computing*, Sun Labs TR-94-29). The wire
does not make remote look local; it makes remote *nameable* while keeping its
delivery contract explicit:

- `out:` across the wire is **best-effort, batched, eventually delivered**;
- `whenever:do:` across the wire is a **subscription** (sync of changes since a cursor);
- **`take:` never crosses the wire** (non-goal §1). Each side publishes into its own
  space; the other syncs.

### Which prior art — and which to ignore

Recorded because it tells us what to steal and what to leave:

- The **naming** model is Plan 9's — the namespace *is* the interface, `walk` ≈
  `resolve:`, and Plan 9's `fid` is precisely our `Channel` capability. Its lesson
  for "two kinds": keep the verbs uniform and let whoever serves a name supply the
  semantics.
- **But we cannot use Plan 9's remote model.** A mount means every read is a round
  trip to a server that must be up *right now*; our node sleeps, loses datagrams, and
  runs while partitioned. Disconnected operation is a requirement → we cannot fetch
  on demand → we must **replicate** — and *that* is where `seq`, cursors and the
  stopped-state discipline come from. They are forced by the radio, not chosen for
  elegance.
- Which means **we are building etcd, not 9P**: their `revision` is our per-path
  `seq`, their *watch-from-revision* is our `changedSince: cursor`, and **their
  compaction hazard is ours too** (porta#24 — a node whose cursor falls behind
  porta's retention point is silently stranded; §7). Steal their vocabulary *and*
  their scars.
- **LOCUS** is the cautionary bookend: Plan 9 got away with far more transparency
  than LOCUS largely because its interface was *file I/O, which everyone already
  knows can fail*. Our contract keeps failure visible on purpose.

---

## 3. The model — one picture

```
        PORTA (gateway)                            NSL NODE
  ┌──────────────────────────┐             ┌──────────────────────────┐
  │  per-node space mirror   │   check-in  │  the node's own space    │
  │                          │  exchange   │                          │
  │  node/<id>/goal/**  ─────┼────────────▶│  goal/**   (cells in)    │
  │  node/<id>/obs/**   ◀────┼─────────────│  obs/**    (cells out)   │
  │  node/<id>/tel/**   ◀────┼─────────────│  tel/**    (streams out) │
  │  node/<id>/sys/**   ◀────┼─────────────│  sys/**    (streams out) │
  │  node/<id>/dbg/req  ─────┼────────────▶│  dbg/req   (stream in)   │
  │  node/<id>/dbg/resp ◀────┼─────────────│  dbg/resp  (stream out)  │
  └──────────────────────────┘             └──────────────────────────┘
       porta writes ONLY goal/** and dbg/req;    the bridge job holds the
       the node writes ONLY its own node/<id>/** radio capability + these grants
```

Every name resolves to one of **two kinds**, fixed by its path prefix (this replaces
v1's per-verb rules). The kind is declared by the *first write* and the wrong verb on
an existing name is an ordinary, catchable error (enforced in nsl `Channel`, not in
the C space — which is storage, not policy):

- **cell** — a pigeonhole that *holds* the latest letter. `put:value:` overwrites it;
  `peek:ifAbsent:` reads it **without removing it**. State lives here (`goal/**`,
  `obs/**`). Sync transfers cell *values* changed since a cursor.
- **stream** — a pigeonhole you *empty* by reading. `out:value:` appends;
  `take:ifEmpty:` removes the oldest, once. Each letter is delivered once. Actions
  and telemetry live here (`tel/**`, `sys/**`, `dbg/*`, `goal/do/*`). Sync transfers
  entries once, in order, deduped by sequence number.

Every value on the wire is a **CBOR record** from the nsl message algebra (null,
bool, int, float, string, bytes, symbol, list, map, record — the L6/`cbor.md`
encoding). Schemas are pinned **per path prefix** in `PROTOCOL-NSL-RECORDS.md`;
evolution is additive (new paths, new optional record fields).

### No `clear:`, no tombstone

There is no delete verb and no tombstone. This is a deliberate deletion of a whole
mechanism, on the industrial-control rule: *there is no "clear" — there is a start
button and a separate stop button, and stop always wins.*

- **Absence means only "nobody has ever said anything about this name,"** and the
  safe answer to absence is the fail-safe default.
- A stopped loop is `auto: false`; an un-deployed app is `runlevel: #stopped`. The
  operator **writes the stopped state explicitly** — it is a value, not a deletion.
- If a record must ever be genuinely *removed* (not stopped — removed), that is an
  **action**: a letter on a `do/` stream, not a property of the pigeonhole.

### `seq`, cursors, and `changedSince:`

Replication needs three things the raw event queue never had, and rung 9 gave the
space: a latest-value slot per cell, a monotonic per-path `seq`, and a
changed-since-cursor query. A node persists its cursor (NVS) only *after* applying a
batch, so a crash mid-apply re-pulls rather than skips. Cold boot = cursor 0 = full
replay.

---

## 4. The node end

### 4.1 The bridge job

An ordinary supervised nsl job, granted: the radio/transport capability, write
access to `goal/**` and `dbg/req` (inbound), and read/reaction access to `obs/**`,
`tel/**`, `sys/**`, `dbg/resp` (outbound). A crash of the bridge is a contained
fault: porta sees a late check-in, nothing worse.

**The VM did change — and had to.** The value-holding kind lives in the *space*, not
in the bridge's private map. tuvm#32 settled this (rung 9, option A): because the
space has `post`/`take`/`peek` and **no call**, a cell held privately in the bridge
would be invisible to the supervisor reading `goal/apps/**` — and §4.2/§4.3's whole
claim ("the protocol *is* the space, filtered by grant") would be false. So the space
gained: a value slot on `SpaceChannel`, prims 105–107 (`spaceKind:` / `spacePeek:` /
`spacePut:value:`), and the nsl `Channel>>put:value:` / `peek:ifAbsent:` verbs (with
`take:ifEmpty:` as the sole stream read, tuvm#21). The bridge is a *consumer* of that
kind, not the owner of it.

On each check-in (cadence per `obs/mode`, exactly as v1 derives liveness):

1. **Pull** — request `goal/**` (+ `dbg/req`) entries with `seq > cursor`; apply each
   to the local space (cell paths become local cell values via `put:value:`;
   `goal/do/*` stream entries are `out:`ed once); persist the new cursor (NVS) only
   after apply.
2. **Push** — send cell `obs/**` values changed since the last acked push, plus
   queued `tel/**` / `sys/**` / `dbg/resp` stream entries, each batch tagged with a
   node-side sequence for gateway dedup.

On the node these are the *same paths* its local jobs already use — the supervisor
reacts to `goal/apps/**` exactly as it reacts to any channel. There is no separate
"protocol handler": **the protocol is the space, filtered by grant.**

### 4.2 Node-side path map (what replaces the nine verbs)

| v1 verb / report field | nsl path | kind | record shape (sketch) |
|---|---|---|---|
| `run` | `goal/apps/<name>` | cell | `{crc:, size:, triggers:, runlevel:, lifecycle:, arguments:}` |
| `stop` | `goal/apps/<name>` | cell | write `runlevel: #stopped` — a value, not a delete; reconcile uninstalls |
| `set-mode` | `goal/mode` | cell | `{mode:, minAwakeS:, maxAwakeS:, maxAsleepS:, loopSleepS:}` — atomic because it is one record |
| `set-name` | `goal/name` | cell | string |
| `set-forward` | `goal/forward` | cell | whole-policy record, as v1 already demands |
| `set` (per-app key) | `goal/config/<app>/<key>` | cell | scalar; per-key last-write-wins is per-path, native |
| `reboot` | `goal/do/reboot` | **stream** | `{}` — exactly-once via cursor; v1's exception paragraph dissolves |
| `debug attach/detach` | `goal/debug/<app>` | cell | `{action:}` — plus the `dbg/req`/`dbg/resp` stream pair |
| `profile` | `goal/profile/<app>` | cell | `{action:, durationS:, continuous:}` |
| report `apps` | `obs/apps/<name>` | cell | observed `{crc:, runlevel:, lifecycle:, triggers:}` |
| report `config` echo | `obs/config/<app>/<key>` | cell | scalar echo |
| `node_config` echo | `obs/mode`, `obs/name`, `obs/forward` | cell | publish-on-change is what cells do; no echo-cadence rules |
| `health` | `obs/health` | cell | `{uptimeUs:, wakes:, pollTimeouts:}` |
| `health.reset` (fault) | `sys/reset` | stream | `{category:, code:}` — v1's "data_log on first fault" is just an entry |
| telemetry `print`/`log` | `tel/print`, `tel/log` | stream | `{text:}` / `{level:, text:}` |
| telemetry `metric` | `tel/m/<name>` | stream | `{value:, ts:}` |
| telemetry `panic` | `sys/panic` | stream, **must-deliver** | bounded inline summary + `BlobRef` (RECORDS §2.4) |
| debug lines | `dbg/req` ↓ / `dbg/resp` ↑ | stream | `{line:}` — porta stays a stateless relay, session lives in the node (v1 §8) |

### 4.3 What stays OFF the space

- **Bulk image payload** — data plane. Delivery is a bulk transfer selected by
  content hash, referenced from the `goal/apps/<name>` record. v1 §5's raw-bytes+CRC
  model carries over as-is (transport per node class: TFTP today, CoAP blockwise on
  Thread).
- **Auth / identity (G5)** — the grant machinery cannot ride the thing it guards.
  Node identity + session auth wrap the exchange at the transport layer; prefix
  authority is enforced *inside*: porta may write only `goal/**` and `dbg/req`; the
  node may write only its own `node/<id>/**` mirror.
- **Profiler blobs** — opaque and potentially large; delivered like payloads
  (referenced, pulled in bulk), not as stream entries.

`sys/panic` is the one apparent exception, and it is not one: the *report* is a
bounded ~60-byte inline summary (reason, bootId, monoMs, truncated detail) plus a
`BlobRef` for the core dump. Truncation is legal here and nowhere else — a node
dying of OOM can still **report** that it died of OOM, because the report needs no
buffer to build (RECORDS §2.4).

---

## 5. The porta end

### 5.1 Store

One mechanism replaces the per-feature tables (commands queue, report cache,
config-desired projection, debug_response, …):

- `space_cells(node_id, path, seq, value_cbor, ts)` — latest value per path,
  monotonic per-node `seq` assigned on write. No tombstone column: a "stopped" record
  is an ordinary value; a genuinely removed path is driven by a `do/` action.
- `space_events(node_id, path, seq, value_cbor, ts, direction)` — append-only;
  inbound (node→porta) rows are the telemetry/forensics log, outbound (porta→node)
  rows are the command/debug queues.

Convergence is **structural**: `goal/X` vs `obs/X` is a tree diff — the UI's
desired-vs-observed view and the self-heal loop are the same query. Self-heal as a
*mechanism* disappears: a node whose `obs` diverges simply hasn't applied the `goal`
seq yet (visible), and a node that lost state resyncs from cursor 0 (automatic).

### 5.2 Operator surface

CLI/API verbs become path writes and tree reads — the vocabulary stays
operator-friendly while the plumbing unifies:

- `porta app run <node> <name> …` → write `goal/apps/<name>` cell (+ stage image)
- `porta app stop <node> <name>` → write `goal/apps/<name>` with `runlevel: #stopped`
- `porta mode <node> deep-sleep …` → write `goal/mode` cell
- `porta reboot <node>` → append `goal/do/reboot` stream entry
- `porta get <node> [prefix]` → dump the cell tree (goal + obs, diffed)
- `porta tail <node> [prefix]` → follow `tel/**` / `sys/**` streams
- `porta debug send/poll` → `dbg/req` append / `dbg/resp` read-after-cursor (v1 §8
  semantics preserved verbatim)

Liveness derivation is unchanged from v1: `offline = k × cadence`, cadence read from
the `obs/mode` cell. The heartbeat *is* the exchange.

### 5.3 Wire framing (first cut — ratified at phase-3 design)

Keep the node-initiated, gateway-passive shape (deep-sleep-correct and
transport-agnostic):

- **Pull** — node requests `space?id=<id>&cursor=<n>` → body = CBOR array of
  `[seq, path, kind, value]` for outbound entries with `seq > n`, plus the new
  high-water. Empty body = up to date.
- **Push** — node sends CBOR array of `[nodeSeq, path, kind, value]`; gateway dedupes
  on `nodeSeq` high-water and acks it.
- Transport — whatever the node class carries: the v1 TFTP surface can carry these
  two resources unchanged on day one; CoAP (blockwise, observe) is the natural Thread
  evolution. Framing is transport-independent by construction.

**The size law is a real constraint, not a free choice.** The binding limit is the
*radio*, not the heap: 6LoWPAN fragments above ~102 B, and a datagram survives only
if every fragment does, so at the bench fleet's measured worst-node loss
(4.6 %/datagram) a 1 KB batch arrives ~54 % of the time. This governs whether
`obs/**` pushes go per-path or snapshot-batched (§7 open question) — measure it in
the fleet-sim (RECORDS "the size law").

---

## 6. Coexistence & migration

- Porta serves v1 and this protocol side by side, selected by node `kind`. No Toit
  node changes.
- **First implementation target: the G10 host fleet-sim** (N host tuvm VMs + porta
  over localhost UDP) — prove the exchange, cursor resync, and kill-mid-transfer
  recovery there before any radio is involved.
- The nsl node's conformance doc is this file; the bridge job + C seam land with the
  phase-3 (CBOR wire) rungs in `tuvm/review/program-sequence.md`.
- **The bridge toward the north star (§0).** Coexistence is the mechanism that lets
  the nsl model earn its keep in production *before* any Toit node is touched. If it
  earns it, the follow-on is a v1→space alignment for the `nodus` fleet — the verb
  protocol retired, not perpetually carried. That step is out of scope here and gated
  entirely on the nsl deployment going well.

---

## 7. Open questions (for the phase-3 design doc)

1. **Cursor persistence granularity** on the node (NVS write per check-in vs
   per-batch; flash-wear budget, G7 discipline).
2. **Porta retention window + the compaction-vs-cursor hazard** (porta#24, *still
   unanswered*): how long porta keeps cell history, and what happens to a node whose
   cursor falls behind that point — etcd's compaction scar, ours too (§2). This is the
   one hazard the cell/stream decision did *not* close.
3. **Event-queue overflow at the *gateway* mirror** (a chatty node vs porta storage)
   — per-prefix retention policy, mirroring the node's own G2 overflow vocabulary.
4. **`dbg` session flow-control** (v1 relies on poll pacing; keep, or add a window?).
5. **Auth handshake, concretely (G5)** — what wraps the exchange on localhost UDP for
   the sim vs Thread in the field; where the node's porta-identity pairing lives (G7).
6. **`obs/**` push shape** — per-path or snapshot-batched on constrained MTUs (Thread
   fragmentation vs chattiness); decided by measurement against the §5.3 size law.

*(The former open question "pin the per-prefix record schemas" is now **done** —
`PROTOCOL-NSL-RECORDS.md`.)*

---

## 8. Provenance & decision log

Distilled from the 2026-07-11 architecture review (actors-at-the-grain /
Linda-at-the-joints framing; `tuvm/review/WHY.md`). The §2 convergence table cites v1
behaviours verbatim from `PROTOCOL.md`.

Decisions folded into this edition (full reasoning trail in
[`PROTOCOL-NSL-draft-2026-07-11.md`](PROTOCOL-NSL-draft-2026-07-11.md)):

- **tuvm#32 CLOSED — the space owns the value-holding kind** (rung 9, option A). The
  bridge is a consumer, not the owner; "no VM changes" was wrong, and the VM changed
  (§4.1).
- **Vocabulary: cell / stream**, never "retained". "Retained" is MQTT's word for a
  broker-side delivery trick; we mean a *state semantics* — *this name has a value*.
  Every system that solved this uses the first word or a synonym (Plan 9 *file*,
  ZooKeeper *znode*, etcd *key*, REST *resource*).
- **No `clear:`, no tombstone** — stopped state is written explicitly; removal is a
  `do/` action (§3).
