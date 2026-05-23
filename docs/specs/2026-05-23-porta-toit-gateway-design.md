# Design: Porta Toit gateway (command-queue control plane + sqlite)

**Date:** 2026-05-23
**Status:** Draft (brainstorming complete; pending user review → implementation plan)
**Builds on:** the no-jaguar supervisor node runtime
(`docs/specs/2026-05-22-porta-no-jaguar-supervisor-design.md`, SHIPPED). That
proved the on-device side: a supervisor that holds a persistent goal-state in
NVS, reconciles it, installs/starts containers, and deep-sleep-cycles. This
design builds the **gateway** the nodes poll, in Toit, replacing the throwaway
`host/serve.toit`.

## Context & strategic framing

Porta is a **LAN gateway** — an on-premises hub that delivers executable code and
control to local nodes over UDP/TFTP (ESPnow/Thread later), backed by a local
sqlite store. It is **not** a WAN fleet manager. **Artemis is the WAN tier**
(cloud broker, org/auth, pods, diff-OTA, signing); Porta sits below it on the
LAN. We therefore **borrow Artemis's vocabulary and `jag`'s CLI grammar** but
drop all WAN machinery.

The existing Go gateway (`gateway/`, UDP 5683) implements an older imperative
**command-queue** protocol for the **smalltalk/jast nodes** (`/commands` pop +
`/results` PUT + `/debug`), plus a TCP CLI, an MCP/SSE server, and a debug
manager, all over a sqlite store. This design **brings that command-queue
concept to the Toit nodes** — faithful to "copy the old gateway's concepts" —
while leaving smalltalk-node support as future work.

### The model: commands mutate node-local goal state (CQRS over a sleeping fleet)

The decisive design conversation settled this shape:

- A **command** is the auditable mutation verb — the unit of operator intent
  (`run <blink> interval 30s`, `stop <blink>`, `set-poll-interval 1s`). The CLI
  issues commands **to the gateway**, targeted at a **specific node**, and the
  gateway **queues** them (FIFO, in sqlite) until that node next connects.
- A node's **goal state lives on the node** (the existing NVS `Inventory`). The
  gateway holds **no materialized goal**. Each command a node pulls **executes on
  the node and mutates its own goal state**.
- Nodes are **usually asleep**. A wake cycle is: connect → **drain the command
  queue until exhausted**, applying each command (pulling payload images as
  needed) → reconcile (install/start/stop) → **report actual state** → sleep.
- Poll cadence is **command-configurable**: drop to ~1 s for responsiveness while
  actively programming a node, long in production.

## Goals

1. A Toit gateway daemon that serves the Toit/supervisor nodes a **per-node
   command queue** + **payload images**, backed by **sqlite**, replacing
   `host/serve.toit`.
2. A **`jag`-aligned CLI** (same `pkg-cli` library Artemis/jag use) that issues
   commands, registers payloads, and inspects nodes and the audit log.
3. **Auditability**: every command is recorded (issued / delivered), and every
   node reports its **observed state** each wake, giving an execution-truth audit
   trail and self-healing convergence.
4. Hardware-verified end-to-end on node `fwkb`.

## Non-goals (this sub-project)

- **Smalltalk/jast node support** — their specific verbs, the debug protocol,
  `run_st`, sourcemaps. The Go gateway stays as `gateway-go/` for that.
- **WAN/cloud machinery** from Artemis: `org`, `auth`/`login`, `broker`,
  `fleet init`, cloud device-identity provisioning, `pod upload`/`download`.
- **Source compilation** in the gateway (M1 takes prebuilt images; deferred).
- Groups, signing, diff-OTA.

## Key decisions (locked during brainstorming)

1. **Repo:** rename the existing Go gateway `gateway/` → **`gateway-go/`**
   (legacy). The new Toit gateway is **`gateway/`**.
2. **Nodes supported:** Toit/supervisor nodes only. Smalltalk nodes deferred.
3. **State:** sqlite (host-only `~/workspaceToit/sqlite` package / `toit-sqlite`).
4. **CLI grammar:** mirror `jag` (`scan`, `ping`, `container`, `-d/--device`,
   `--interval 30s`) and borrow Artemis *concepts* (`device show`/`status`,
   `set-max-offline`, duration types, `Example`s). Drop WAN verbs.
5. **Node identity:** the ESP32 **base MAC as hex**, sent as `?id=<mac>` in TFTP
   request filenames (mirrors the Go gw's `?id=<eui64>`).
6. **Control model:** per-node FIFO **command queue** on the gateway; **node-local
   goal state**; commands mutate it (option from the design dialogue).
7. **Commands are declarative & absolute** so they are **idempotent** and safe to
   redeliver/re-issue: `run blink@crc-Y interval 30s`, never relative
   (`bump interval +10s`).
8. **Delivery semantics (option C):** a command is marked **delivered** on **TFTP
   transfer-complete** (the block engine raises the event — TFTP ACKs prove
   *delivery* to the node's stack, not execution). **No per-command app-ack.**
   Execution truth comes from the node's **state report** each wake, which the
   gateway uses for audit *and* to detect a command that didn't take and re-issue.
9. **Payloads** are stored as **BLOBs in sqlite** (image + crc + size), keyed by
   crc; a node pulls `payload` (by name+crc) only when a `run` references an image
   it lacks.
10. **`container install` takes a prebuilt image** in M1 (the artifact the loader
    spike already builds); source-compile is later.
11. **TFTP refactor** (dependency, dispatched to a separate agent): split
    `~/workspaceToit/tftp` into block-engine / transport / request-handler; keep
    the device `TFTPClient` wire- and API-compatible (it is hardware-verified).

## Architecture

```
porta/
  gateway-go/      Go, legacy (smalltalk/jast nodes, MCP, debug) — untouched
  gateway/         NEW Toit gateway package (this design)
    gateway.toit       CLI entrypoint (pkg-cli): serve + admin verbs
    serve.toit         the daemon: TFTP-over-UDP handler wired to the store
    store.toit         sqlite access (nodes, payloads, command_queue, reports)
    command.toit       command model + encode/decode (wire + audit)
    handler.toit       RequestHandler impl: ?id parse, queue drain, payload, report
  device/          supervisor (extended: drain+apply+report; configurable poll)
  host/            serve.toit retired once M1 is hardware-verified
```

Three layers, three responsibilities:

- **`~/workspaceToit/tftp` (refactored, Spec A):** a transport-agnostic **block
  engine** (RFC-1350 DATA/ACK/retransmit + `blksize`/`tsize` negotiation, raising
  a **transfer-complete** event), a **UDP transport** owning the socket and
  per-peer state, and a **RequestHandler** interface. `FilesystemStorage` becomes
  one handler impl.
- **`gateway/handler.toit`:** a `StoreBackedHandler` that parses `?id=<mac>`,
  upserts/auto-names the node, and answers requests from the store (commands,
  payloads, report ingest).
- **`gateway/store.toit`:** all sqlite reads/writes; the single source of truth
  for both the daemon and the CLI (shared DB file, WAL).

## Wire protocol (per wake)

Resource names carry the node id as a query suffix the handler parses
(`stripQuery` + `extractDeviceID`, as the Go gw does):

1. **Drain commands.** Node issues RRQ `commands?id=<mac>` repeatedly. Each
   response is the **next queued command** (encoded; see `command.toit`) or an
   **empty/sentinel** body meaning "queue exhausted" → node stops draining. The
   gateway marks each command **delivered** when its RRQ **transfer completes**.
2. **Fetch payloads on demand.** When an applied `run <name>@crc` references an
   image the node lacks, node streams RRQ `payload?id=<mac>&name=<n>&crc=<c>` into
   the existing `ImageStreamWriter`/`flash-image` install path.
3. **Apply + reconcile.** Each command mutates the node's NVS goal state; the node
   reconciles (install/start/stop) using the supervisor machinery already shipped.
4. **Report.** Before sleeping, node issues WRQ `report?id=<mac>` with a compact
   **observed-state** body: the apps it is now running (name@crc, runlevel,
   triggers) + health/heartbeat. Gateway stores it (audit + convergence).
5. **Sleep** until the next wake (poll cadence per the node's current
   `set-poll-interval`).

Idempotency makes redelivery safe: re-applying `run blink@crc-Y` is a no-op when
the node is already at that state.

## Node identity

ESP32 base MAC, lowercase hex, no separators (e.g. `a0b1c2d3e4f5`). The exact SDK
API (`esp32` module) is resolved during planning. The MAC is stable across
deep-sleep and reflash, so it is a sound primary key.

## sqlite schema (M1)

- **`nodes`** — `id TEXT PK` (MAC), `name` (auto-assigned on first poll, jag-style
  word list; operator-overridable), `source_addr`, `first_seen`, `last_seen`,
  `poll_interval_s`, `max_offline_s`, `last_report_at`, `observed_state` (JSON:
  apps@crc + health from the latest report).
- **`payloads`** — `crc INTEGER PK` (image identity), `name`, `size`, `image BLOB`.
  (A name may have many crcs over time; crc is the identity used on the wire.)
- **`command_queue`** — `id INTEGER PK`, `device_id`, `seq`, `verb`, `args` (JSON),
  `issued_at`, `issued_by`, `delivered_at` (NULL until transfer-complete). FIFO
  per device by `(device_id, id)`; **never deleted on delivery** (it is the audit
  log) — pruned by age only.
- **`reports`** — `id INTEGER PK`, `device_id`, `ts`, `observed_state` (JSON),
  `health`. Append-only audit of execution truth. (M2 telemetry extends this.)

Deferred tables (smalltalk-era / later milestones): `data_log` time-series,
`debug_*`.

## Command vocabulary (M1)

All declarative/absolute:

- `run <name> --crc <n> --interval <dur> [--runlevel <n>] [--triggers …]` — node
  should run app `name` from image `crc` on the given schedule.
- `stop <name>` — node should not run `name`.
- `set-poll-interval <dur>` — change the node's wake/poll cadence.

`identify` / `reboot` (genuinely edge-triggered) are deferred; under this model
they would be transient, non-idempotent commands and are not needed for M1.

## CLI surface (M1) — `jag`-aligned, Artemis concepts

`pkg-cli` (`Command`/`Option`/`Flag`/`--short-name`/`--type="duration"`/`Example`),
`-d/--device` selects by **name or id (MAC)**:

| Command | Effect | Lineage |
|---|---|---|
| `gateway serve [--db P] [--port 6969]` | the daemon (TFTP/UDP, store-backed) | (gw-specific) |
| `gateway scan [--include-never-seen]` | list nodes; health via `max-offline` | jag scan + artemis status |
| `gateway ping -d <node>` | recently-seen check | jag |
| `gateway device show -d <node>` (alias `status`) | last-seen, observed goal-state, queued/undelivered commands, poll interval | artemis |
| `gateway device set-max-offline -d <node> <dur>` | offline threshold (config row) | artemis |
| `gateway device set-poll-interval -d <node> <dur>` | enqueue `set-poll-interval` | (this model) |
| `gateway device name -d <id> <name>` | override auto-name | jag auto-names |
| `gateway container install <name> <file> -d <node> --interval <dur> [--crc N]` | register prebuilt image + enqueue `run` | jag |
| `gateway container uninstall <name> -d <node>` | enqueue `stop` | jag |
| `gateway container list -d <node>` | node's goal apps from latest report (DEVICE/IMAGE/NAME) | jag |
| `gateway log -d <node>` | the auditable command history (issued/delivered) | (audit) |

(`container install`/`uninstall` map to the `run`/`stop` command verbs; `run`/
`stop` may be exposed as aliases.)

## Device-side changes (supervisor)

The shipped supervisor's `GET goal` → `reconcile(goal)` is replaced by:

1. Send `?id=<mac>` on every request.
2. **Drain** `commands?id=` until the empty sentinel; **apply** each to the NVS
   `Inventory` (the goal state). `goal_state.toit`'s JSON parse is repurposed into
   per-command apply; `inventory.reconcile` and the install/start machinery are
   reused unchanged.
3. Pull `payload?id=&name=&crc=` on demand for missing images.
4. PUT `report?id=` with the observed state before sleeping.
5. Honor a node-local **poll interval** set by `set-poll-interval` (persisted like
   `schedule_store`), replacing the hardcoded `POLL-PERIOD`.

This requires a re-flash of `fwkb` and a fresh hardware verification.

## Dependency — Spec A: TFTP engine/transport/handler split

Written as its own agent-facing spec and **dispatched to a separate agent**.
Refactor `~/workspaceToit/tftp`:

- **Block engine** — pure RFC-1350 state machine; input parsed packets + a data
  source/sink; output packets; **raises a transfer-complete event** (needed for
  delivery marking). No socket.
- **UDP transport** — owns the socket + per-peer transfer state; pumps packets to
  the engine; sends replies.
- **RequestHandler** — `(op, resource, raw-path-with-query, peer) → reader | sink`.
  `FilesystemStorage` reimplemented on top as one handler.

**Hard constraint:** the on-device `TFTPClient` API and wire behavior are
hardware-verified and must not change. Back-compat tests (device client +
`host/serve.toit`) gate the refactor.

## Milestones

- **M1** — command-queue control plane: `gateway/` package (store, serve, handler,
  command, CLI), per-node FIFO queue + payload BLOBs + report ingest, delivery
  marked on transfer-complete, jag-aligned CLI. Supervisor extended to
  drain/apply/report + configurable poll. Replaces `host/serve.toit`.
  **Hardware-verified on `fwkb`.** Depends on Spec A.
- **M2** — richer **telemetry** (`data_log` time-series) + `gateway monitor`,
  extending the M1 report channel.
- **M3** — **pods**: `.pod` (`ar`-archive) ingestion — gateway extracts container
  images + device-config and turns them into payloads + `run` commands.
- **M4** — **MCP/SSE** server over `pkg-http` wrapping the same store/command ops
  (read tools first: `list_nodes`, `device_status`; then `run`/`stop`/
  `register_payload`).
- **Deferred:** full smalltalk-node support (verbs/debug/`run_st`), groups,
  signing, diff-OTA, source compilation in the gateway.

## Testing strategy

- **Host TDD** for `store.toit`, `command.toit` (encode/decode + apply
  idempotency), and CLI parsing — run under `toit-sqlite` on host. Mirrors how the
  supervisor's host-side modules were TDD'd.
- **Integration** on host: a fake node draining a seeded queue, pulling a payload,
  and reporting; assert delivered/observed rows.
- **Hardware**: reflash `fwkb`, drive a full programming session at ~1 s poll
  (`container install` → node installs/starts → report shows app@crc), then a
  production-cadence deep-sleep cycle. Verify the audit log and convergence
  (re-issue on a missed command).

## Risks & open questions

- **Empty-queue sentinel**: define the "queue exhausted" signal cleanly over TFTP
  (empty body vs explicit marker) so draining terminates unambiguously.
- **MCP-in-Toit (M4)** is the largest unknown (SSE + JSON-RPC over `pkg-http`); it
  is intentionally last.
- **Spec A sequencing** (write+dispatch first, vs author both specs and build the
  TFTP-free store/CLI in parallel) — to be decided at planning time.
- **Report size**: keep `observed_state` compact (name@crc + counts), not full
  per-app detail, to bound the WRQ.
- **Package layout**: `gateway/` as a single package vs splitting host-only store
  from wire code — settle when the file set is concrete.

## Decomposition

Two specs: **Spec A** (TFTP split, agent) and **Spec B** (this gateway design),
each → its own implementation plan. M1 of Spec B is the first plan to execute,
once Spec A's seams exist (or in parallel against the pinned seam API).
