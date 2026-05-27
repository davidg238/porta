# Design: Toit-over-TFTP loader smoke test

**Date:** 2026-05-21
**Status:** Approved (brainstorming complete; ready for implementation plan)
**Location of spike code:** `~/workspaceToit/tftp-loader/` (`host/` + `device/`)

## Context

We want to generalize the jast-gw gateway into a polyglot compile-and-dispatch
fabric: the UDP/TFTP **poll** model becomes the universal transport for delivering
executable code to nodes. ST source compiles to `.bec` for Berry-VM nodes
(nRF52840/Thread); Toit source would compile to a relocated container image for
Toit-VM nodes (ESP32/WiFi/IPv4). The poll model is chosen deliberately over
Jaguar's always-on HTTP/TCP server, which breaks sleepy nodes and conflicts with
apps that want their own access point.

This spec covers only the **first sub-project**: a smoke test that de-risks the
single unproven mechanism. Subsequent sub-projects (own image pipeline + custom
envelope; compile service + dispatcher in jast-gw) are out of scope here.

## Goal & success criterion

Prove that **a relocated ESP32 container image, delivered over UDP/TFTP (not
Jaguar's HTTP), flashes and runs on a classic ESP32 (Xtensa) via
`system.containers.ContainerImageWriter`**, and survives a deep-sleep / wake /
re-poll cycle.

**Success =** the device's serial console shows a *delivered* payload container's
heartbeat output, where the image arrived over TFTP, and it reappears after a
deep-sleep wake.

## Key findings that make this feasible (evidence)

Both crux pieces already exist — this is extraction + integration, not invention.

- **On-device install is transport-agnostic.** `~/workspaceToit/jaguar/src/jaguar.toit`
  `flash-image image-size/int reader/reader.Reader name/string? defines/Map --crc32/int`
  takes a generic `reader.Reader`, drives `system.containers.ContainerImageWriter`,
  verifies CRC32, and commits via the registry in `container_registry.toit`.
  Jaguar only ever supplies an HTTP-body reader; we supply a TFTP reader instead.
  `run-installed-containers` (jaguar.toit:122) auto-restarts named installed
  containers on boot/wake.
- **Host image production.** jag does `toit compile --snapshot` then
  `toit tool snapshot-to-image` (jag `util.go:204-246`, `Build(ctx, device, ...)`),
  relocating the image **per chip** — so the image is coupled to the device's
  chip + SDK version.
- **Upload contract.** jag `device_network.go:108-119`: `PUT /run` (or `/install`),
  body = relocated image bytes, header `X-Jaguar-CRC32 = crc32.ChecksumIEEE(body)`
  (standard CRC32-IEEE), image-size = body length.

## Approach (chosen: "borrowed image, throwaway harness")

Capture a known-good, SDK-matched image directly from jag rather than building
our own. This removes all image-production risk so any failure points squarely at
the TFTP -> flash-image -> run loop.

## Components & data flow

```
HOST (one-time capture)                 HOST (harness)              ESP32 (jag-flashed, known SDK)
hello.toit --jag run--> PUT sink   +--> tftp_server.toit <--TFTP--  loader (installed container)
                 saves body+crc32  |     serves "firmware" blob       1. wifi up (STA/IPv4)
                        |          |                                   2. read "firmware"
                        v          |                                   3. parse 8B header
                 firmware blob ----+                                   4. flash-image(reader,size,crc32)
                 = [u32 size_le][u32 crc32_le][image bytes]            5. start payload container
                                                                       6. deep-sleep -> wake -> goto 1
```

### 1. Capture sink (host, throwaway, ~40 lines Go or Python)
- Accepts `PUT /run` and `PUT /install`.
- Saves the request body to `host/image` and the `X-Jaguar-CRC32` header value.
- Procedure: run `jag run hello.toit` (or `jag container install`) targeted at the
  sink instead of a real device. Produces a guaranteed-correct, SDK-matched image.

### 2. TFTP harness (host Toit, throwaway)
- Uses `~/workspaceToit/tftp/src/tftp_server.toit`.
- Serves a single file `"firmware"` whose contents are the blob:
  `[u32 size_le][u32 crc32_le][image bytes]`.
- `size` = length of image bytes; `crc32` = the captured `X-Jaguar-CRC32` value
  (reused verbatim — never recomputed, so the algorithm matches by construction).
- Raise TFTP block size (e.g. `--blksize=1024`) since the image is tens of KB.

### 3. Loader (ESP32 Toit container — the keeper)
- Installed as a **named** container via `jag container install loader loader.toit`
  so `run-installed-containers` restarts it on wake.
- Lifts `flash-image` from `jaguar/src/jaguar.toit` and the named-install bits from
  `container_registry.toit`, swapping jag's HTTP-body reader for a
  `~/workspaceToit/tftp/src/tftp_client.toit` reader.
- Loop:
  1. Bring up WiFi (STA, IPv4, joins home AP).
  2. `TFTPClient --host=<gateway-ip>`, `open`, read `"firmware"` -> Reader.
  3. Read first 8 bytes -> `size`, `crc32` (little-endian u32 each).
  4. Hand the *same* reader to lifted `flash-image size reader "payload" {:} --crc32=crc32`
     -> installs the container, returns its uuid.
  5. Start the payload container.
  6. `esp32.deep-sleep` for N seconds; on wake, repeat from step 1.

### 4. Payload (trivial Toit, captured via jag)
- Heartbeat printer: `print "delivered tick $n"` each second.
- Also installed as a **named** container so it persists across sleep.
- This is the observable proof the delivered image ran.

## Why the 8-byte self-describing header

The loader needs `image-size` (to construct `ContainerImageWriter image-size`) and
`crc32` (to verify) *before* streaming. Rather than depend on TFTP `tsize`/OACK,
the blob self-describes: read 8 bytes, then hand the same reader to `flash-image`
for the remaining `size` bytes. This mirrors how a real gateway poll would return
metadata + payload, and keeps delivery to a single TFTP read.

Header layout: `[0:4]` = image size, little-endian u32; `[4:8]` = CRC32-IEEE of
the image bytes, little-endian u32; `[8:]` = image bytes.

## Device packaging

- ESP32 flashed once with a jag Jaguar envelope, which pins the SDK version.
- Loader and payload are both **named installed** containers (persist across
  deep-sleep, auto-restart via `run-installed-containers`).
- WiFi stays on for the smoke test. The sleepy-node / no-AP-server win is
  explicitly deferred to sub-project 2 (custom envelope), not tested here.

## Milestones (stage the risk)

- **M1:** loader pulls `"firmware"` over TFTP -> `flash-image` -> payload heartbeat
  prints. *No deep-sleep.* This alone proves the crux.
- **M2:** add `esp32.deep-sleep` -> wake -> re-poll; confirm payload restarts and
  loader re-pulls.

## Risks & mitigations

| Risk | Mitigation |
|------|------------|
| Image SDK != device firmware SDK | Use the same jag/SDK to both flash the device and produce the image. Pin and record the version in the spike README. |
| CRC mismatch | Reuse the captured `X-Jaguar-CRC32` value verbatim; do not recompute. |
| `ContainerImageWriter` unavailable from a container context | Jaguar calls it from a container, so it is available; M1 confirms early. |
| Slow/failed transfer (tens of KB over 512-byte blocks) | Raise TFTP `--blksize` (e.g. 1024). |
| Re-pulling the image every wake is wasteful | Acceptable for the smoke test; note "skip if CRC unchanged" as a sub-project-2 optimization. |

## Out of scope (later sub-projects)

- **Sub-project 2:** build our own image (replicate `compile -> snapshot ->
  snapshot-to-image`), custom envelope with the loader as the system app (no
  Jaguar, no always-on server), CRC-skip optimization, real sleepy-node behavior.
- **A/C:** a Toit compile service (`POST /compile`, mirrors the ST compile service)
  and a dispatcher in jast-gw (route source -> compiler by node runtime; registry
  gains `runtime` + `chip` + `sdk_version`; queue image for TFTP delivery).

## Cross-cutting constraint

SDK-version match between the compile output and each node's firmware, per chip.
In the smoke test this is guaranteed by using one jag/SDK for both flashing and
image production. In production, the node must report its chip + SDK version in
its poll so the gateway can request a matching image.
