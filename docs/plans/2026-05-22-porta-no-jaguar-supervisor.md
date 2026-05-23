# Porta No-Jaguar Supervisor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the jaguar-dependent smoke-test loader with a no-jaguar **supervisor** that boots from a custom envelope, reconciles an Artemis-shaped goal-state over the LAN, installs/schedules container payloads by the Artemis trigger vocabulary, and persists across power-cycles via an NVS inventory.

**Architecture:** Host-testable pure logic (triggers, goal-state, inventory+reconcile, image streaming) is built first with TDD; then the minimal Toit gateway (reuses `FilesystemStorage` to serve a goal-state file + raw image); then device integration (transport seam, RTC+NVS storage, supervisor main loop); finally the custom-envelope build + `jag flash --exclude-jaguar` + on-device verification. The node↔gateway contract mirrors Artemis goal-state so the gateway is pod-feedable later.

**Tech Stack:** Toit (SDK v2.0.0-alpha.192), `system.containers`, `system.storage` (NVS `Bucket --flash`), `esp32` (deep-sleep / wakeup-cause / rtc-user-bytes), the `tftp` package (`~/workspaceToit/tftp`), prebuilt `firmware-esp32` envelope from `toitlang/envelopes`.

Spec: `docs/specs/2026-05-22-porta-no-jaguar-supervisor-design.md`.

---

## File Structure

**Device (`device/`) — created:**
- `triggers.toit` — Artemis trigger model: parse/serialize the `{type:value}` goal-state map; GPIO/touch wake masks.
- `goal_state.toit` — `App` + `GoalState`; JSON parse/serialize of the goal contract.
- `inventory.toit` — `InstalledApp`, `Inventory` (NVS-encodable), and `reconcile goal -> Reconciliation` (the CRC-skip decision).
- `image_writer.toit` — `ImageInstaller` interface + `ImageStreamWriter` (header-free streaming install + size/CRC verify). Replaces `blob_sink.toit`.
- `transport.toit` — `Transport`/`GatewayClient` seam + `WifiTransport`/`TftpGatewayClient` (wraps `TFTPClient`).
- `schedule_store.toit` — `clock-us` (wall-clock across deep-sleep) + `ScheduleStore` (RTC-backed last-poll timestamp).
- `supervisor.toit` — the boot container `main`: wake dispatch → poll/reconcile → start due consumers → deep-sleep. Replaces `loader.toit`.
- Tests: `triggers_test.toit`, `goal_state_test.toit`, `inventory_test.toit`, `image_writer_test.toit`.

**Device — modified:**
- `flash_image.toit` — `ContainerImageInstaller.commit` uses `--run-boot=false`, drops `JAGUAR-INSTALLED-MAGIC`; imports `image_writer` not `blob_sink`.

**Device — retired (deleted):** `header.toit`, `header_test.toit`, `blob_sink.toit`, `blob_sink_test.toit`, `loader.toit`.

**Host (`host/`) — created:**
- `goal.toit` — `build-goal` (constructs the goal-state JSON the gateway serves).
- `goal_test.toit`.

**Host — modified:**
- `serve.toit` — write `goal` (JSON) + `payload` (raw image, no framing) into ROOT, serve via `FilesystemStorage`.

**Host — retired (deleted):** `blob.toit`, `blob_test.toit`.

---

## Milestone A — Node contracts & pure logic (host-tested, TDD)

### Task 1: Triggers model

**Files:**
- Create: `device/triggers.toit`
- Test: `device/triggers_test.toit`

- [ ] **Step 1: Write the failing test**

```toit
// device/triggers_test.toit
import expect show *
import .triggers show Triggers

main:
  t := Triggers.parse {"boot": 1, "interval": 60, "gpio-high:33": 33, "gpio-touch:4": 4}
  expect t.boot
  expect-equals 60 t.interval-s
  expect-equals [33] t.gpio-high
  expect-equals [4] t.touch
  expect-equals (1 << 33) t.ext1-high-mask

  // round-trip through to-map
  t2 := Triggers.parse t.to-map
  expect t2.boot
  expect-equals 60 t2.interval-s
  expect-equals [33] t2.gpio-high

  // unknown trigger rejected
  expect-throw "unknown trigger: bogus": Triggers.parse {"bogus": 1}

  // empty triggers → all defaults
  e := Triggers.parse {:}
  expect-not e.boot
  expect-null e.interval-s
  expect-equals 0 e.ext1-high-mask
```

- [ ] **Step 2: Run test to verify it fails**

Run: `toit run device/triggers_test.toit`
Expected: FAIL — cannot resolve import `.triggers`.

- [ ] **Step 3: Write minimal implementation**

```toit
// device/triggers.toit
/**
Artemis-compatible container triggers, parsed from a goal-state trigger map of
  the form {"boot":1, "interval":60, "gpio-high:33":33, "gpio-touch:4":4}.
Reference: artemis/src/cli/pod-specification.toit:761-912.
*/
class Triggers:
  boot/bool
  install/int?
  interval-s/int?
  gpio-high/List
  gpio-low/List
  touch/List

  constructor --.boot=false --.install=null --.interval-s=null
      --.gpio-high=[] --.gpio-low=[] --.touch=[]:

  /** Parses the goal-state {type:value} trigger map. */
  static parse map/Map -> Triggers:
    boot := false
    install/int? := null
    interval-s/int? := null
    gpio-high := []
    gpio-low := []
    touch := []
    map.do: | key/string value |
      if key == "boot": boot = true
      else if key == "install": install = value
      else if key == "interval": interval-s = value
      else if key.starts-with "gpio-high:": gpio-high.add (int.parse key[10..])
      else if key.starts-with "gpio-low:": gpio-low.add (int.parse key[9..])
      else if key.starts-with "gpio-touch:": touch.add (int.parse key[11..])
      else: throw "unknown trigger: $key"
    return Triggers --boot=boot --install=install --interval-s=interval-s
        --gpio-high=gpio-high --gpio-low=gpio-low --touch=touch

  /** Serializes back to the goal-state {type:value} map form. */
  to-map -> Map:
    m := {:}
    if boot: m["boot"] = 1
    if install != null: m["install"] = install
    if interval-s != null: m["interval"] = interval-s
    gpio-high.do: m["gpio-high:$it"] = it
    gpio-low.do: m["gpio-low:$it"] = it
    touch.do: m["gpio-touch:$it"] = it
    return m

  /** Pin mask for esp32.enable-external-wakeup covering all gpio-high pins. */
  ext1-high-mask -> int:
    mask := 0
    gpio-high.do: mask |= 1 << it
    return mask
```

- [ ] **Step 4: Run test to verify it passes**

Run: `toit run device/triggers_test.toit`
Expected: PASS (no output / exit 0).

- [ ] **Step 5: Commit**

```bash
git add device/triggers.toit device/triggers_test.toit
git commit -m "feat(device): Artemis-compatible Triggers model + tests"
```

### Task 2: Goal-state model

**Files:**
- Create: `device/goal_state.toit`
- Test: `device/goal_state_test.toit`

- [ ] **Step 1: Write the failing test**

```toit
// device/goal_state_test.toit
import expect show *
import .goal_state show GoalState App

main:
  json := """{"apps":{"payload":{"size":38016,"crc":2157114022,
      "triggers":{"interval":60},"runlevel":3,"arguments":[]}}}"""
  g := GoalState.parse json.to-byte-array
  expect-equals 1 g.apps.size
  app/App := g.apps["payload"]
  expect-equals "payload" app.name
  expect-equals 38016 app.size
  expect-equals 2157114022 app.crc
  expect-equals 60 app.triggers.interval-s
  expect-equals 3 app.runlevel

  // round-trip: parse(to-json) preserves fields
  g2 := GoalState.parse g.to-json
  expect-equals 38016 (g2.apps["payload"] as App).size
  expect-equals 2157114022 (g2.apps["payload"] as App).crc
  expect-equals 60 (g2.apps["payload"] as App).triggers.interval-s

  // missing optional fields default
  g3 := GoalState.parse """{"apps":{"x":{"size":1,"crc":2}}}""".to-byte-array
  expect-equals 3 (g3.apps["x"] as App).runlevel
  expect-equals [] (g3.apps["x"] as App).arguments
```

- [ ] **Step 2: Run test to verify it fails**

Run: `toit run device/goal_state_test.toit`
Expected: FAIL — cannot resolve import `.goal_state`.

- [ ] **Step 3: Write minimal implementation**

```toit
// device/goal_state.toit
import encoding.json
import .triggers show Triggers

/** One application container in a goal-state. */
class App:
  name/string
  size/int       // image bytes; sizes the ContainerImageWriter
  crc/int        // CRC32-IEEE of the image; change-detection + verify
  triggers/Triggers
  runlevel/int
  arguments/List

  constructor --.name --.size --.crc --.triggers --.runlevel=3 --.arguments=[]:

/**
A desired-state goal: the apps a node should run. Mirrors Artemis
  device-config["apps"] (artemis/src/cli/broker.toit:1006-1030) plus the
  size/crc Porta needs for a streaming install.
*/
class GoalState:
  apps/Map  // name/string -> App

  constructor .apps:

  static parse bytes/ByteArray -> GoalState:
    obj := json.decode bytes
    apps := {:}
    (obj.get "apps" --if-absent=: {:}).do: | name/string spec/Map |
      apps[name] = App
          --name=name
          --size=spec["size"]
          --crc=spec["crc"]
          --triggers=(Triggers.parse (spec.get "triggers" --if-absent=: {:}))
          --runlevel=(spec.get "runlevel" --if-absent=: 3)
          --arguments=(spec.get "arguments" --if-absent=: [])
    return GoalState apps

  to-json -> ByteArray:
    apps-map := {:}
    apps.do: | name/string app/App |
      apps-map[name] = {
        "size": app.size,
        "crc": app.crc,
        "triggers": app.triggers.to-map,
        "runlevel": app.runlevel,
        "arguments": app.arguments,
      }
    return json.encode {"apps": apps-map}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `toit run device/goal_state_test.toit`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add device/goal_state.toit device/goal_state_test.toit
git commit -m "feat(device): Artemis-shaped GoalState/App JSON model + tests"
```

### Task 3: Inventory + reconcile

**Files:**
- Create: `device/inventory.toit`
- Test: `device/inventory_test.toit`

- [ ] **Step 1: Write the failing test**

```toit
// device/inventory_test.toit
import expect show *
import uuid
import .goal_state show GoalState App
import .triggers show Triggers
import .inventory show Inventory InstalledApp

make-goal crc/int -> GoalState:
  return GoalState {
    "payload": App --name="payload" --size=10 --crc=crc
        --triggers=(Triggers --interval-s=60) --runlevel=3
  }

main:
  id := uuid.Uuid.uuid5 "porta" "x"

  // empty inventory → everything must be fetched
  r0 := Inventory.empty.reconcile (make-goal 111)
  expect-equals 1 r0.to-fetch.size
  expect-equals 0 r0.to-schedule.size

  // matching crc → schedule from flash, no fetch
  inv := Inventory {
    "payload": InstalledApp --name="payload" --id=id --size=10 --crc=111
        --triggers=(Triggers --interval-s=60) --runlevel=3
  }
  r1 := inv.reconcile (make-goal 111)
  expect-equals 0 r1.to-fetch.size
  expect-equals 1 r1.to-schedule.size

  // changed crc → fetch
  r2 := inv.reconcile (make-goal 222)
  expect-equals 1 r2.to-fetch.size

  // app removed from goal → to-remove
  r3 := inv.reconcile (GoalState {:})
  expect-equals 1 r3.to-remove.size

  // encode/decode round-trip
  tree := inv.encode
  back := Inventory.decode tree
  expect-equals 111 (back.apps["payload"] as InstalledApp).crc
  expect-equals id (back.apps["payload"] as InstalledApp).id
  expect-equals 60 (back.apps["payload"] as InstalledApp).triggers.interval-s
```

- [ ] **Step 2: Run test to verify it fails**

Run: `toit run device/inventory_test.toit`
Expected: FAIL — cannot resolve import `.inventory`.

- [ ] **Step 3: Write minimal implementation**

```toit
// device/inventory.toit
import uuid
import .goal_state show GoalState App
import .triggers show Triggers

/** A container currently installed on the node, as recorded in NVS. */
class InstalledApp:
  name/string
  id/uuid.Uuid   // committed image id
  size/int
  crc/int
  triggers/Triggers
  runlevel/int

  constructor --.name --.id --.size --.crc --.triggers --.runlevel:

/** What the supervisor must do to match a goal. */
class Reconciliation:
  to-fetch/List     // App   — new or crc-changed: download + install
  to-schedule/List  // InstalledApp — unchanged: start from flash
  to-remove/List    // InstalledApp — in inventory, absent from goal

  constructor --.to-fetch --.to-schedule --.to-remove:

/** The node's persistent inventory of installed apps (NVS-encodable). */
class Inventory:
  apps/Map  // name -> InstalledApp

  constructor .apps:

  static empty -> Inventory: return Inventory {:}

  /** Decodes the plain Map/List tree produced by $encode (as stored in NVS). */
  static decode tree/Map -> Inventory:
    apps := {:}
    (tree.get "apps" --if-absent=: {:}).do: | name/string m/Map |
      apps[name] = InstalledApp
          --name=name
          --id=(uuid.Uuid m["id"])
          --size=m["size"]
          --crc=m["crc"]
          --triggers=(Triggers.parse m["triggers"])
          --runlevel=m["runlevel"]
    return Inventory apps

  encode -> Map:
    m := {:}
    apps.do: | name/string a/InstalledApp |
      m[name] = {
        "id": a.id.to-byte-array,
        "size": a.size,
        "crc": a.crc,
        "triggers": a.triggers.to-map,
        "runlevel": a.runlevel,
      }
    return {"apps": m}

  reconcile goal/GoalState -> Reconciliation:
    to-fetch := []
    to-schedule := []
    to-remove := []
    goal.apps.do: | name/string app/App |
      installed/InstalledApp? := apps.get name
      if installed != null and installed.crc == app.crc:
        to-schedule.add installed
      else:
        to-fetch.add app
    apps.do: | name/string installed/InstalledApp |
      if not goal.apps.contains name: to-remove.add installed
    return Reconciliation --to-fetch=to-fetch --to-schedule=to-schedule --to-remove=to-remove
```

- [ ] **Step 4: Run test to verify it passes**

Run: `toit run device/inventory_test.toit`
Expected: PASS. (If `uuid.Uuid`'s byte constructor or `to-byte-array` name differs in this SDK, adjust both call sites consistently — the test will tell you.)

- [ ] **Step 5: Commit**

```bash
git add device/inventory.toit device/inventory_test.toit
git commit -m "feat(device): NVS Inventory + goal reconcile (CRC-skip) + tests"
```

### Task 4: Header-free image stream writer (replaces blob_sink)

**Files:**
- Create: `device/image_writer.toit`, `device/image_writer_test.toit`
- Delete: `device/blob_sink.toit`, `device/blob_sink_test.toit`, `device/header.toit`, `device/header_test.toit`

- [ ] **Step 1: Write the failing test**

```toit
// device/image_writer_test.toit
import expect show *
import io
import crypto.crc
import uuid
import .image_writer show ImageStreamWriter ImageInstaller

class FakeInstaller implements ImageInstaller:
  begun-size/int := -1
  buf_/io.Buffer := io.Buffer
  committed/bool := false
  aborted/bool := false
  result_/uuid.Uuid := uuid.Uuid.uuid5 "porta-test" "fake"
  begin size/int -> none: begun-size = size
  write chunk/ByteArray -> none: buf_.write chunk
  commit -> uuid.Uuid: committed = true; return result_
  abort -> none: aborted = true
  image -> ByteArray: return buf_.bytes

crc-of image/ByteArray -> int:
  s := crc.Crc.little-endian 32 --polynomial=0xEDB88320
      --initial-state=0xffff_ffff --xor-result=0xffff_ffff
  s.add image
  return s.get-as-int

feed w/ImageStreamWriter bytes/ByteArray chunk/int -> none:
  i := 0
  while i < bytes.size:
    j := min bytes.size (i + chunk)
    w.write bytes[i .. j]
    i = j

main:
  image := ByteArray 1000: it & 0xff
  good := crc-of image

  // happy path: streamed in 128-byte blocks, size+crc verified
  fi := FakeInstaller
  w := ImageStreamWriter fi --size=image.size --crc=good
  expect-equals image.size fi.begun-size   // begin called at construction
  feed w image 128
  id := w.commit
  expect fi.committed
  expect-equals image (fi.image)
  expect-equals fi.result_ id

  // crc mismatch → abort + throw
  fi2 := FakeInstaller
  w2 := ImageStreamWriter fi2 --size=image.size --crc=(good ^ 0x1)
  feed w2 image 128
  expect-throw "CRC32 mismatch": w2.commit
  expect fi2.aborted

  // truncated → abort + throw
  fi3 := FakeInstaller
  w3 := ImageStreamWriter fi3 --size=image.size --crc=good
  feed w3 image[0..500] 128
  expect-throw "truncated stream: expected 1000 bytes, got 500": w3.commit
  expect fi3.aborted
```

- [ ] **Step 2: Run test to verify it fails**

Run: `toit run device/image_writer_test.toit`
Expected: FAIL — cannot resolve import `.image_writer`.

- [ ] **Step 3: Write minimal implementation**

```toit
// device/image_writer.toit
import crypto.crc
import io
import uuid

/**
Sink for image bytes: begin once (with size), write in order, then exactly one
  of commit / abort. Backed on-device by a ContainerImageWriter; backed in tests
  by an in-memory recorder.
*/
interface ImageInstaller:
  begin size/int -> none
  write chunk/ByteArray -> none
  commit -> uuid.Uuid
  abort -> none

/**
An io.Writer that streams a raw container image into an $ImageInstaller and
  verifies length + CRC32-IEEE on $commit. Size and CRC come from the goal (the
  former self-describing 8-byte blob header is gone — metadata rides in the
  command now). Live memory is bounded to one block.
*/
class ImageStreamWriter extends io.Writer:
  installer_/ImageInstaller
  expected-size_/int
  expected-crc_/int
  written_/int := 0
  summer_/crc.Crc := crc.Crc.little-endian 32 --polynomial=0xEDB88320
      --initial-state=0xffff_ffff --xor-result=0xffff_ffff

  constructor .installer_ --size/int --crc/int:
    expected-size_ = size
    expected-crc_ = crc
    installer_.begin size

  try-write_ data/io.Data from/int to/int -> int:
    // Copy out: ContainerImageWriter.write neuters its argument, so the chunk
    // must not alias data the TFTP layer still owns.
    chunk := ByteArray (to - from)
    chunk.replace 0 data from to
    summer_.add chunk
    written_ += chunk.size
    installer_.write chunk
    return to - from

  commit -> uuid.Uuid:
    if written_ != expected-size_:
      installer_.abort
      throw "truncated stream: expected $expected-size_ bytes, got $written_"
    if summer_.get-as-int != expected-crc_:
      installer_.abort
      throw "CRC32 mismatch"
    return installer_.commit

  abort -> none:
    installer_.abort
```

- [ ] **Step 4: Run test to verify it passes**

Run: `toit run device/image_writer_test.toit`
Expected: PASS.

- [ ] **Step 5: Delete the retired header/blob modules + tests**

```bash
git rm device/blob_sink.toit device/blob_sink_test.toit device/header.toit device/header_test.toit
```

- [ ] **Step 6: Commit**

```bash
git add device/image_writer.toit device/image_writer_test.toit
git commit -m "feat(device): header-free ImageStreamWriter; retire blob/header modules"
```

---

## Milestone B — Minimal gateway (host-runnable)

### Task 5: Host goal builder

**Files:**
- Create: `host/goal.toit`, `host/goal_test.toit`

- [ ] **Step 1: Write the failing test**

```toit
// host/goal_test.toit
import expect show *
import encoding.json
import .goal show build-goal

main:
  bytes := build-goal --name="payload" --size=38016 --crc=2157114022 --interval-s=5
  obj := json.decode bytes
  app := obj["apps"]["payload"]
  expect-equals 38016 app["size"]
  expect-equals 2157114022 app["crc"]
  expect-equals 5 app["triggers"]["interval"]
  expect-equals 3 app["runlevel"]
```

- [ ] **Step 2: Run test to verify it fails**

Run: `toit run host/goal_test.toit`
Expected: FAIL — cannot resolve import `.goal`.

- [ ] **Step 3: Write minimal implementation**

```toit
// host/goal.toit
import encoding.json

/**
Builds the goal-state JSON the minimal gateway serves at "goal". One app named
  $name, with a single interval trigger. Mirrors the Porta goal contract in
  docs/specs/2026-05-22-...; the node fetches the image by the app's name.
*/
build-goal --name/string --size/int --crc/int --interval-s/int -> ByteArray:
  return json.encode {
    "apps": {
      name: {
        "size": size,
        "crc": crc,
        "triggers": {"interval": interval-s},
        "runlevel": 3,
        "arguments": [],
      },
    },
  }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `toit run host/goal_test.toit`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add host/goal.toit host/goal_test.toit
git commit -m "feat(host): goal-state JSON builder + test"
```

### Task 6: Rewrite the gateway server (serve goal + raw image)

**Files:**
- Modify: `host/serve.toit`
- Delete: `host/blob.toit`, `host/blob_test.toit`

- [ ] **Step 1: Replace `host/serve.toit`**

```toit
// host/serve.toit — minimal Porta gateway: serves a goal-state + raw image.
import host.directory
import host.file
import log
import tftp show FilesystemStorage TFTPServer
import .goal show build-goal

/** Root directory for the TFTP filesystem storage. */
ROOT ::= "/tmp/porta-tftp"

/** Unprivileged UDP port (69 needs root). The supervisor must match this. */
PORT ::= 6969

/** App name; the node fetches the image under this filename. */
PAYLOAD-NAME ::= "payload"

/** Interval (seconds) advertised in the goal — fast, to observe multi-rate. */
PAYLOAD-INTERVAL-S ::= 5

/**
Serves the captured image as the raw "payload" file plus a "goal" file in the
  Artemis-shaped goal-state format. No blob framing — size+crc ride in the goal.
*/
main:
  image := file.read-contents "image"
  crc32 := int.parse (file.read-contents "image.crc32").to-string.trim
  goal := build-goal --name=PAYLOAD-NAME --size=image.size --crc=crc32
      --interval-s=PAYLOAD-INTERVAL-S
  if not file.is-directory ROOT: directory.mkdir --recursive ROOT
  file.write-contents image --path="$ROOT/$PAYLOAD-NAME"
  file.write-contents goal --path="$ROOT/goal"
  print "serving goal ($goal.size B) + $PAYLOAD-NAME (image=$image.size crc32=$crc32) on UDP/$PORT"
  storage := FilesystemStorage --root=ROOT --read-only
  server := TFTPServer --storage=storage --port=PORT --logger=log.default
  server.start
```

- [ ] **Step 2: Delete retired blob framing**

```bash
git rm host/blob.toit host/blob_test.toit
```

- [ ] **Step 3: Verify the gateway serves correctly**

Run (from `host/`, where `image` + `image.crc32` already exist):
```bash
cd host && toit run serve.toit &
sleep 1
# Fetch goal + payload back over TFTP using the python tftp client or curl-tftp:
# Simplest cross-check — confirm the files were written with no framing:
ls -l /tmp/porta-tftp/goal /tmp/porta-tftp/payload
cmp /tmp/porta-tftp/payload image && echo "payload == image (no framing) OK"
kill %1
```
Expected: `payload == image (no framing) OK`, `goal` is a small JSON file, and the server prints the `serving goal …` line.

- [ ] **Step 4: Commit**

```bash
git add host/serve.toit
git commit -m "feat(host): serve goal-state + raw image; drop blob framing"
```

---

## Milestone C — Device integration

> These touch hardware-only APIs (`system.containers`, `esp32`, WiFi). They are
> verified by `toit analyze` (compiles clean) here and end-to-end on hardware in
> Milestone D. There are no host unit tests for these.

### Task 7: Flash installer — no jaguar magic, supervisor-owned start

**Files:**
- Modify: `device/flash_image.toit`

- [ ] **Step 1: Replace `device/flash_image.toit`**

```toit
// device/flash_image.toit
import system.containers
import uuid

import .image_writer show ImageInstaller

/**
On-device image installer adapted from jaguar's flash-image, reshaped as a
  push-style $ImageInstaller so an $ImageStreamWriter streams a TFTP transfer
  straight into flash without buffering the whole image.

Commits with `--run-boot=false`: the supervisor — not the firmware — owns
  starting containers (no JAGUAR-INSTALLED-MAGIC, no auto-restart on boot). The
  committed image still persists in the flash registry across power-cycles.
*/
class ContainerImageInstaller implements ImageInstaller:
  writer_/containers.ContainerImageWriter? := null

  begin size/int -> none:
    writer_ = containers.ContainerImageWriter size

  write chunk/ByteArray -> none:
    writer_.write chunk

  commit -> uuid.Uuid:
    result := writer_.commit --run-boot=false
    writer_ = null  // later abort (e.g. from a finally) becomes a no-op
    return result

  abort -> none:
    if writer_ != null:
      writer_.close
      writer_ = null
```

- [ ] **Step 2: Verify it compiles clean**

Run: `toit analyze device/flash_image.toit`
Expected: no errors. (If `commit --run-boot=false` is rejected, check the `ContainerImageWriter.commit` signature in the SDK — `--run-boot` is the boot-trigger flag.)

- [ ] **Step 3: Commit**

```bash
git add device/flash_image.toit
git commit -m "feat(device): commit images with run-boot=false; drop jaguar magic"
```

### Task 8: Transport seam (Wifi + TFTP)

**Files:**
- Create: `device/transport.toit`

- [ ] **Step 1: Write `device/transport.toit`**

```toit
// device/transport.toit
import io
import tftp show TFTPClient

/** Fetches named resources from the gateway over some transport. */
interface GatewayClient:
  /** Reads a small resource fully into memory (e.g. the goal-state). */
  fetch-bytes name/string -> ByteArray
  /** Streams a resource into $to-writer (e.g. an image). Returns bytes read. */
  fetch name/string --to-writer/io.Writer -> int
  close -> none

/**
Brings up a link and yields a $GatewayClient. WiFi is the only transport this
  milestone; ESPnow/bt-mesh implement this same interface later (see the spec's
  transport seam).
*/
interface Transport:
  connect -> GatewayClient

/** WiFi transport: link comes up inside TFTPClient.open (net.open). */
class WifiTransport implements Transport:
  host_/string
  port_/int

  constructor --host/string --port/int:
    host_ = host
    port_ = port

  connect -> GatewayClient:
    client := TFTPClient --host=host_
    client.port = port_   // TFTPClient has no --port ctor arg; set before open.
    client.open
    return TftpGatewayClient client

/** GatewayClient backed by the tftp package's TFTPClient. */
class TftpGatewayClient implements GatewayClient:
  client_/TFTPClient

  constructor .client_:

  fetch-bytes name/string -> ByteArray:
    return client_.read-bytes name

  fetch name/string --to-writer/io.Writer -> int:
    return client_.read name --to-writer=to-writer

  close -> none:
    client_.close
```

- [ ] **Step 2: Verify it compiles clean**

Run: `toit analyze device/transport.toit`
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add device/transport.toit
git commit -m "feat(device): Transport/GatewayClient seam + WifiTransport (TFTP)"
```

### Task 9: Schedule store (wall-clock + RTC last-poll)

**Files:**
- Create: `device/schedule_store.toit`

- [ ] **Step 1: Write `device/schedule_store.toit`**

```toit
// device/schedule_store.toit
import esp32
import io show LITTLE-ENDIAN

/**
Wall-clock microseconds that advance across deep-sleep. Time.monotonic-us resets
  to ~0 on each deep-sleep wake; esp32.total-deep-sleep-time accumulates the time
  spent asleep, so their sum is a monotonic clock across sleep cycles.
*/
clock-us -> int:
  return Time.monotonic-us + esp32.total-deep-sleep-time

/**
RTC-backed scheduler state. RTC user memory survives deep-sleep but NOT a cold
  power-cycle; a 4-byte magic distinguishes "valid (woke from sleep)" from
  "cold boot" (treated as last-poll = 0, forcing a poll). Layout:
    [0:4]  magic
    [4:12] last-poll clock-us (int64, little-endian)
*/
class ScheduleStore:
  static MAGIC_ ::= 0x50_4f_52_54  // "PORT"
  rtc_/ByteArray

  constructor:
    rtc_ = esp32.rtc-user-bytes
    if (LITTLE-ENDIAN.uint32 rtc_ 0) != MAGIC_:
      // Cold boot: initialise. last-poll = 0 so the first wake polls.
      LITTLE-ENDIAN.put-uint32 rtc_ 0 MAGIC_
      LITTLE-ENDIAN.put-int64 rtc_ 4 0

  last-poll-us -> int:
    return LITTLE-ENDIAN.int64 rtc_ 4

  last-poll-us= value/int -> none:
    LITTLE-ENDIAN.put-int64 rtc_ 4 value
```

- [ ] **Step 2: Verify it compiles clean**

Run: `toit analyze device/schedule_store.toit`
Expected: no errors. (If `Time.monotonic-us` is spelled differently, the analyzer will flag it — it lives in core `Time`.)

- [ ] **Step 3: Commit**

```bash
git add device/schedule_store.toit
git commit -m "feat(device): RTC-backed schedule store + cross-sleep wall-clock"
```

### Task 10: Supervisor main loop

**Files:**
- Create: `device/supervisor.toit`
- Delete: `device/loader.toit`

- [ ] **Step 1: Write `device/supervisor.toit`**

```toit
// device/supervisor.toit
import esp32
import storage
import system.containers

import .goal_state show GoalState
import .inventory show Inventory InstalledApp
import .image_writer show ImageStreamWriter
import .flash_image show ContainerImageInstaller
import .transport show WifiTransport GatewayClient
import .schedule_store show ScheduleStore clock-us

/** Gateway LAN address. Adjust to the host running host/serve.toit. */
GATEWAY-HOST ::= "192.168.0.175"
GATEWAY-PORT ::= 6969

/** How often to poll the gateway for goal changes. */
POLL-PERIOD ::= Duration --s=30
/** How long to stay awake observing started payloads before sleeping. */
OBSERVE ::= Duration --s=5
/** Deep-sleep duration between wakes. */
SLEEP ::= Duration --s=30

/** NVS bucket + key holding the persistent inventory. */
BUCKET-NAME ::= "porta"
INVENTORY-KEY ::= "inventory"

/**
One supervisor wake: dispatch by wake cause, poll/reconcile if due, start the
  installed payloads, observe, then deep-sleep. Deep-sleep wakes via full reboot,
  so the loop is the reboot; $main is linear.
*/
main:
  cause := esp32.wakeup-cause
  print "supervisor: awake (cause=$cause)"

  bucket := storage.Bucket.open --flash BUCKET-NAME
  inventory := load-inventory bucket
  store := ScheduleStore
  now := clock-us

  // Poll on cold boot (empty inventory) or when the poll period has elapsed.
  cold := inventory.apps.is-empty
  poll-due := cold or (now - store.last-poll-us) >= POLL-PERIOD.in-us
  if poll-due:
    // Never strand the device awake on a transient failure: trace and still sleep.
    catch --trace:
      inventory = poll-and-reconcile bucket inventory
      store.last-poll-us = now

  start-installed inventory
  arm-wakeups inventory

  print "supervisor: observing for $OBSERVE"
  sleep OBSERVE
  print "supervisor: deep-sleeping for $SLEEP"
  esp32.deep-sleep SLEEP

/** Loads the inventory from NVS, or an empty one if none/garbage. */
load-inventory bucket/storage.Bucket -> Inventory:
  tree := bucket.get INVENTORY-KEY --if-absent=: null
  if tree == null: return Inventory.empty
  return Inventory.decode tree

save-inventory bucket/storage.Bucket inventory/Inventory -> none:
  bucket[INVENTORY-KEY] = inventory.encode

/** Fetches the goal, installs new/changed images, returns the updated inventory. */
poll-and-reconcile bucket/storage.Bucket inventory/Inventory -> Inventory:
  print "supervisor: polling $GATEWAY-HOST:$GATEWAY-PORT"
  client/GatewayClient := (WifiTransport --host=GATEWAY-HOST --port=GATEWAY-PORT).connect
  try:
    goal := GoalState.parse (client.fetch-bytes "goal")
    recon := inventory.reconcile goal
    recon.to-schedule.do: | a/InstalledApp | print "supervisor: $a.name unchanged (crc=$a.crc)"
    recon.to-fetch.do: | app |
      print "supervisor: fetching $app.name ($app.size B, crc=$app.crc)"
      installer := ContainerImageInstaller
      writer := ImageStreamWriter installer --size=app.size --crc=app.crc
      client.fetch app.name --to-writer=writer
      id := writer.commit
      inventory.apps[app.name] = InstalledApp --name=app.name --id=id
          --size=app.size --crc=app.crc --triggers=app.triggers --runlevel=app.runlevel
      print "supervisor: installed $app.name -> $id"
    save-inventory bucket inventory
    return inventory
  finally:
    client.close

/** Starts every installed app (deep-sleep cleared running state each wake). */
start-installed inventory/Inventory -> none:
  inventory.apps.do: | name/string a/InstalledApp |
    containers.start a.id
    print "supervisor: started $name ($a.id)"

/** Re-arms GPIO (ext1) wake sources declared by installed apps' triggers. */
arm-wakeups inventory/Inventory -> none:
  mask := 0
  inventory.apps.do: | _ a/InstalledApp | mask |= a.triggers.ext1-high-mask
  if mask != 0:
    esp32.enable-external-wakeup mask true
    print "supervisor: armed ext1 wake mask=0x$(%x mask)"
```

- [ ] **Step 2: Delete the retired loader**

```bash
git rm device/loader.toit
```

- [ ] **Step 3: Verify it compiles clean**

Run: `toit analyze device/supervisor.toit`
Expected: no errors. Fix any signature mismatches surfaced (e.g. `bucket.get … --if-absent`, `containers.start`).

- [ ] **Step 4: Commit**

```bash
git add device/supervisor.toit
git commit -m "feat(device): no-jaguar supervisor — poll/reconcile/start/deep-sleep"
```

---

## Milestone D — Custom envelope, flash, and on-device verification

### Task 11: Build the no-jaguar envelope, flash, and verify the full cycle

**Files:**
- Create: `host/build-envelope.sh` (build helper)

- [ ] **Step 1: Write the build helper**

```bash
# host/build-envelope.sh — builds a no-jaguar envelope with the supervisor.
# Run from the porta repo root. Requires toit + jag on PATH (SDK v2.0.0-alpha.192).
set -euo pipefail
SDK_VERSION="v2.0.0-alpha.192"
ENV="firmware-esp32.envelope"

# 1. Prebuilt, SDK-matched envelope.
if [ ! -f "$ENV" ]; then
  curl -L -o "$ENV.gz" \
    "https://github.com/toitlang/envelopes/releases/download/$SDK_VERSION/firmware-esp32.envelope.gz"
  gunzip "$ENV.gz"
fi

# 2. Supervisor → 32-bit binary image (classic ESP32 is 32-bit).
toit compile -s -o supervisor.snapshot device/supervisor.toit
toit tool snapshot-to-image -m32 --format=binary -o supervisor.image supervisor.snapshot

# 3. Install supervisor as the (sole) boot container.
toit tool firmware -e "$ENV" container install supervisor supervisor.image

# 4. Show contents (expect 'supervisor' present, no 'jaguar').
toit tool firmware -e "$ENV" show
```

- [ ] **Step 2: Build the envelope**

Run: `bash host/build-envelope.sh`
Expected: `firmware … show` lists a `supervisor` container and **no** `jaguar` container.

- [ ] **Step 3: Start the gateway**

Run (separate terminal, leave running):
```bash
cd host && toit run serve.toit
```
Expected: `serving goal (… B) + payload (image=38016 crc32=2157114022) on UDP/6969`.

- [ ] **Step 4: Flash without jaguar, provisioning WiFi**

Run (replace SSID/PW/port):
```bash
jag flash firmware-esp32.envelope --exclude-jaguar \
  --wifi-ssid "<SSID>" --wifi-password "<PW>" --port /dev/ttyUSB0
```
Expected: flash completes; device reboots into the supervisor.

- [ ] **Step 5: Observe the first (cold-boot) cycle**

Run: `jag monitor`
Expected serial sequence:
```
supervisor: awake (cause=…)
supervisor: polling 192.168.0.175:6969
supervisor: fetching payload (38016 B, crc=2157114022)
supervisor: installed payload -> <uuid>
supervisor: started payload (<uuid>)
delivered tick 0
delivered tick 1
...
supervisor: deep-sleeping for 30s
```
This proves: no-jaguar boot → WiFi from flash-time creds → goal poll → image install → payload runs. ✅ first-provisioning.

- [ ] **Step 6: Verify no-redownload on the next wake**

Keep `jag monitor` running across one deep-sleep wake (~35 s).
Expected on the second wake: `supervisor: payload unchanged (crc=2157114022)` and `started payload` with **no** "fetching payload" line — the persisted image is relaunched without a download. ✅ CRC-skip.

- [ ] **Step 7: Verify cold-boot autonomy (offline resume)**

Stop the gateway (`Ctrl-C` in its terminal), then physically power-cycle the device (unplug/replug, or press EN). Re-attach `jag monitor`.
Expected: because NVS still holds the inventory, the supervisor restarts the persisted payload. (On a *true* cold boot RTC is cleared so `poll-due` is true and the poll will fail with the gateway down — caught/traced — but `start-installed` still runs the payload from flash.) Confirm `delivered tick N` reappears with the gateway offline. ✅ offline autonomy after first provisioning.

- [ ] **Step 8: Commit**

```bash
git add host/build-envelope.sh
git commit -m "feat(host): no-jaguar envelope build helper + verified full cycle"
```

- [ ] **Step 9: Update the project memory**

Update `memory/toit-tftp-loader-state.md` (and its `MEMORY.md` pointer) to record: supervisor milestone done, device now runs the no-jaguar envelope (supervisor boot container, jaguar absent), gateway serves goal+payload. Note any deviations found during on-device bring-up.

---

## Self-Review

**Spec coverage:**
- No-jaguar prebuilt .192+BT envelope + `--exclude-jaguar` → Task 11.
- Supervisor as sole boot container, `trigger=none` consumers (run-boot=false) → Tasks 7, 10.
- Artemis goal-state + trigger vocabulary contract → Tasks 1, 2.
- Reconcile / CRC-skip / no-redownload → Tasks 3, 10; verified Step 6.
- NVS inventory + offline autonomy → Tasks 3, 9, 10; verified Step 7.
- Drop 8-byte header; metadata in command → Tasks 4, 5, 6.
- Transport seam (Transport/Channel; WifiTransport now) → Task 8. *Deviation:* the seam is realized at the `GatewayClient` level wrapping `TFTPClient`, not by splitting the tftp package's internal block engine — the deeper split lands when `EspnowChannel` is actually built (spec marks ESPnow out of scope). Recorded here intentionally.
- Minimal gateway serves hand-crafted goal-state + image → Tasks 5, 6.
- Wake-reason dispatch / deep-sleep ownership → Task 10 (`cause`, `arm-wakeups`, `deep-sleep`).

**Deferred per spec (no tasks, intentionally):** real `.pod` ingestion, topology registry, VPN, MCP, fleet/group mapping, ESPnow/bt-mesh, whole-firmware/diff OTA, gateway sqlite store, polyglot unification, signing.

**Placeholder scan:** `<SSID>`/`<PW>`/`<uuid>`/`<port>` are intentional runtime fillers in shell/serial examples, not plan gaps.

**Type consistency:** `ImageInstaller`/`ImageStreamWriter` (Task 4) consumed by `ContainerImageInstaller` (Task 7) and supervisor (Task 10); `Triggers` (Task 1) used by `App`/`GoalState` (Task 2), `InstalledApp`/`Inventory` (Task 3), and `arm-wakeups` (Task 10); `GoalState.parse`/`App.{size,crc,triggers,runlevel}` consistent across Tasks 2/3/10; `GatewayClient.{fetch-bytes,fetch,close}` defined Task 8, used Task 10; `ScheduleStore.last-poll-us` + `clock-us` defined Task 9, used Task 10. Consistent.

**Known SDK-name risks to confirm at execution (analyzer will surface):** `uuid.Uuid` byte-constructor + `to-byte-array`; `ContainerImageWriter.commit --run-boot`; `Time.monotonic-us`; `bucket.get … --if-absent`. Each is isolated to one task with a note.
</content>
