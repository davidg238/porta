# `profile` verb — remote target-execution profiling — design

Date: 2026-06-26
Status: approved (brainstorm)
Scope: porta-side, language-neutral. The node-side profiler wiring and the
blob decoder are a documented handoff to each node repo (`nodus`, future
`nodus-st`), not part of this spec.

## Problem

porta can command, debug, and ingest telemetry from a fleet of heterogeneous
nodes, but there is no way to **profile a target app's execution**. The Toit
runtime (and the future Smalltalk/Newspeak runtimes) each expose a profiler, but
porta hosts zero language toolchain and must not learn any single runtime's
profiler model. We want an operator to arm profiling for a named app on a node,
have the node run one profiling session and ship the result, and let a node-side
dev tool decode it — reusing the patterns already established by the `debug` verb
(§2.9) and `kind:"panic"` telemetry (§6).

How Toit profiles (background, informative): `Profiler` in the SDK
(`lib/core/utils.toit`) is a bytecode-invocation-count profiler —
`install(all-tasks)` / `start` / `stop` / `report(--cutoff)`. `report` ships the
encoded result on the **trace-message channel**, byte-for-byte the same delivery
path a panic stack trace uses. The encoded blob is a method-index→hit-count
histogram that is meaningless without the app's snapshot to symbolicate it.
Therefore a profile result is structurally a sibling of a panic blob: an opaque,
node-kind-defined artifact whose decode lives in the node's dev tooling.

## In scope (porta)

1. A `profile` command verb in the vocabulary (`internal/command`) + PROTOCOL.md.
2. A WRQ-only `profile?id=<mac>` TFTP resource that ingests the opaque blob.
3. Append-only `profile_result` storage with stable per-session identity.
4. Operator surface: `apisrv` endpoints, `portacli` `porta profile` subcommands,
   and a web node-detail **Profiles** panel that lists blobs raw with a decode hint.
5. Tests (TDD), following the existing `debug`/`data` patterns.

## Out of scope

- The node-side profiler session implementation (relaunch-under-profiler,
  arm/ship/disarm) — node repos; see Handoff.
- The blob **decoder** / symbolication (`nodus profile`, future `nodus-st profile`)
  — node repos. porta never parses the blob.
- Any Toit-specific profiler tuning on the wire (`all-tasks`, `cutoff` stay
  node-internal — see Neutrality invariant).
- Wall-clock vs bytecode-count sampling semantics — a node-runtime concern.

## Design

### 1. Wire protocol (PROTOCOL.md — new §2.x verb + §1/§7 resource)

**`profile` command verb** — declarative, last-write-wins, stateful-in-node,
mirroring `debug` (§2.9). While a profile session is armed for `name`, a plain
`run` for `name` is held back (the profiler owns the app), exactly as `debug`
does.

| Key | Type | Req | Meaning |
|-----|------|-----|---------|
| `verb` | string | yes | `"profile"` |
| `name` | string | yes | App to profile (must already be installed) |
| `action` | string | yes | `"start"` — arm a one-shot session · `"stop"` — disarm early |
| `duration_s` | int | no | Run-loop auto-stop bound. **Default 30.** Ignored by deep-sleep nodes (the session is bounded by the wake's single execution). |
| `continuous` | bool | no | Default `false`. `true` re-arms each cycle until an explicit `stop`; each cycle ships its own result. |

```json
{"verb": "profile", "name": "myapp", "action": "start", "duration_s": 30}
{"verb": "profile", "name": "myapp", "action": "stop"}
```

**Termination model — one-shot + early stop.** `start` arms a *single* profiling
run; the node auto-disarms after shipping one result. `stop` cancels an
armed/running session early. Whether an early `stop` ships the partial result
collected so far or no result at all is **node-defined** (porta accepts a blob if
one arrives and accepts none if not). `continuous:true` is the only way a session
spans more than one run.

**`profile?id=<mac>` resource — WRQ only.** The node uploads the **raw encoded
profile blob** (no base64: unlike `kind:"panic"`, which base64s because it rides
JSON telemetry, this is a dedicated binary resource). There is **no RRQ side** —
the session configuration travels entirely on the `profile` command verb, so the
resource is upload-only, like `report` and `data`. Handler changes:
`profile?id=` is added to `AcceptWrite`'s allowlist and a `case "profile"` is
added to `Write`. No `Read`/`Complete` changes.

### 2. Node behavior (informative — implemented per node repo)

- **Run-loop / always-on:** on `start`, (re)launch `name` with its runtime's
  profiler active; after `duration_s` (or app exit) encode the profiler result
  and WRQ it to `profile?id=<mac>`; disarm unless `continuous`.
- **Deep-sleep:** `start` sets the profile goal; the *next wake* runs that one
  execution under the profiler, ships the blob, and disarms (`continuous` re-arms
  for subsequent wakes).
- Holds back a plain `run` for `name` while armed (same rule as `debug`).

### 3. Sessions, identity, and storage

Append-only **`profile_result`** table — one row per shipped session, mirroring
`debug_response`:

| Column | Meaning |
|--------|---------|
| `seq` | Per-node monotonic session id (the selection key) |
| `node_id` | Node MAC |
| `ts` | Arrival time (epoch s) |
| `app` | Profiled app name |
| `label` | Optional operator label (porta-side only; see below) |
| `blob` | Raw encoded profiler artifact (bytes) |
| `byte_len` | `len(blob)`, for the list view without reading the blob |

**Session selection.** Operators choose a session by `seq`/`ts`/`app`:
`porta profile poll` lists them; `porta profile get <node> <seq>` fetches one
blob. `continuous` emits one row (one `seq`) per cycle — the before/after
comparison story.

**Correlation.** The profile goal is single-in-flight per node (last-write-wins),
so porta tags each arriving blob with the node's *current* profile goal (`app`,
`label`) at ingest. No session token is carried on the wire.

**Operator label (porta-side only).** `profile start` accepts an optional
`--label` (e.g. `before-fix`). It is stored on the session and joined onto the
result row; it **never goes on the wire**, preserving language-neutrality. It
exists purely to name sessions for human comparison.

### 4. Operator surface

- **API** (`apisrv`): `POST /api/nodes/{sel}/profile` (body: `{action,name,duration_s,continuous,label}`)
  enqueues the verb; `GET /api/nodes/{sel}/profile` lists result rows
  (blob as base64 in JSON); `GET /api/nodes/{sel}/profile/{seq}` fetches one.
- **CLI** (`portacli`): `porta profile start <node> <app> [--duration 30s] [--continuous] [--label L]`
  · `porta profile stop <node> <app>` · `porta profile poll <node>` (list)
  · `porta profile get <node> <seq>` (fetch raw blob to stdout/file).
- **Web** (`web`): a **Profiles** panel on node detail listing result rows
  (`seq · ts · app · label · bytes`) with a `[decode ↗]` hint per row pointing at
  the node's dev tool — the same affordance pattern as the panic decode link.
  porta renders the blob raw; it performs **no decode**.

### 5. Neutrality invariant (first-class)

The profile blob is **opaque and node-kind-defined**. porta stores and serves it
verbatim and never parses it. The decoder is selected by the node's existing
`kind` column (the polyglot seam): `nodus profile` for Toit, `nodus-st profile`
for Smalltalk, etc. Because no runtime-specific profiler tuning is on the wire,
each runtime applies its own model internally. This is the same posture porta
already takes toward `kind:"panic"` blobs, and it is what makes the feature work
unchanged for Toit, Smalltalk, and Newspeak nodes.

### 6. Testing (TDD)

Following the `debug`/`data` test patterns:
- `command`: `Profile(...)` encode + arg validation (action enum, duration/label).
- `handler`: `profile?id=` accepted by `AcceptWrite`; `Write` ingests a blob into
  `profile_result`; correlation tags `app`/`label` from the current goal.
- `store`: append-only round-trip, per-node `seq` monotonicity, list + get-by-seq.
- `apisrv`: start/stop enqueue; list/fetch (base64) round-trip.
- `portacli`: subcommand wiring.
- `web`: Profiles panel renders rows + `[decode ↗]` hint; no decode in porta.

## Handoff (node repos — not this spec)

Each node repo implements: the `profile` verb (arm/relaunch/one-shot/disarm per
its power mode), encoding its profiler artifact, the `profile?id=` WRQ upload, and
a `profile` dev-tool subcommand that symbolicates the blob against the app
snapshot. The normative blob format is node-kind-defined and documented in the
node repo, exactly like the panic-reporting contract.
