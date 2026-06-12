# Porta wire protocol

This is the **canonical, authoritative** contract between the **porta gateway**
(the northbound controller) and any **node** that it commands. The gateway owns
the command vocabulary, the report schema, and the TFTP transfer surface. Nodes
conform to this document; they do not define it.

`nodus` is one conforming node implementation (Toit, classic ESP32). Any future
node implementation — including a planned Smalltalk node — MUST be implementable
from this document alone, without reading `nodus` source. Where this document and
the code disagree, the code wins and this document is the bug.

Source of truth in code:
- Commands: `gateway/command.toit` (encode), `nodus/src/node_command.toit` (decode/apply).
- Report: `nodus/src/report.toit` (build), `gateway/handler.toit` (ingest).
- App/goal shape & defaults: `nodus/src/goal_state.toit`, `nodus/src/inventory.toit`.
- Triggers: `nodus/src/triggers.toit`.
- TFTP resources / framing: `gateway/handler.toit`, `nodus/src/supervisor.toit`.

---

## 1. Transport: TFTP over UDP

All traffic is TFTP. The node is the TFTP **client**; the gateway is the TFTP
**server**. The node identifies itself in every request via a `?id=<mac>` query
suffix on the TFTP resource name, where `<mac>` is an **opaque lowercase-hex
device identifier** (no separators, 12–16 hex digits). The gateway treats it as
an opaque key; derivation is defined per node kind:

| Node kind | Identifier source | Width |
|-----------|-------------------|-------|
| `toit` (ESP32 family) | 6-byte WiFi MAC, e.g. `aabbccddeeff` | 12 hex |
| `st` (nRF52840 / Zephyr) | IEEE 802.15.4 EUI-64, e.g. `aabbccddeeff1122` | 16 hex |

The identifier must be stable across reboot, deep sleep, and reflash — it is the
node's primary key.

The resource name is `base?key=value&key2=value2` — no leading slash. A key with
no `=` maps to the empty string. The gateway parses this with `parse-resource_`
in `gateway/handler.toit`.

| Direction | TFTP op | Resource | Meaning |
|-----------|---------|----------|---------|
| node → gw | RRQ | `commands?id=<mac>` | Pull the oldest undelivered command. Empty body = queue drained. |
| node → gw | RRQ | `payload?id=<mac>&name=<app>&crc=<crc>` | Download a container image (raw bytes) selected by `crc`. |
| node → gw | WRQ | `report?id=<mac>` | Upload the observed-state report. |
| node → gw | WRQ | `data?id=<mac>` | Upload buffered telemetry (JSONL). |

Notes:
- Any RRQ/WRQ carrying `id` causes the gateway to **touch** (last-seen) the node.
- `commands` is served one command per RRQ. The node drains by RRQ-ing
  repeatedly until it receives a **zero-byte body**, which is the
  "queue is empty" sentinel (every real command encodes to at least one byte).
- A `commands` RRQ that transfers a real (non-empty) command is marked
  **delivered** on the gateway only on the TFTP transfer-complete event with
  `ok=true` (`on-transfer-complete` in `gateway/handler.toit`). A failed or
  drain (empty) transfer marks nothing.
- A WRQ to any base other than `report` or `data`, or any WRQ missing `id`, is
  rejected (`STORAGE-ACCESS-DENIED`). A `payload` RRQ whose `crc` does not match
  a stored image throws `STORAGE-FILE-NOT-FOUND`.

---

## 2. Commands (gateway → node)

A command is a single JSON object. It always carries a `"verb"` string; the
remaining keys are the verb-specific arguments. On the wire, encode is:
`{"verb": <verb>, <...args flattened at top level...>}` — the args map is
**flattened into the top-level object**, not nested under an `"args"` key
(`Command.encode` in `gateway/command.toit`). Decode reverses this: every key
except `"verb"` becomes an arg (`NodeCommand.decode`).

Commands are **declarative and absolute**: applying one is idempotent, and a
later command for the same target wins. This makes redelivery safe.

One verb is the exception: `reboot` (§2.8) is **imperative** — a one-shot
instruction, not a declarative target. It is redelivery-safe not because it is
idempotent but because the queue delivers each command exactly once (a command
is marked delivered on its TFTP transfer-complete and never re-served, §1), so a
`reboot` fires once and never re-fires after the node returns.

Verb constants (identical in `gateway/command.toit` and
`nodus/src/node_command.toit`):

| Verb string | Constant |
|-------------|----------|
| `run` | `VERB-RUN` |
| `stop` | `VERB-STOP` |
| `set-mode` | `VERB-SET-MODE` |
| `set-name` | `VERB-SET-NAME` |
| `set-forward` | `VERB-SET-FORWARD` |
| `set` | `VERB-SET` |
| `reboot` | `VERB-REBOOT` |

### 2.1 `run` — install/run an app

Tells the node it should be running app `name` from image `crc` (of `size`
bytes), under the given `triggers`, at `runlevel`, with the declared `lifecycle`
and container `arguments`.

| Key | Type | Required | Default | Meaning |
|-----|------|----------|---------|---------|
| `verb` | string | yes | — | `"run"` |
| `name` | string | yes | — | App name (identity within the node). |
| `crc` | int | yes | — | CRC32-IEEE of the image. Identity + change detection; also the `payload` selector. |
| `size` | int | yes | — | Image byte count. Lets the node size its image writer from the command alone. |
| `triggers` | object | yes | — | `{type: value}` trigger map (see §4). |
| `runlevel` | int | no | `3` | Start ordering / level. |
| `lifecycle` | string | no | `"run-once"` | `"run-once"` or `"run-loop"` (see §2.7). |
| `arguments` | array | no | `[]` | Container arguments. |

Example:
```json
{
  "verb": "run",
  "name": "blink",
  "crc": 305419896,
  "size": 81920,
  "triggers": {"boot": 1, "interval": 60},
  "runlevel": 3,
  "lifecycle": "run-once",
  "arguments": []
}
```

When the node decodes a `run` (`apply-to-goal`), it sets/replaces its goal entry
for `name` with `{size, crc, triggers, runlevel, lifecycle, arguments}`. Absent
`triggers`/`runlevel`/`lifecycle`/`arguments` default to `{}` / `3` /
`"run-once"` / `[]` respectively. (`size`/`crc`/`name` have no defaults — they
are required.)

### 2.2 `stop` — remove an app

| Key | Type | Required | Meaning |
|-----|------|----------|---------|
| `verb` | string | yes | `"stop"` |
| `name` | string | yes | App to remove from the goal/inventory. |

```json
{"verb": "stop", "name": "blink"}
```

The node removes `name` from its goal; reconcile then uninstalls the image.

### 2.3 `set-mode` — power mode (atomic)

A node's power mode is **one declaration**, so it is **one atomic command** — the
node accepts the whole command or rejects it whole (it never half-applies the most
safety-critical operation). It replaces the retired `set-power-mode` +
`set-poll-interval` pair.

| Key | Type | Required | Meaning |
|-----|------|----------|---------|
| `verb` | string | yes | `"set-mode"` |
| `mode` | string | yes | `"deep-sleep"` or `"always-on"`. |
| `max_awake_s` | int | deep-sleep only | Awake-window ceiling (run-once payload-wait cap), seconds; must be > 0. |
| `max_asleep_s` | int | deep-sleep only | Sleep cap = the node's deep-sleep cadence, seconds; must be > 0. |
| `min_awake_s` | int | optional (deep-sleep) | Awake-window floor (no-payload settle window), seconds; `0 < min_awake_s ≤ max_awake_s`. |
| `loop_sleep_s` | int | optional (always-on) | Always-on loop sleep = the node's check-in cadence, seconds; `0 < loop_sleep_s ≤ 600`. Omitted ⇒ the node leaves its stored value unchanged. |

```json
{"verb": "set-mode", "mode": "deep-sleep", "min_awake_s": 5, "max_awake_s": 20, "max_asleep_s": 300}
{"verb": "set-mode", "mode": "always-on", "loop_sleep_s": 300}
{"verb": "set-mode", "mode": "always-on"}
```

- `loop_sleep_s` is the always-on analogue of deep-sleep's `max_asleep_s` cadence. The
  node bounds it at 600 s (it caps the node's HW-watchdog budget) and re-validates
  authoritatively, rejecting an out-of-range value atomically with the whole command.
  When and how the new cadence takes effect is a node implementation detail, not a
  wire contract; the `node_config` echo (§3.2) reports the *in-effect* value.
- The mode chooses the supervisor loop: `deep-sleep` polls then deep-sleeps for
  `max_asleep_s` (waking via full reboot); `always-on` never sleeps, keeping `run-loop`
  daemons (§2.7) alive between reports. A `run-loop` app on a `deep-sleep` node is killed
  by each sleep, so `always-on` is required for a long-lived daemon.
- The node validates atomically (reject partial/invalid), persists the resulting NVS
  config, and **echoes** the effective config back in the report's `node_config` block
  (§3.2). The echo doubles as the convergence ack — a config change is confirmed by
  the gateway's persisted echo reflecting the new mode, not by any separate ACK.

### 2.4 `set-forward` — per-stream forwarding policy

A single **declarative, absolute** command carrying the node's complete northbound
forwarding policy. Each stream is an optional nested object; an omitted stream
resolves to its default (off) on the node — the command is the whole policy, not a patch.

| Key | Type | Required | Meaning |
|-----|------|----------|---------|
| `verb` | string | yes | `"set-forward"` |
| `print` | object | no | `{"on": bool, "every_s"?: int}` |
| `log` | object | no | `{"on": bool, "level"?: string, "every_s"?: int}` |
| `telemetry` | object | no | `{"on": bool, "every_s"?: int}` |

- `level` (log only) ∈ `trace|debug|info|warn|error|fatal`. Absent ⇒ node keeps `warn`.
- `every_s` (optional, all streams): the always-on per-stream forward interval.
  Ignored by deep-sleep nodes (cadence there is `set-mode`'s `max_asleep_s`). Absent ⇒ node
  coalesces with its report window. (Reserved; porta carries it but exposes no CLI flag yet.)

```json
{"verb": "set-forward", "print": {"on": false}, "log": {"on": true, "level": "warn"}, "telemetry": {"on": true}}
```

The node persists the resolved policy in its flash config (so it survives reboot).
FATAL-level logs and panics are delivered regardless of the gates.

### 2.5 `set` — per-app config key

Sets one scalar config key for one app. Config is a plane **separate** from the
goal/triggers; it does not change which apps run.

| Key | Type | Required | Meaning |
|-----|------|----------|---------|
| `verb` | string | yes | `"set"` |
| `app` | string | yes | Target app name. |
| `key` | string | yes | Config key. |
| `value` | scalar | yes | int, float, bool, or string. |

```json
{"verb": "set", "app": "sampler", "key": "interval", "value": 30}
```

The node stores `value` under `app → {key: value}` and echoes the applied blob
back in the report's `config` field (§3), enabling desired-vs-observed
reconciliation. The runtime type of `value` is significant and is preserved
end to end. `set` for the same `(app, key)` is last-write-wins.

### 2.6 `set-name` — node name

Node naming is **node-owned** (stored in NVS, echoed in `node_config`). The gateway
**mirrors** the echoed name for display; it does not originate it.

| Key | Type | Required | Meaning |
|-----|------|----------|---------|
| `verb` | string | yes | `"set-name"` |
| `name` | string | yes | The node's new name. |

```json
{"verb": "set-name", "name": "lab-door"}
```

The node persists `name` and includes it in its next `node_config` echo (§3.2); the
gateway folds that into the node's display name.

### 2.7 `lifecycle` semantics

Declared per app on the `run` command. The halting behaviour of a container
cannot be inferred, so it is declared:

- `"run-once"` (`LIFECYCLE-RUN-ONCE`, the default): the container is expected to
  **return**. The supervisor may `wait` on it (with a cap) before sleeping.
- `"run-loop"` (`LIFECYCLE-RUN-LOOP`): the container **never returns**
  (always-on). The supervisor starts it but must not block waiting for it to
  exit.

### 2.8 `reboot` — restart the node

| Key | Type | Required | Meaning |
|-----|------|----------|---------|
| `verb` | string | yes | `"reboot"` |

```json
{"verb": "reboot"}
```

Node-control verb carrying no args: the verb alone is the instruction. It does
**not** change the goal/app set.

**Imperative, not declarative.** Unlike every other verb, `reboot` is a one-shot
action rather than a declarative target (see the §2 preamble). It is
redelivery-safe only because the queue delivers each command exactly once — a
`reboot` fires once and never re-fires after the node returns. Multiple
`reboot`s drained in one poll collapse to a single reboot.

**Timing.** The node applies the reboot at the **end of the current poll** —
after draining the rest of the command batch and PUTting its report — so the
operator still gets a final report and any same-batch commands take effect
first.

**No convergence.** There is no observed-state echo for a reboot, so the gateway
treats it as **terminal on delivery**: the command lifecycle reaches
`delivered` when the node pulls it and never advances to `converged` (only `set`
reconciles against observed state). The operator infers success from the node
re-appearing after its restart.

**Reset reporting (node conformance).** A *commanded* reboot SHOULD surface in
the node's next report as `health.reset: "software"` (§3.1) so the gateway can
distinguish an operator-commanded restart from a duty-cycle deep-sleep wake.
This requires the node to use a true software-reset primitive — **not**
`esp32.deep-sleep`, which would report `deep-sleep`.

---

## 3. Report / observed state (node → gateway)

Each wake, after reconciling, the node PUTs (WRQ) one JSON object to
`report?id=<mac>` (`build-report` in `nodus/src/report.toit`):

```json
{
  "apps": {
    "blink": {
      "crc": 305419896,
      "runlevel": 3,
      "lifecycle": "run-once",
      "triggers": {"boot": 1, "interval": 60}
    }
  },
  "config": {
    "sampler": {"interval": 30}
  },
  "health": {
    "uptime_us": 1234567,
    "wakes": 42,
    "reset": "watchdog",
    "reset_code": 6
  },
  "node_config": {
    "mode": "deep-sleep",
    "min_awake_s": 5,
    "max_awake_s": 20,
    "max_asleep_s": 300,
    "name": "lab-door"
  },
  "chip": "esp32",
  "sdk": "v2.0.0-alpha.192"
}
```

`node_config` is the **effective-config echo** (§3.2) — present **only** on cold boot
and after a config change; steady-state reports omit it.

Fields:

| Path | Type | Meaning |
|------|------|---------|
| `apps` | object | Observed installed apps, keyed by app name. |
| `apps.<name>.crc` | int | Installed image CRC32-IEEE (what is actually on flash). |
| `apps.<name>.runlevel` | int | Observed runlevel. |
| `apps.<name>.lifecycle` | string | Observed lifecycle (`"run-once"` / `"run-loop"`). |
| `apps.<name>.triggers` | object | Observed `{type: value}` trigger map (§4). |
| `config` | object | Applied per-app config blob: `app → {key: value}`. May be empty `{}`. |
| `health.uptime_us` | int | Monotonic uptime in microseconds. |
| `health.wakes` | int | Cumulative wake count. |
| `health.reset` | string (optional) | Neutral reset category — the node maps its platform reset code onto the vocabulary below. Absent on firmware predating reset reporting. |
| `health.reset_code` | int (optional) | Raw platform reset code, for diagnostics only. The gateway never interprets it. |
| `node_config` | object (optional) | The node's **effective-config echo** (§3.2): mode + its knobs + name. Present **only** on cold boot and after a config change. Absent on steady-state reports. |
| `chip` | string (optional) | Node chip model, e.g. `"esp32"`, `"esp32c6"`, `"esp32s3"`. Used by a node-repo dev tool (e.g. `nodus run`) to pick the flash envelope. Absent on firmware predating identity reporting. |
| `sdk` | string (optional) | Toit SDK version the node firmware was built with, e.g. `"v2.0.0-alpha.192"`. A node-repo dev tool (e.g. `nodus run`) refuses to deploy an image built with a different SDK (overridable with `--force`); absent → it blocks until the node reports it. |
| `kind` | string (optional) | The node's **runtime/payload family** — which toolchain builds the images this node runs: `"toit"` (Toit container images) or `"st"` (Smalltalk → Berry `.bec` bytecode). Not the chip (see `chip`) and not the transport (observable from the peer address). Defaults to `"toit"` when never reported, for back-compat with firmware predating kind reporting. |

Gateway ingest (`ReportWriter_` in `gateway/handler.toit`):
- `apps`, `config`, `health` each default to `{}` if absent (a node that does
  not implement `config` is tolerated; `config` then defaults empty).
- `chip` / `sdk` / `kind` are optional self-reported firmware identity. The gateway
  records them on the node row (self-healing — corrected automatically if a device
  is reflashed); an absent or empty value never clobbers a previously-known identity.
- The gateway stores `{"apps":…, "config":…}` as observed-state and `health`
  separately.
- `health.reset` / `health.reset_code` are optional. The gateway records the latest
  on the node row (an absent/empty value never clobbers the last known one — like
  `chip`/`sdk`), surfaces it on node detail, and emits a `data_log` event (`kind:"reset"`)
  the first time a **fault** category appears (`watchdog`, `panic`, `brownout`).
- `node_config` is optional (§3.2). When present, the gateway persists the block as
  the node's cached effective config and mirrors the echoed `name` for display; an
  absent block (steady-state report) never clobbers the cache. From the cached cadence
  the gateway **derives** the node's offline window — it stores no settable `max_offline`.
- After committing the report, the gateway runs config **self-heal**: it diffs
  the desired config (projected from delivered `set` commands) against the
  reported `config` and re-enqueues any delivered-but-divergent `set` (tagged
  `gateway-reconcile`). The node need do nothing special for this — it just keeps
  echoing its applied `config`.

There is no `goal` resource the node fetches: the node seeds its goal from its
own persistent inventory and applies drained commands on top. The report is the
node's only northbound state declaration.

### 3.1 Reset categories

`health.reset` carries a **neutral** reset category — never a raw platform enum.
Each node maps its own platform reset code (e.g. an esp32 `RESET-*` value) onto
this canonical set; the gateway stays implementation-agnostic. The only permitted
values:

| Category | Meaning |
|----------|---------|
| `power-on` | cold / power-on reset |
| `deep-sleep` | wake from deep sleep (normal duty-cycle wake) |
| `software` | software-requested reboot |
| `external` | external / reset-pin |
| `watchdog` | watchdog timeout (task or HW) |
| `panic` | software panic / exception |
| `brownout` | supply-voltage dip |
| `unknown` | unmapped / unavailable |

The optional `health.reset_code` carries the raw platform code alongside the
category, for diagnostics only — the gateway records and displays it but never
interprets it. `watchdog`, `panic`, and `brownout` are the **fault** categories the
gateway treats as noteworthy (a `data_log` event on first appearance).

### 3.2 Effective-config echo (`node_config`)

The node **owns** its configuration and declares it back as a top-level `node_config`
block so the gateway can display it (read-only) and derive liveness. To stay frugal on
the wire (ESP-NOW / Thread MTUs), config does **not** travel every report:

- Echoed **only** on (a) cold boot and (b) any config change. Steady-state reports omit it.
- The on-change echo **is** the convergence ack for `set-mode`/`set-name` (§2.3).
- The boot echo **heals drift** in the gateway's cache after a reflash/reset.

Each node declares only the fields native to its mode (`build-node-config` in
`nodus/src/node_config.toit`):

**deep-sleep:**
```json
{"mode": "deep-sleep", "min_awake_s": 5, "max_awake_s": 20, "max_asleep_s": 300, "name": "lab-door"}
```

**always-on:**
```json
{"mode": "always-on", "loop_sleep_s": 60, "name": "vin"}
```

| Field | deep-sleep | always-on | Meaning |
|-------|:---------:|:---------:|---------|
| `mode` | ✅ | ✅ | `"deep-sleep"` / `"always-on"`. |
| `min_awake_s` | ✅ | — | Awake-window floor (settle window), seconds. |
| `max_awake_s` | ✅ | — | Awake-window ceiling (payload-wait cap), seconds. |
| `max_asleep_s` | ✅ | — | Sleep cap = the node's cadence, seconds. |
| `loop_sleep_s` | — | ✅ | The run-loop's sleep duration = control-plane check-in cadence, seconds. |
| `name` | optional | optional | Node-owned name; **omitted** when the node is unnamed. |
| `max_offline` | **never** | **never** | Gateway-derived, never on the wire (see below). |

No redundant fields: a deep-sleep node's cadence *is* `max_asleep_s`, so it does not also
send `loop_sleep_s`; an always-on node never sleeps, so it sends `loop_sleep_s`
instead. `loop_sleep_s` is the **control-plane** heartbeat (the `report?id=` PUT +
`commands?id=` fetch round-trip), **not** a telemetry cadence — liveness must key off the
control-plane heartbeat, since a healthy ultra-low-power node may be silent on telemetry
for hours while still checking in.

**Liveness derivation (gateway-side).** The gateway computes, from the echoed cadence:

```
offline = k × cadence            k = 3 (gateway policy constant)
cadence = (mode == "deep-sleep") ? max_asleep_s : loop_sleep_s
```

`k = 3` tolerates two consecutive missed check-ins (flaky TFTP under load, WiFi re-assoc)
before flapping a node offline. `max_offline` is **retired, not moved** — it is derived
from fields the gateway already receives, with no extra every-report wire field.

---

## 4. Triggers

The trigger map is `{type: value}` (`nodus/src/triggers.toit`,
`gateway/command.toit:triggers-from-flags`). Recognised entries:

| Key | Value | Meaning |
|-----|-------|---------|
| `boot` | `1` | Run on (cold) boot. |
| `install` | int | Run on the Nth install generation. |
| `interval` | int (seconds) | Periodic wake. |
| `gpio-high:<pin>` | `<pin>` (int) | Wake/run on GPIO `<pin>` high (ext1). |
| `gpio-low:<pin>` | `<pin>` (int) | Wake/run on GPIO `<pin>` low. |
| `gpio-touch:<pin>` | `<pin>` (int) | Wake/run on touch pin `<pin>`. |

The key carries the pin for the GPIO/touch variants (e.g. `"gpio-high:33": 33`).
An unrecognised key makes the node throw on parse.

---

## 5. Image payload delivery (TFTP)

> **Framing note (current vs. retired).** Earlier porta/nodus smoke-test specs
> describe a self-describing delivery blob `[u32 size_le][u32 crc32_le][image
> bytes]` with an 8-byte header read before streaming. **That header has been
> removed.** In the shipping protocol the `payload` resource is the **raw image
> bytes only** — size and CRC ride in the `run` command (§2.1), not in a blob
> header. See the comment in `nodus/src/image_writer.toit`. Document and
> implement the current form below.

To install an app, the node:

1. Receives a `run` command carrying `name`, `crc`, `size` (§2.1).
2. Issues an RRQ for `payload?id=<mac>&name=<name>&crc=<crc>`. The gateway
   selects the stored image by `crc` and returns the **raw image bytes**
   (no header). The TFTP transfer size equals `size`.
3. Streams the bytes straight into its image writer, sized from the command's
   `size`. It does **not** parse any leading header.
4. On completion, verifies:
   - **length** equals the command's `size` (else "truncated stream"), and
   - **CRC32-IEEE** of the streamed bytes equals the command's `crc`
     (else "CRC32 mismatch").
   Only then does it commit the image.

### CRC32-IEEE parameters

The checksum is standard CRC32-IEEE (`ImageStreamWriter` in
`nodus/src/image_writer.toit`):

| Parameter | Value |
|-----------|-------|
| Width | 32 |
| Polynomial | `0xEDB88320` (reversed/little-endian form) |
| Initial state | `0xFFFFFFFF` |
| XOR out | `0xFFFFFFFF` |
| Reflected | yes (little-endian CRC) |

This is the same CRC used by `jag` for its `X-Jaguar-CRC32` and by the gateway.

---

## 6. Telemetry data (node → gateway)

When forwarding is enabled for a stream (§2.4) and the node buffered
entries this wake, it PUTs (WRQ) a **JSONL** body (one JSON object per line) to
`data?id=<mac>` (`build-data-body` in `nodus/src/telemetry_codec.toit`).

Each line is one entry:

| Key | Type | Required | Default at gateway | Meaning |
|-----|------|----------|--------------------|---------|
| `kind` | string | no | `"log"` | Entry kind: `"print"`, `"log"`, `"metric"`, `"panic"`, or `"reset"`. |
| `level` | string | no | `null` | Log-stream severity (`trace`..`fatal`). Present on `"log"` entries only. |
| `name` | string | no | `null` | Metric/series name. |
| `value` | scalar | no | `null` | int / float / bool — typed scalar value. |
| `text` | string | no | `null` | Log text, string-valued reading, or (for `"panic"`) the base64 trace blob. |
| `ts` | int | no | gateway receive time | Timestamp (epoch seconds). |
| `seq` | int | no | line index | Sequence within the batch. |

Entries the node emits in practice:
```json
{"kind": "print", "text": "raw print output"}
{"kind": "log", "level": "warn", "text": "pump stalled"}
{"kind": "metric", "name": "pm2_5", "value": 12}
{"kind": "panic", "text": "<base64 trace blob>"}
```

FATAL-level logs and `panic` entries are part of the must-deliver subset — the node ships them even when the corresponding gate is off.

The `"panic"` kind reports an uncaught payload exception: `text` is the base64 of
the node's raw trace ("system message"). Decoding/symbolication is **node-defined**
and lives in the node's dev tooling (e.g. `nodus panic`, which wraps `jag decode`);
the normative panic-reporting contract lives in the node repo. `kind` is free-form:
the gateway stores any value verbatim, so the panic kind is additive (no schema or
ingest change).

Gateway ingest (`DataWriter_` in `gateway/handler.toit`) decodes each line and
appends it to the data_log, preserving the runtime type of `value` via a
`value_type` tag: `bool → 0/1` + `"bool"`; `int → "int"`; `float → "float"`;
`string` value lands in the text column + `"string"`; `null`/array/object →
no scalar value. A line that fails to decode (e.g. a truncated final line) is
skipped; the rest of the batch is unaffected.

---

## 7. Conformance

A conforming node MUST:

- Identify itself with `?id=<hex-id>` (opaque lowercase hex, 12–16 digits, §1)
  on every TFTP request.
- Drain `commands?id=` by repeated RRQ until a zero-byte body, treating commands
  as absolute/idempotent (last write wins per target).
- Honour the seven verbs (`run`, `stop`, `set-mode`, `set-name`, `set-forward`,
  `set`, `reboot`) with the arg schemas and defaults in §2, including the
  `lifecycle` declaration (default `run-once`) and `runlevel` (default `3`).
  `set-mode` MUST apply atomically (accept whole or reject whole); an always-on
  `set-mode` MAY carry the optional `loop_sleep_s`, which the node re-validates,
  rejecting an out-of-range value atomically with the whole command. `reboot` is
  applied at the end of the poll and SHOULD report `health.reset: "software"` on
  the next check-in.
- Download images via `payload?id=&name=&crc=` as **raw bytes** and verify
  length against the command's `size` and CRC32-IEEE (§5 parameters) against the
  command's `crc` before committing.
- Report observed state to `report?id=` with the `apps` / `config` / `health`
  shape in §3, echoing per-app `crc`/`runlevel`/`lifecycle`/`triggers` and the
  applied `config` blob, plus the `node_config` effective-config echo (§3.2) on
  cold boot and after any config change (omitted on steady-state reports).
- (If it forwards telemetry) ship JSONL to `data?id=` per §6.
- (If it forwards telemetry) forward print/log/telemetry per the resolved `set-forward` policy, tagging log entries with `level`, and deliver FATAL logs + `kind:"panic"` entries even when the relevant gate is off.
- (If it forwards telemetry) report uncaught payload exceptions as `kind:"panic"`
  entries per §6 (the base64 trace blob in `text`); decoding is node-defined and
  lives in the node repo's tooling.

A conforming node MAY omit `config` from its report (it defaults to empty),
omit optional command args (defaults apply), and implement any transport that
presents the same TFTP RRQ/WRQ resource surface (WiFi is the only transport
today; ESP-NOW / BT-mesh are planned behind the same interface).

A conforming node SHOULD report its `chip` / `sdk` identity (§3) so a node-repo dev
tool (e.g. `nodus run`) can verify payload/SDK compatibility before deploying; a node
that omits them still conforms, but such a tool blocks against it until identity is
known.
