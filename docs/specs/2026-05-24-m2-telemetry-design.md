# Design: Porta M2 — telemetry (`data_log`) + `gateway monitor`

**Date:** 2026-05-24
**Status:** Draft (brainstorming complete; pending user review → implementation plan)
**Builds on:** the M1 command-queue control plane
(`docs/specs/2026-05-23-porta-toit-gateway-design.md`, SHIPPED @101bb3e). M1 ships
a per-node FIFO command queue, payload BLOBs, and a once-per-wake **report** channel
(`reports` table: `observed_state` + `health`). M2 extends that report channel with
**richer telemetry**: a device→gateway time-series (`data_log`) and a CLI reader
(`gateway monitor`).

**Sibling specs (decided during this brainstorm, written separately):**
- **Node lifecycle & reliability** — power modes (deep-sleep | always-on), the
  supervisor loop structure for each, the matching watchdog flavor, and call
  timeouts. M2's telemetry path is **power-mode-agnostic** by construction (see
  Invariant below); the lifecycle spec owns the modes themselves.
- **Security & transport evolution** — payload-level, transport-agnostic security
  (sign image + reports), and why channel-level DTLS/CoAP is a poor fit for a
  deep-sleeping, multi-transport fleet.
- **POC sample-node network** — 3 on-disk sample projects (incl. a control loop)
  rewritten on this infrastructure. Scoped later; consumes M2 + the down-path.

## Context & strategic framing

The legacy Go gateway had a `data_log` table (`id, eui64, timestamp, payload BLOB`):
an append-only, time-range-queryable, age-pruned stream of **device-emitted data**,
surfaced via MCP `query_data` / `get_console`. There was **never a live console
tunnel** — `get_console` just returns the last N `data_log` rows, and the device
PUT its console/print output into `data_log`. (SLIP in st-zephyr was *serial-link
framing* for the host↔dongle transport, not a console tunnel, and never rode the
UDP link — see `st-zephyr/.../2026-04-02-jast-udp-transport-design.md`.)

M2 brings that store-and-forward data channel to the Toit nodes. The deep-sleep
model makes store-and-forward the *only* sensible shape: a node captures data while
awake, buffers it, and ships it at its next gateway contact. Nothing is captured
during deep-sleep — there is nothing running to capture.

## Goals

1. A device→gateway **telemetry time-series** (`data_log`) carrying two kinds of
   data: **console** (unstructured `log` lines) and **structured readings**
   (`metric` name+value), unified in one channel.
2. A device-side **remoting container** that collects telemetry via Toit services
   and is drained by the supervisor at report time.
3. A `gateway monitor` CLI — the `get_console`/`query_data` successor: range query +
   optional follow.
4. **Off by default** (matches M1's deliberate "no per-app logs"), command-toggled.
5. Hardware-verified on node `fwkb`.
6. Design (not build) the symmetric **down-path** (setpoints / remote config) so the
   service interfaces line up; implement it as a fast follow (M2.2).

## Non-goals (this milestone)

- **Power modes / watchdog / supervisor loop restructuring** — sibling spec.
- **Security** of the telemetry/command channels — sibling spec; LAN is trusted now.
- **Live console attach** — incompatible with deep-sleep; never existed even in the
  legacy always-on gateway.
- **On-device aggregation as a platform feature** — the *app* reduces (avg/min/max/
  downsample) and calls `report` at report-rate; `TelemetryService` records what it
  is given. (See lifecycle spec — always-on nodes sample ≫ report-rate.)

## Key decisions (locked during brainstorming)

1. **Remoting container, services only.** One new container (started first, lowest
   runlevel) provides `PrintService` (or `LogService`), `TelemetryService`, and
   `ControlService` (down-path), plus a RAM ring buffer + a `drain` the supervisor
   calls. Consolidated into **one** container — the "light but not free" cost is
   per-container, not per-service.
2. **Transport stays in the supervisor.** The TFTP/`GatewayClient` wire I/O,
   command-apply, reconcile, and deep-sleep timing remain in the (M1-verified)
   supervisor. The remoting container holds **no socket** — so no image-across-the-
   process-boundary problem, and no M1 refactor.
3. **`GatewayClient` is *the* transport seam.** Swapping TFTP → CoAP/ESPnow later is
   "write a new `GatewayClient` impl," independent of container topology. M2 must not
   leak TFTP specifics into supervisor or remoting logic. (Detail: the "zero-byte
   body = drained" sentinel is TFTP-native; make it an explicit interface contract
   when the seam is hardened.)
4. **Separate `data?id=` WRQ; the report stays lean.** Telemetry does **not** bloat
   the M1 `report` body. After reporting, if the drained buffer is non-empty *and*
   `console-forward` is on, the supervisor PUTs a `data?id=<mac>` WRQ.
5. **JSONL on the wire**, not a JSON array — one entry per line. Bounds peak decode
   memory on both ends (device encodes/writes one entry at a time; gateway decodes +
   `INSERT`s one line at a time) and is truncation-tolerant (every complete line is
   independently valid; an append-only log is the right model).
6. **Two flags, both off by default:** `console-forward` (ship console to gateway,
   command-toggled) and `uart-echo` (re-forward to UART so `jag monitor` still works
   on the bench — a device-side default-off constant; no CLI in M2).
7. **RAM-only buffer, one wake window.** The supervisor always drains + ships before
   sleep, so nothing must survive deep-sleep. Durable-across-failed-ship buffering is
   a noted enhancement, not M2.
8. **`TelemetryService` is dumb.** It records readings as given. Aggregation is the
   app's responsibility. The ring buffer (drop-oldest + a dropped-count marker) is
   the backstop against a chatty/misbehaving app.
9. **Down-path reuses the command queue.** A new `set <app> <key>=<value>` verb,
   delivered/audited exactly like `run`/`stop`. The node persists it to a per-app NVS
   config store and serves it via `ControlService`. No new gateway table. **Designed
   here, built in M2.2.**

### Invariant: the telemetry path is power-mode-agnostic

The remoting container collects and is `drain`ed; the `data?id=` WRQ flushes on each
poll. Neither cares whether the node deep-sleeps or stays awake afterward. Deep-sleep
vs always-on differs *only* in what the supervisor does **between** poll cycles
(sibling spec) — the cycle itself (drain commands → reconcile → flush telemetry →
report) is identical. M2 therefore special-cases **nothing** for always-on.

## Architecture

```
device/
  supervisor.toit     (M1) lifecycle + transport + command-apply + reconcile + sleep
                      (M2) calls remoting.drain after report; ships data?id= WRQ;
                           applies set-console flag; (M2.2) writes per-app config
  remoting/           NEW container — services only, no socket
    remoting.toit       entry: install providers; run; expose drain to supervisor
    print_sink.toit     PrintService/LogService provider → ring buffer (kind=log)
    telemetry.toit      TelemetryService provider (report name/value) → buffer (metric)
    control.toit        ControlService provider (get key) ← per-app NVS config  (M2.2)
    buffer.toit         bounded ring buffer (drop-oldest + dropped-count), drain -> List

gateway/
  store.toit          + data_log table; insert_data (per-line), query_data, prune_data
  handler.toit        + data?id= WRQ ingest: parse JSONL line-by-line → store.insert_data
  gateway.toit        + monitor verb; + device set-console; (M2.2) device set/get
```

### Device-side data flow (per wake)

```
boot/wake → supervisor starts remoting container (runlevel 0) → starts payload apps (≥1)
   apps print / report ───────────────►  remoting ring buffer (log + metric entries)
   gateway delivers `set app k=v` ─────►  supervisor → per-app NVS config → ControlService → app   (M2.2)
   before sleep: supervisor calls remoting.drain → JSONL `data?id=` WRQ → (deep-sleep | stay awake)
```

The remoting container is a fresh process each wake (deep-sleep wipes RAM); its
buffer only ever holds the current wake window's data, which the supervisor ships at
the end of the window. Coherent and simple.

### Why the `drain` boundary is cheap

Inter-container service calls in Toit are VM-scheduled (cooperative), not FreeRTOS
task switches; coarse-grained calls (per-reading, one `drain` per wake, per-command)
cost microseconds. The one path to keep *off* the boundary is large image payloads —
which is exactly why transport (and image streaming/install) stays in the supervisor.
(`PrintService`/`LogService.log` is already an RPC today, so console capture adds no
new hop — only changes which process answers.)

## sqlite schema (M2)

```sql
CREATE TABLE IF NOT EXISTS data_log (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  device_id  TEXT,
  ts         INTEGER,           -- epoch seconds (gateway receive time; device may carry its own ts in-body)
  seq        INTEGER,           -- device-side ordering within a batch
  kind       TEXT,              -- 'log' | 'metric'
  name       TEXT,              -- metric name (NULL for log)
  value      NUMERIC,           -- numeric/bool metric value (NULL for log or string metric)
  text       TEXT,              -- console line, OR a string-valued metric (NULL otherwise)
  value_type TEXT               -- 'int'|'float'|'bool'|'string' for metrics; NULL for log
);
CREATE INDEX IF NOT EXISTS idx_data_device_ts ON data_log(device_id, ts);
```

Pruned by age (`prune_data maxAge` — mirrors the Go gw's `PruneData`).

### Addendum (2026-05-24): typed metric values

The original design typed a metric `value` as a single `REAL`. During M2 implementation
this was generalized so a metric value can be an **int, float, bool, or string**,
preserved end-to-end:

- `TelemetryService.report name value` accepts any scalar (no `/float` constraint);
  the device ships it verbatim in the JSONL body (JSON already distinguishes the types).
- The gateway infers `value_type` from the **decoded JSON runtime type** — no explicit
  wire tag — and stores: numeric → `value` (NUMERIC, so an int stays an int); bool →
  `value` 0/1 + `value_type='bool'`; string → reused `text` column + `value_type='string'`.
  A non-scalar value (JSON array/object) degrades gracefully to `value`/`value_type` NULL.
- `gateway monitor` renders by `value_type` (`pm=13`, `pm=13.0`, `door=true`, `mode=auto`).

Verified on host: int/float/bool/string survive the device RPC and the JSONL
round-trip. Note one storage subtlety: SQLite NUMERIC affinity stores a
whole-number float (e.g. 13.0) as an integer storage class, so query-data returns
int 13 — but value_type stays "float", and `gateway monitor` renders by value_type,
so a float metric always displays with its decimal point (pm=13.0).

## Wire protocol

Unchanged M1 steps 1–3 (drain commands, fetch payloads, apply+reconcile), then:

4. **Report** — unchanged M1 WRQ `report?id=<mac>` (apps@crc + health). Lean.
5. **Telemetry (new, conditional)** — if `console-forward` is on and the drained
   buffer is non-empty, WRQ **`data?id=<mac>`**, body = **JSONL**:
   ```
   {"ts":1716500000,"seq":0,"kind":"metric","name":"pm","value":13}
   {"ts":1716500001,"seq":1,"kind":"log","text":"supervisor: started blink"}
   ```
   Gateway parses line-by-line, `INSERT`ing each into `data_log` (bounded decode).
   Truncated tail → only the last partial line is lost.
6. **Sleep | stay awake** — per the node's power mode (sibling spec).

## CLI surface (M2)

| Command | Effect |
|---|---|
| `gateway monitor -d <node> [--since <dur>] [--follow] [--kind log\|metric]` | the `get_console`/`query_data` successor: print `data_log` rows for the node/window (`ts  kind  name=value \| text`); `--follow` polls the store and tails new rows as wakes deliver them (no live device pipe — consistent with deep-sleep) |
| `gateway device set-console -d <node> on\|off` | enqueue the `console-forward` flag (command-delivered like everything else) |

**Down-path CLI (design only — M2.2):**

| Command | Effect |
|---|---|
| `gateway device set -d <node> <app> <key> <value>` | enqueue `set app key=value` |
| `gateway device get -d <node> <app> [<key>]` | show desired (from command log) and/or observed (from report) config |

## Testing strategy

- **Host TDD:** `data_log` store (insert / query-by-range / prune-by-age); JSONL
  parse→insert incl. truncated-tail tolerance; `monitor` formatting; `set-console` /
  (M2.2) `set` command encode. Run under `toit-sqlite` on host (as M1 store/command).
- **Integration (host):** a fake node PUTs a `data?id=` JSONL body → assert
  `data_log` rows; `monitor` reads them back; assert default-off ships **nothing**.
- **Hardware (`fwkb`):** run the M2.0 spike first; then a payload that `print`s +
  `report`s, flip `set-console on`, verify `data_log` fills and `monitor --follow`
  tails; verify quiet by default.

## Hardware verification result (2026-05-24, node `fwkb` / `30aea41a6208`)

M2.1 **hardware-verified on `fwkb`**. Built the supervisor into a no-jaguar envelope
(`host/build-envelope.sh`), flashed, and drove it from the host gateway daemon
(`serve --port=6969`, db `/tmp/porta-m2.db`):

- Firmware boots, the spawned telemetry provider registers (`supervisor: telemetry
  provider registered`), node polls and reports.
- Commands deliver + apply (`set-poll-interval`, `set-console`, `run chatty`).
- `chatty` (test payload) emits `log` + typed `metric`s → provider buffer → supervisor
  drains after the observe window → ships a `data?id=` JSONL WRQ → gateway ingests →
  `gateway monitor` reads it back. **All scalar types rendered correctly:**
  `boot=true` (bool), `mode=blink` (string), `counter=0..4` (int), `load=0.0..6.0`
  (float, incl. whole-number `0.0`), and `chatty: tick N` (log).
- **Default-off invariant confirmed:** with `set-console off`, chatty still runs but the
  supervisor ships nothing — the `data_log` row count held steady. The M1 path is
  unchanged when telemetry is off.

The **explicit `TelemetryServiceClient.log` path** (the M2.0 fallback) is the one used and
is now hardware-proven; the print-interception spike was not needed.

**Caveat (transport, not M2) — RESOLVED 2026-05-24:** the `tftp#5` TID-race (davidg238/tftp#5)
originally bit the initial 38 KB `chatty.bin` payload fetch repeatedly (once a hard hang needing a
power-cycle), forcing a `>= 30s` poll-interval workaround. The fix (fresh UDP socket per transfer /
drain stale datagrams between exchanges) is now **hardware-verified on `fwkd`**: a supervisor envelope
rebuilt against the fixed lib fetched the 38 KB multiblock `chatty.bin` cleanly on first try — no
block-1 timeout, no retries, no race — and the telemetry up-path re-verified on a fresh db. The
poll-interval floor is lifted (sub-30s dev loops are fine again). tftpClaude is committing the fix and
bumping the tftp version.

## Milestones (within M2)

- **M2.0 — spike (first, gating):** can a non-system container register `PrintService`
  and capture *other* containers' `print` on `fwkb`?
  **Fallback (already proven on hardware):** vindriktning's `LogService` pattern —
  apps call an explicit `TelemetryService.log msg` (one-line app change), drop
  `print`-interception. Bounds the spike's downside.
- **M2.1 — build:** remoting container (Print/Log + Telemetry providers + ring buffer
  + `drain`) → supervisor drains → ships `data?id=` JSONL WRQ; `console-forward`
  flag; `data_log` schema + JSONL ingest; `gateway monitor`. **Hardware-verified.**
- **M2.2 — fast follow (down-path):** `ControlService` + per-app NVS config store +
  `set` command verb + `set`/`get` CLI. Symmetric write-side of the data plane.

## Risks & open questions

- **PrintService displacement (M2.0)** — unproven that a non-system container can
  intercept other containers' `print`. Mitigated by the `LogService` fallback.
- **Cadence coupling** — telemetry flushes on the command-poll. An always-on node may
  want to push telemetry faster than it polls for commands. Simplest: one shared
  cadence; decouple later if needed. (Interacts with the lifecycle spec.)
- **Gateway data_log growth** — always-on + continuous sampling can fill `data_log`
  fast; age-pruning + app-side aggregation are the controls.
- **`ts` source** — gateway-receive time vs device-carried timestamp. Devices may
  lack synced wall-clock across deep-sleep; M2 records gateway-receive `ts` and
  optionally carries a device monotonic/seq in-body. (NTP-on-wake is a node concern.)
```
