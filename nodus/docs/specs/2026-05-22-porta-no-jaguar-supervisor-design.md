# Design: Porta no-jaguar supervisor + Artemis-pod-compatible node runtime

**Date:** 2026-05-22
**Status:** Draft (brainstorming complete; pending user review → implementation plan)
**Supersedes scope of:** the throwaway smoke-test harness in
`docs/specs/2026-05-21-toit-tftp-loader-design.md` (M1/M2 proved the transport;
this design builds the real node runtime on top of it).

## Context & strategic framing

Porta is a **LAN gateway**: an on-premises hub that delivers executable code and
commands to local nodes over whatever transport they speak (WiFi/UDP today;
Thread and **ESPnow** as the differentiating future), and marshals data/MCP back
up. It is explicitly **not** a WAN fleet manager.

**Artemis is the WAN tier, not a competitor.** Artemis manages
internet-distributed fleets via a cloud broker, with diff-OTA to save cellular
data and a signing model for untrusted networks. Porta sits *below* it: Artemis
(or an operator) hands Porta **pod artifacts** over a VPN that the gateway — not
the Toit nodes — terminates; Porta extracts their contents and distributes them
locally. The nodes never speak Artemis or pods; Porta translates.

Two findings make this strategically valuable and tractable:

1. **Artemis pods are crackable artifacts we can consume.** A `.pod` is an `ar`
   archive; its `customized.env` member is an envelope (also `ar`) holding
   `$metadata` (SDK/chip), the per-container **images** as named members, and a
   `device-config` asset describing each container. Porta can open a pod, gate on
   SDK/chip, extract just the application container image(s) + their descriptions,
   and ship those to a node — dropping the envelope/VM and the Artemis service
   container for nodes that already run compatible Porta firmware.
2. **Artemis's on-device architecture is already our model.** Artemis installs
   app containers with firmware `--trigger="none"` and lets its *service
   container* (the sole boot container) schedule them from a **goal-state map**
   using a fixed trigger vocabulary. That is precisely the supervisor model we
   arrived at independently. So Porta's supervisor is a **minimal, LAN /
   multi-transport reimplementation of the Artemis device service's
   reconcile + schedule loop**, and the Artemis `src/service/` is our reference.

By adopting Artemis's **goal-state shape** and **trigger vocabulary** as the
node↔gateway contract, everything we build now sits on the pod-compatible path:
the gateway becomes pod-feedable later with zero node changes.

## Goal & success criterion

Establish the Porta node runtime **without jaguar**: a custom firmware envelope
whose system app is a **supervisor** that owns deep-sleep, brings up its
**configured transport**, reconciles an Artemis-shaped goal-state delivered over
the LAN, schedules container payloads by the Artemis trigger vocabulary, and
survives power-cycles offline via a non-volatile inventory.

**Success =** flash the custom envelope with `jag flash --exclude-jaguar`; the
device boots straight into the supervisor (no jaguar present, verified by serial
+ static `firmware … show`); the supervisor brings up its configured transport
(**WiFi for this milestone**, from flash-time credentials), polls the minimal
Porta gateway, reconciles a goal-state that installs and schedules the captured
payload container, runs it per its triggers, deep-sleeps, and on wake re-runs the
cycle — relaunching the persisted payload **without** re-downloading it when
unchanged, and resuming correctly after a simulated cold boot from the NVS
inventory.

## Locked decisions (do not re-litigate)

- **SDK v2.0.0-alpha.192, Bluetooth left enabled.** No version bump (the .194
  fixes — float_to_string leak, UART write+flush hang, ESP-IDF 5.4.2, esp32p4,
  JSON floats — are noted for a future bump, none blocking). BT-off would be a
  big RAM win but requires a from-source envelope build; deferred.
- **Envelope: prebuilt `firmware-esp32.envelope` @ .192** from `toitlang/envelopes`
  + `toit tool firmware … container install` + `jag flash --exclude-jaguar`.
  No native toolchain, no `menuconfig`.
- **Decoupled Toit stack.** Build the Toit-native node + a minimal Toit-side
  gateway now; do **not** entangle the ST-only Go gateway (`gateway/`, EUI-64 /
  Berry-shaped). The polyglot unification — later folding ST into *this* server
  and retiring the Go one — is its own future sub-project.
- **Adopt Artemis goal-state + trigger vocabulary** as the node↔gateway contract
  (see Scheduling below).
- **Drop the 8-byte self-describing blob header.** Metadata (`id`, `size`, `crc`,
  triggers) travels in the goal-state/command; the image transfer is **raw
  relocated image bytes**. Retires `host/header.toit`, `host/blob.toit`, and the
  header-peeling in `BlobInstallWriter`.
- **Build the transport seam now** (channel interface + `UdpChannel`); ESPnow is
  a documented future driver, not built here.

## Architecture

### Node: supervisor + trigger=none consumers

```
ESP32 — custom envelope, NO jaguar
firmware system process
  └─ boots → SUPERVISOR (the only boot-trigger container)
       on every wake:
       1. read esp32.wakeup-cause + RTC schedule cache; update wall-clock
          (esp32.total-deep-sleep-time + Time.monotonic-us)
       2. if NVS inventory empty (cold boot) → must reach gateway to provision
          else → restore goal-state from NVS, re-arm gpio/touch wake sources
       3. DISPATCH by reason + due-time:
            - WAKEUP-TIMER & porta poll due → poll gateway, reconcile goal-state
            - consumer's interval due       → containers.start <id>
            - WAKEUP-EXT0/EXT1/TOUCHPAD     → start the gpio/touch consumer;
                                              porta does NOT poll (not its slot)
       4. wait for started consumers
       5. next-sleep = min(next porta poll, next consumer due); esp32.deep-sleep
       (wake = full reboot → step 1)
```

The supervisor is the sole **boot-trigger** container. The **gateway poll is a
supervisor-internal task** for now (conceptually one consumer among others,
splittable into its own container later). Payload **consumers** are installed
firmware `--trigger=none`, so the firmware never auto-starts them — the
supervisor does, exactly when due. **Consequence:** a consumer needs no
`wakeup-cause` checks; when its `main` runs, that *is* the signal it is its slot.
The supervisor may optionally pass the wake reason as a start argument if a
consumer cares.

This mirrors Artemis: it too uses `--trigger="none"` + a service container that
schedules from goal-state. Runtime install commits with `--run-boot=false`
(no `JAGUAR-INSTALLED-MAGIC`), which is also why the old double-start wart is gone.

### Scheduling: the Artemis trigger vocabulary

The supervisor honors exactly the Artemis trigger set, in the readable
goal-state map form (`{type: value}`):

| Trigger | Goal-state form | ESP32 mechanism |
|---|---|---|
| `boot` | `{"boot":1}` | start on every supervisor boot |
| `install` | `{"install":<nonce>}` | start once after (re)install |
| `interval` | `{"interval":<seconds>}` | deep-sleep timer; due when elapsed |
| `gpio-high`/`gpio-low` | `{"gpio-high:<pin>":<pin>}` | `enable-external-wakeup` (ext0/ext1) |
| `gpio-touch` | `{"gpio-touch:<pin>":<pin>}` | `enable-touchpad-wakeup` |
| `restart`/`delayed` | (runtime) | scheduled re-run after delay |

There is **no cron and no on-network** trigger in Artemis; we do not invent any.
Reference: `artemis/src/cli/pod-specification.toit:761-912`,
`artemis/src/service/containers.toit:174-187`,
`artemis/artemis-pkg-copy/src/artemis.toit:103-154`.

### Goal-state contract (node ↔ gateway)

The poll delivers an **Artemis-shaped goal-state** (or a delta). Per-container
description, mirroring Artemis's `device-config["apps"]` entry, plus the
streaming-install fields Porta needs:

```jsonc
{
  "apps": {
    "payload": {
      "id":        "<image-uuid>",      // = Uuid(sha256(snapshot-uuid + assets))[..16]
      "size":      38016,                // bytes, to construct ContainerImageWriter
      "crc":       2157114022,           // CRC32-IEEE, verified after streaming
      "triggers":  { "interval": 60 },
      "runlevel":  3,
      "arguments": []
    }
  }
}
```

Reconciliation mirrors Artemis `process-goal_`
(`artemis/src/service/synchronize.toit:730-803`): for each app, if `id` is
already installed (known from NVS inventory) → just schedule it; else fetch the
raw image bytes by `id` over the transport, stream into
`system.containers.ContainerImageWriter(size)`, `commit`, verify `crc`, then
schedule per `triggers`. The compact **poll up-call** carries the node's
`{node-id, chip, sdk, [{name:id}]}` so the gateway can return only deltas
("up-to-date" / "load X"). On a LAN, bandwidth is cheap — no binary-delta OTA.

### Transport seam (two layers)

Transport is pluggable at two levels, and the supervisor must **not** hardcode
WiFi:

1. **`Transport` — link selection + bring-up.** Driven by a **connection
   descriptor** that mirrors Artemis's pod `connections` array (`wifi`,
   `cellular`, `ethernet`; `artemis/src/cli/pod-specification.toit:452-528`),
   which Porta extends with `espnow` and `bt-mesh`. A `Transport` opens the link
   per its descriptor and yields a `Channel`. For WiFi this is `net.open` +
   `udp-open`; for ESPnow the `espnow.Service` *is* both link and channel.
2. **`Channel` — the datagram interface** the reliable-block engine uses:
   `send bytes` / `receive -> bytes` / `close`.

Correspondingly, split the Toit TFTP client into a **reliable-block engine**
(block sequencing, ACK, retransmit, OACK/blksize negotiation — transport-agnostic)
sitting on a `Channel`.

Implement **`WifiTransport` + `UdpChannel` now**; the connection descriptor is
config-driven (default WiFi this milestone). `EspnowChannel`/`bt-mesh` are later
drop-ins: Toit's `esp32.espnow.Service` (`send --address`,
`receive -> Datagram{address,data}`, `add-peer`, optional `Key`) is structurally
identical, and ESPnow datagrams are **~1470 bytes** — a ~1024-byte block fits in
one frame. ESPnow has no path/`?id=` notion, so the command layer needs its own
tiny framing there (peer = node MAC); the reliable-transfer reuse is the prize.
The current `tftp_client.toit` already isolates socket ops, so introducing the
interfaces is contained.

### Gateway (minimal, this sub-project)

A small **Toit-side** server (grown from `host/serve.toit`) that:
- answers a node poll with a **hand-crafted goal-state** in the Artemis format,
  pointing at the captured container image; and
- serves the **raw image bytes** by id over the transport.

It is the seed of the future polyglot gateway (later it ingests real pods and
absorbs the ST function, retiring the Go server) — so written with light care,
not as pure throwaway. No `.pod` parsing, topology, or MCP yet, and **no
persistent store this milestone** (the goal-state is hand-crafted in memory).
When it later grows a device registry / desired-state / topology store, use the
Toit **`sqlite`** package (`~/workspaceToit/sqlite`), mirroring the Go gateway's
`store/store.go`.

## Storage tiers

| Tier | Survives | Holds |
|---|---|---|
| RTC memory (`esp32`, 4 KB) | deep-sleep only | volatile: next-due timestamps, wake counter, wall-clock accumulator |
| **NVS — `storage.Bucket --flash`** | **power-cycle** | the **inventory**: installed `{name, id, size, crc, triggers, runlevel}` + porta poll config |
| flash container registry | power-cycle | the bulky payload image bytes |
| gateway | — | **desired** goal-state (source of truth for *changes* and first provisioning) |

Payload **images already survive a power-cycle** (flash registry); only the
volatile RTC schedule cache is lost. So a power-cycled node resumes **fully
offline** from the NVS inventory: re-arm wake sources, `containers.start` due
payloads — no gateway round-trip. The gateway is authoritative; RTC is a cache;
NVS is the durable mirror.

## Envelope build / flash recipe (the deliverable)

```
# 1. Prebuilt envelope, SDK-matched
curl -L -o firmware-esp32.envelope.gz \
  https://github.com/toitlang/envelopes/releases/download/v2.0.0-alpha.192/firmware-esp32.envelope.gz
gunzip firmware-esp32.envelope.gz

# 2. Compile the supervisor → image (-m32: classic ESP32 is 32-bit)
toit compile -s -o supervisor.snapshot device/supervisor.toit
toit tool snapshot-to-image -m32 --format=binary -o supervisor.image supervisor.snapshot

# 3. Install supervisor as the (sole) boot container
toit tool firmware -e firmware-esp32.envelope container install supervisor supervisor.image

# 4. Verify supervisor present, jaguar absent
toit tool firmware -e firmware-esp32.envelope show

# 5. Flash without jaguar, provisioning WiFi creds (stored in firmware config,
#    independent of the jaguar container — verified in jag firmware_flash.go:303-305)
jag flash firmware-esp32.envelope --exclude-jaguar \
  --wifi-ssid <SSID> --wifi-password <PW> --port /dev/ttyUSB0

# 6. Observe (serial only — no network jag commands without jaguar)
jag monitor
```

## Verification

- **Static:** `firmware … show` confirms supervisor present, jaguar absent.
- **Boot:** serial shows supervisor boot → WiFi up → goal-state poll → image
  install → payload runs per its `interval` trigger.
- **No-redownload:** across a deep-sleep wake, payload relaunches from flash with
  no image transfer (poll returns "up-to-date").
- **Cold-boot autonomy:** clear RTC (power-cycle) → supervisor rebuilds schedule
  from NVS and resumes without the gateway.
- **Multi-rate (degenerate):** payload `interval` set faster-than-real so
  multi-rate scheduling is observable with a single payload.

## Design constraints (read before implementing)

- **MAJOR — first-provisioning is a hard gateway dependency.** A node with empty
  NVS and no image in flash (never provisioned, or NVS erased/corrupted) cannot
  operate without first reaching the gateway. *After* first provisioning the node
  is autonomous across power-cycles and gateway outages. On a LAN the gateway is
  local and usually always-on, so this is acceptable — but it must be stated.
  Future mitigations (not now): a minimal fallback payload baked into the
  envelope; signed inventory snapshots.
- **No network recovery channel.** With jaguar excluded, `jag monitor` (serial)
  works but `jag container list` / network commands do not. A bad supervisor is
  recovered only by USB reflash. Mitigated by wrapping each poll in
  `catch --trace` so transient failures still reach deep-sleep and retry.
- **SDK/chip coupling.** The container image is relocated per chip + SDK; it must
  match the node's Porta firmware. Guaranteed here by using one SDK (.192) for
  envelope, supervisor, and payload. When ingesting real pods later, the gateway
  must compare the pod's `$metadata` `sdk-version`/`kind`/`word-size` against the
  target node before distributing.
- **One sleep cycle per chip.** Deep-sleep is a full reset; there are no
  independent per-payload wakeups on one ESP32. "Other payloads waking on their
  own cycle" = other *nodes* in the fleet. (Light-sleep, which retains RAM, is the
  fork if a payload must run across short naps — not used here.)
- **Storage service must be in the envelope** for `storage.Bucket --flash`
  (standard; confirm at first bring-up).

## Out of scope (later sub-projects)

- **Real `.pod` ingestion in the gateway:** open `ar` → `customized.env` →
  `$metadata` gate → extract container images by id from `device-config["apps"]`
  → derive goal-state. Reference: `artemis/src/cli/pod.toit`,
  `pod-specification.toit`, `broker.toit:900-1030`, `sdk.toit:369-410`.
- **Topology registry + multi-transport routing:** a real node/transport/group
  model (artemis-facade has none to reuse — only the CLI-wrap pattern, the
  captured Artemis device-record schema, and a floorplan-SVG UI idea).
- **VPN termination** on the gateway (Artemis ↔ Porta uplink).
- **Data-acquisition + MCP marshalling** on the gateway.
- **Fleet/group → per-node mapping** (Artemis is device→group→pod, group-granular).
- **Additional transports** — `EspnowChannel`, `bt-mesh`, and the
  `cellular`/`ethernet` connection-descriptor types; only `WifiTransport` is built
  now (the `Transport`/`Channel` seam + connection descriptor make these
  drop-ins).
- **Persistent gateway storage** — when the gw grows a real device registry /
  desired-state / topology store, use the Toit **`sqlite`** package
  (`~/workspaceToit/sqlite`), mirroring the Go gateway's `store/store.go`. The
  minimal gw this milestone needs no DB (hand-crafted goal-state).
- **Whole-firmware OTA** of a node (reflash for now); **diff/delta OTA** (unneeded
  on a LAN); **BT-off custom envelope** (RAM win); **polyglot unification** with
  the ST Go gateway; **signing/auth**.

## References (Artemis, read-only)

- Pod artifact: `artemis/src/cli/pod.toit` (ar members, `Pod.parse`).
- Pod spec + triggers: `artemis/src/cli/pod-specification.toit`;
  schema `artemis/public/schemas/pod-specification/v1.json`.
- Spec→goal-state translation (best gateway reference):
  `artemis/src/cli/broker.toit:900-1030`.
- SDK/chip from envelope: `artemis/src/cli/sdk.toit:369-410`.
- Identity + fleet→pod: `artemis/src/cli/device.toit`, `fleet.toit`.
- On-device apply/install/schedule (best supervisor reference):
  `artemis/src/service/{synchronize,containers,jobs}.toit`;
  trigger wire encoding `artemis/artemis-pkg-copy/src/artemis.toit:103-154`.
- Gateway storage: Toit `sqlite` package `~/workspaceToit/sqlite`; existing
  Go-gateway schema `gateway/store/store.go` (devices, command_queue).
</content>
</invoke>
