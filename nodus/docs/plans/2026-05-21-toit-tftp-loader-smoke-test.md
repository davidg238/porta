# Toit-over-TFTP Loader Smoke Test — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **Before writing any Toit:** invoke the Toit skills — `toit-conventions`, `toit-package`, `toit-jag-dev`, `toit-exe`, and `toit-envelope`. Read the design first: `../specs/2026-05-21-toit-tftp-loader-design.md`. Read `../../CLAUDE.md` for orientation and the `./st-zephyr` reference symlink.

**Goal:** Prove a relocated ESP32 container image, delivered over UDP/TFTP, flashes and runs on a classic ESP32 via `system.containers`, and survives a deep-sleep/wake/re-poll cycle.

**Architecture:** Borrow a known-good, SDK-matched image from `jag` (Approach 1). A throwaway host TFTP server serves it as a self-describing blob `[u32 size_le][u32 crc32_le][image]`. A Toit loader on the ESP32 reads the blob with `tftp_client.read-bytes`, parses the 8-byte header, wraps the image in an `io.Reader`, and installs it via a lifted-and-adapted `flash-image` (from `~/workspaceToit/jaguar`). M1 proves the mechanism without sleep; M2 adds deep-sleep + re-poll.

**Tech Stack:** Toit (device loader + host TFTP harness), Go or Python (one-time capture sink), `jag`/`toit` toolchain, classic ESP32 (Xtensa). Reuses `~/workspaceToit/{jaguar,tftp}`.

---

## Source references (read these, lift from them)

- `~/workspaceToit/jaguar/src/jaguar.toit` — `flash-image` (≈ line 211): the install loop using `containers.ContainerImageWriter` + CRC32 verify. **Lift and adapt** (change its reader type to `io.Reader`).
- `~/workspaceToit/jaguar/src/container_registry.toit` — `install`/`uninstall` named-container persistence (needed for M2).
- `~/workspaceToit/tftp/src/tftp_client.toit` — `read-bytes filename -> ByteArray`, `last-tsize`. Used by the loader.
- `~/workspaceToit/tftp/src/tftp_server.toit` — used by the host harness.
- CRC32 = standard CRC32-IEEE. Jaguar computes it as `crc.Crc.little-endian 32 --polynomial=0xEDB88320 --initial_state=0xffff_ffff --xor_result=0xffff_ffff`; jag's Go side uses `crc32.ChecksumIEEE`. We **reuse the captured value verbatim** and never recompute, so the algorithm matches by construction.

## File structure

- `host/capture_sink.go` (or `.py`) — accepts `PUT /run` & `/install`, saves body → `host/image`, saves `X-Jaguar-CRC32` → `host/image.crc32`. *Throwaway.*
- `host/blob.toit` — `frame-blob image/ByteArray crc32/int -> ByteArray` (prepend the 8-byte header). Shared format logic.
- `host/blob_test.toit` — round-trip test for `frame-blob` + `parse-header`.
- `host/serve.toit` — TFTP server (uses `tftp_server.toit`) that serves `firmware` = framed blob.
- `device/header.toit` — `parse-header bytes/ByteArray -> Header` (size + crc32, little-endian). Pure; unit-tested.
- `device/header_test.toit` — unit test for `parse-header`.
- `device/flash_image.toit` — lifted+adapted `flash-image image-size/int reader/io.Reader name/string? --crc32/int -> uuid.Uuid`.
- `device/loader.toit` — the poll → flash → run (→ sleep) loop. (Scaffold already present.)
- `device/hello.toit` — the heartbeat payload. (Already written.)

> `parse-header` lives in `device/header.toit`; `host/blob.toit` `frame-blob` is its inverse. Keep the byte layout identical: `[0:4]` size LE u32, `[4:8]` crc32 LE u32, `[8:]` image.

---

## Task 1: Device firmware + toolchain prerequisites

**Files:** none (environment + hardware).

- [ ] **Step 1: Confirm toolchain**

Run: `jag version && toit version`
Expected: both print a version. Record the exact `jag`/SDK version — every image MUST be built with this same SDK.

- [ ] **Step 2: Flash the classic ESP32 with a Jaguar firmware**

Run: `jag flash --chip esp32` (follow `toit-jag-dev`; pick the serial port when prompted)
Expected: flashing completes; device reboots.

- [ ] **Step 3: Verify the device is alive over serial**

Run: `jag monitor`
Expected: Jaguar boot log; device announces itself with an IP on your WiFi. Note the device IP (the gateway/host must be reachable from it).

- [ ] **Step 4: Record the SDK version**

Edit `../../CLAUDE.md` (or a `host/SDK_VERSION` file): write the `jag`/SDK version string from Step 1.

- [ ] **Step 5: Commit**

```bash
git add host/SDK_VERSION 2>/dev/null; git commit -am "chore: record pinned SDK version for the smoke test" || true
```

---

## Task 2: Capture a known-good image from jag

**Files:**
- Create: `host/capture_sink.go` (Go) or `host/capture_sink.py` (Python — your choice; Go example shown)
- Produces (gitignored): `host/image`, `host/image.crc32`

- [ ] **Step 1: Write the capture sink**

```go
// host/capture_sink.go — saves the image bytes and CRC32 that `jag run` would PUT.
package main

import ("fmt"; "io"; "net/http"; "os")

func main() {
	h := func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		os.WriteFile("image", body, 0644)
		os.WriteFile("image.crc32", []byte(r.Header.Get("X-Jaguar-CRC32")), 0644)
		fmt.Printf("captured %d bytes, crc32=%s on %s %s\n",
			len(body), r.Header.Get("X-Jaguar-CRC32"), r.Method, r.URL.Path)
		w.WriteHeader(200)
	}
	http.HandleFunc("/run", h)
	http.HandleFunc("/install", h)
	fmt.Println("capture sink on :8080")
	http.ListenAndServe(":8080", nil)
}
```

- [ ] **Step 2: Run the sink**

Run (from `host/`): `go run capture_sink.go`
Expected: `capture sink on :8080`.

- [ ] **Step 3: Make jag PUT the image to the sink**

The cleanest route is to register/point a jag "device" at `http://<host-ip>:8080` and run `jag run ../device/hello.toit` against it. If that proves fiddly, fall back to building the image directly with the same SDK and POSTing it to the sink:
`toit compile --snapshot -o /tmp/hello.snapshot ../device/hello.toit` then `toit tool snapshot-to-image` for `esp32`, then `curl -X PUT --data-binary @<image> -H "X-Jaguar-CRC32:$(...)" http://localhost:8080/install`.
Expected: the sink prints `captured N bytes, crc32=...`.

- [ ] **Step 4: Verify the artifacts exist**

Run (from `host/`): `ls -l image image.crc32 && cat image.crc32`
Expected: `image` is tens of KB; `image.crc32` is a decimal integer.

- [ ] **Step 5: Commit the sink (artifacts are gitignored)**

```bash
git add host/capture_sink.go && git commit -m "feat(host): jag image capture sink for the smoke test"
```

---

## Task 3: Blob framing codec (TDD)

**Files:**
- Create: `device/header.toit`, `device/header_test.toit`
- Create: `host/blob.toit`

- [ ] **Step 1: Write the failing test**

```toit
// device/header_test.toit
import expect show *
import .header

main:
  // size=5, crc32=0xAABBCCDD, then 5 image bytes
  blob := #[5,0,0,0, 0xDD,0xCC,0xBB,0xAA, 1,2,3,4,5]
  h := parse-header blob
  expect-equals 5 h.size
  expect-equals 0xAABBCCDD h.crc32
  expect-equals #[1,2,3,4,5] (blob[8 .. 8 + h.size])
```

- [ ] **Step 2: Run it to verify it fails**

Run (from `device/`): `toit test header_test.toit` (or per `toit-exe`)
Expected: FAIL — `header` / `parse-header` not defined.

- [ ] **Step 3: Implement `parse-header`**

```toit
// device/header.toit
import io show LITTLE-ENDIAN

class Header:
  size/int
  crc32/int
  constructor .size .crc32

parse-header bytes/ByteArray -> Header:
  if bytes.size < 8: throw "blob too short: $bytes.size < 8"
  size := LITTLE-ENDIAN.uint32 bytes 0
  crc32 := LITTLE-ENDIAN.uint32 bytes 4
  return Header size crc32
```

- [ ] **Step 4: Run it to verify it passes**

Run (from `device/`): `toit test header_test.toit`
Expected: PASS.

- [ ] **Step 5: Implement the host-side inverse**

```toit
// host/blob.toit
import io show LITTLE-ENDIAN

// Frame an image as [u32 size_le][u32 crc32_le][image].
frame-blob image/ByteArray crc32/int -> ByteArray:
  out := ByteArray 8 + image.size
  LITTLE-ENDIAN.put-uint32 out 0 image.size
  LITTLE-ENDIAN.put-uint32 out 4 crc32
  out.replace 8 image
  return out
```

- [ ] **Step 6: Commit**

```bash
git add device/header.toit device/header_test.toit host/blob.toit
git commit -m "feat: 8-byte self-describing blob header (size+crc32) codec + test"
```

---

## Task 4: Host TFTP harness

**Files:**
- Create: `host/serve.toit`

- [ ] **Step 1: Write the server**

Grounded in `~/workspaceToit/tftp/examples/server-host.toit`: write the framed blob to `<root>/firmware`, then serve that directory read-only with `FilesystemStorage` + `TFTPServer`.

```toit
// host/serve.toit — serves the captured image as the framed "firmware" blob.
import host.directory
import host.file
import log
import tftp show FilesystemStorage TFTPServer
import .blob

ROOT ::= "/tmp/porta-tftp"
PORT ::= 6969   // 69 needs root/cap_net_bind_service; loader must use this port

main:
  image := file.read-content "image"
  crc32 := int.parse (file.read-content "image.crc32").to-string.trim
  blob := frame-blob image crc32
  if not file.is-directory ROOT: directory.mkdir --recursive ROOT
  file.write-content blob --path="$ROOT/firmware"
  print "serving firmware: image=$image.size crc32=$crc32 blob=$blob.size on UDP/$PORT"
  storage := FilesystemStorage --root=ROOT --read-only=true
  server := TFTPServer --storage=storage --port=PORT --logger=log.default
  server.start
```

- [ ] **Step 2: Run it and self-check the framing**

Run (from `host/`): `toit run serve.toit`
Expected: `serving firmware: image=<tens-of-KB> crc32=<int> blob=<image+8> on UDP/6969`.

- [ ] **Step 3: Verify over TFTP from the host**

Run: fetch `firmware` with the bundled client `~/workspaceToit/tftp/examples/client-read-host.toit` (point it at `localhost:6969`, filename `firmware`), then `ls -l` the output.
Expected: output size == image size + 8.

- [ ] **Step 4: Commit**

```bash
git add host/serve.toit && git commit -m "feat(host): TFTP harness serving the framed firmware blob"
```

---

## Task 5: Lift & adapt `flash-image` for the device

**Files:**
- Create: `device/flash_image.toit`

- [ ] **Step 1: Copy `flash-image` from jaguar and adapt the reader type**

Lift the body of `flash-image` from `~/workspaceToit/jaguar/src/jaguar.toit` (≈ line 211). Adapt:
- Signature: `flash-image image-size/int reader/io.Reader name/string? --crc32/int -> uuid.Uuid` (use `io.Reader`, not the old `reader` package).
- Keep the `containers.ContainerImageWriter image-size` write loop and the CRC32 verify (`crc.Crc.little-endian 32 --polynomial=0xEDB88320 --initial_state=0xffff_ffff --xor_result=0xffff_ffff`).
- For M1, skip the named registry: do a transient install (just `ContainerImageWriter` + `commit`) and return the image id. (M2 reintroduces the registry.)

```toit
// device/flash_image.toit  (sketch — fill in from jaguar source)
import system.containers
import io
import crc

flash-image image-size/int reader/io.Reader name/string? --crc32/int -> any:
  summer := crc.Crc.little-endian 32 --polynomial=0xEDB88320
      --initial_state=0xffff_ffff --xor_result=0xffff_ffff
  written := 0
  writer := containers.ContainerImageWriter image-size
  while written < image-size:
    data := reader.read
    if not data: break
    summer.add data
    written += data.size
    writer.write data
  if summer.get-as-int != crc32:
    writer.close
    throw "CRC32 mismatch"
  image := writer.commit
  return image  // an id usable with containers.start
```

- [ ] **Step 2: Compile-check**

Run (from `device/`): `toit compile -o /dev/null flash_image.toit` (or `toit analyze`, per `toit-exe`)
Expected: compiles. Resolve any `containers`/`crc`/`io` API drift against the SDK now.

- [ ] **Step 3: Commit**

```bash
git add device/flash_image.toit
git commit -m "feat(device): lift flash-image from jaguar, adapt to io.Reader + transient install"
```

---

## Task 6 (M1): Loader pulls, flashes, runs — no sleep

**Files:**
- Modify: `device/loader.toit`

- [ ] **Step 1: Implement the M1 loop**

```toit
// device/loader.toit  (M1: no sleep)
import io
import system.containers
import tftp.tftp-client show TFTPClient  // confirm exact import via toit-code
import .header
import .flash_image show flash-image

GATEWAY-HOST ::= "192.168.1.50"  // TODO: set to the host running serve.toit

main:
  client := TFTPClient --host=GATEWAY-HOST
  client.open
  blob := client.read-bytes "firmware"
  client.close
  h := parse-header blob
  image-bytes := blob[8 .. 8 + h.size]
  print "loader: pulled blob=$blob.size image=$h.size crc32=$h.crc32"
  id := flash-image h.size (io.Reader image-bytes) "payload" --crc32=h.crc32
  print "loader: installed $id, starting"
  containers.start id
  print "loader: started payload"
```

- [ ] **Step 2: Start the host harness**

Run (from `host/`): `toit run serve.toit`
Expected: `serving firmware: ...` and it stays up.

- [ ] **Step 3: Install + run the loader on the device**

Run (from `device/`): `jag run loader.toit`
Expected serial output (via `jag monitor`):
```
loader: pulled blob=... image=... crc32=...
loader: installed <uuid>, starting
loader: started payload
delivered tick 0
delivered tick 1
...
```

- [ ] **Step 4: Confirm success criterion**

The `delivered tick N` lines prove the TFTP-delivered image installed and ran. If you see `CRC32 mismatch`, the captured crc32 and image are out of sync — re-capture (Task 2). If `ContainerImageWriter`/`containers.start` errors, note the exact error; this is the riskiest step.

- [ ] **Step 5: Commit**

```bash
git add device/loader.toit
git commit -m "feat(device): M1 loader — TFTP pull -> flash-image -> run payload"
```

---

## Task 7 (M2): Named install + deep-sleep + re-poll

**Files:**
- Modify: `device/flash_image.toit` (reintroduce named install)
- Modify: `device/loader.toit` (sleep loop)

- [ ] **Step 1: Reintroduce named persistence in `flash_image.toit`**

Port the named-install bits from `~/workspaceToit/jaguar/src/container_registry.toit` (`install name ...` keyed by name, backed by `system.storage`) so the payload is a **named installed** container that `run-installed-containers` restarts on boot. Reuse jaguar's logic; keep the name `"payload"`.

- [ ] **Step 2: Add the sleep loop to `loader.toit`**

```toit
// device/loader.toit  (M2: poll -> flash -> run -> deep-sleep -> wake -> repeat)
import esp32
// ... imports as in M1 ...

POLL-PERIOD ::= Duration --s=30

main:
  // pull + flash + start exactly as M1 (now via the named install) ...
  // give the payload a few seconds to print, then sleep:
  sleep --ms=5000
  print "loader: sleeping for $POLL-PERIOD"
  esp32.deep-sleep POLL-PERIOD
```

- [ ] **Step 3: Install loader as a named container and observe a wake cycle**

Run (from `device/`): `jag container install loader loader.toit`
Then `jag monitor`. Expected across one cycle:
```
loader: pulled ... / installed ... / started payload
delivered tick 0 .. delivered tick 4
loader: sleeping for 30s
<device deep-sleeps, serial quiet ~30s>
<wake/reboot banner>
delivered tick 0           # payload auto-restarted via run-installed-containers
loader: pulled ...         # loader re-polled
```

- [ ] **Step 4: Confirm M2 success criterion**

After a deep-sleep wake: (a) the payload heartbeat reappears (persistence proof), and (b) the loader re-polls. Both happening = M2 done.

- [ ] **Step 5: Commit**

```bash
git add device/flash_image.toit device/loader.toit
git commit -m "feat(device): M2 — named install persistence + deep-sleep re-poll loop"
```

---

## Notes for the implementer

- **Memory:** `read-bytes` buffers the whole blob in RAM. Fine for a tens-of-KB smoke test on classic ESP32. If you later hit memory limits, switch to `tftp_client.read --to-writer` streaming into a pipe feeding `ContainerImageWriter`; out of scope here.
- **Imports:** exact module paths for `tftp_client`/`tftp_server` and the `containers`/`crc`/`esp32` SDK APIs should be confirmed with the `toit-code` skill against the installed SDK — the sketches above may need import-path tweaks.
- **Re-pull every wake** is intentionally left in for the smoke test. "Skip if crc unchanged" is a sub-project-2 optimization.
- **WiFi** stays on throughout. The sleepy-node / no-AP-server win is sub-project 2 (custom envelope), not this plan.
