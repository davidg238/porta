# Vin run-once lifecycle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Land the vindriktning (PM1006) air-quality payload as a `boot × run-once` container: add a declared per-container **lifecycle** field end-to-end, make the supervisor **`wait` on run-once payloads under a `with-timeout` cap** instead of a fixed `sleep OBSERVE`, and ship the `vin` payload that reports an olympic-trimmed PM2.5 mean.

**Architecture:** A `lifecycle` field (`"run-once"` default | `"run-loop"`) is threaded gateway→device through the existing command/goal/inventory/report seams, exactly mirroring how `runlevel` already rides. The supervisor keeps the started `Container` handles for run-once apps and `wait`s on them (capped) before deep-sleeping; when there are no run-once payloads it preserves the M1-verified `sleep OBSERVE` timing unchanged. The `vin` payload is a self-contained device file (like `chatty`/`control_demo`) built on two host-tested pure helpers: `olympic-mean` and the PM1006 frame decoder.

**Tech Stack:** Toit (device firmware + the Toit gateway), `expect` host tests run with `toit <file>_test.toit`, Go tests for `gateway-go` run with `go test ./...`. Build artifacts via `toit compile -s` + `toit tool snapshot-to-image -m32`.

**Scope / non-goals (deliberate):** This plan implements the spec's *"Minimal change needed now"* (lifecycle field, `wait`-with-cap, vin payload). The cap-hit **northbound health event + gateway ≥2× escalation** (decided-in-principle but event-shape still open in the spec) is a **separate follow-up plan** — here a cap hit is logged locally only. Always-on mode and the hardware/software watchdogs (rest of roadmap item 2) are also out of scope.

**Reference:** `docs/specs/2026-05-24-node-lifecycle-reliability-design.md` (esp. "Minimal change needed *now*" and "The second dimension: run-once vs run-loop").

---

## File Map

**Gateway (Toit):**
- Modify `gateway/command.toit` — `Command.run` gains `--lifecycle`; add `lifecycle` getter + `is-valid-lifecycle` helper.
- Modify `gateway/gateway.toit` — `container install` gains `--lifecycle` option + validation, passed to `Command.run`.
- Modify `gateway/command_test.toit` — cover lifecycle on run command + validator.

**Device — lifecycle plumbing:**
- Modify `device/goal_state.toit` — `LIFECYCLE-RUN-ONCE`/`LIFECYCLE-RUN-LOOP` constants; `App.lifecycle` field + parse + `to-json`.
- Modify `device/goal_state_test.toit` — lifecycle parse/serialize round-trip.
- Modify `device/node_command.toit` — `apply-to-goal` carries `lifecycle` into the goal entry.
- Modify `device/node_command_test.toit` — assert lifecycle in applied goal.
- Modify `device/inventory.toit` — `InstalledApp.lifecycle`; `encode`/`decode`/`to-goal-map`.
- Modify `device/inventory_test.toit` — lifecycle survives encode→decode and appears in to-goal-map.
- Modify `device/report.toit` — echo `lifecycle` per app.
- Modify `device/report_test.toit` — assert lifecycle in report.

**Device — supervisor wait-with-cap:**
- Modify `device/supervisor.toit` — `MAX-AWAKE` const; install flow carries `app.lifecycle`; `start-installed` returns run-once `Container` handles; `main` waits-with-cap (else preserves `OBSERVE`).

**vin payload:**
- Create `device/olympic.toit` + `device/olympic_test.toit` — trimmed mean.
- Create `device/pm1006.toit` + `device/pm1006_test.toit` — PM1006 frame decode + UART reader.
- Create `device/vin.toit` — the payload.
- Build artifact: `vin.bin` (repo root, like `chatty.bin`).

**Test invocation reference:**
- One Toit suite: `toit device/<name>_test.toit` (exit 0 = pass; suites print `... OK` on success, nothing on assertion-only).
- All Toit suites: `for f in $(find device gateway -name '*_test.toit'); do echo "== $f"; toit "$f" || echo "FAIL: $f"; done`
- Go: `cd gateway-go && go test ./...`

---

## Task 1: Gateway — `Command.run` carries lifecycle

**Files:**
- Modify: `gateway/command.toit:34-42` (the `run` builder), add getter after `gateway/command.toit:77`, add helper near top.
- Test: `gateway/command_test.toit`

- [ ] **Step 1: Write the failing test**

Append before the final line of `main` in `gateway/command_test.toit`:

```toit
  // lifecycle defaults to run-once and round-trips through the wire codec.
  r0 := Command.run --name="vin" --crc=1 --size=2 --triggers={:}
  expect-equals "run-once" r0.lifecycle
  r1 := Command.run --name="vin" --crc=1 --size=2 --triggers={:} --lifecycle="run-loop"
  expect-equals "run-loop" (Command.decode r1.encode).lifecycle
  // validator
  expect (is-valid-lifecycle "run-once")
  expect (is-valid-lifecycle "run-loop")
  expect-not (is-valid-lifecycle "forever")
```

Ensure the test imports the helper — the existing `import .command show ...` line must include `Command is-valid-lifecycle` (add `is-valid-lifecycle` to the show list).

- [ ] **Step 2: Run test to verify it fails**

Run: `toit gateway/command_test.toit`
Expected: FAIL (compile error: `is-valid-lifecycle`/`lifecycle` unresolved).

- [ ] **Step 3: Implement**

In `gateway/command.toit`, add the helper after the `VERB-SET ::= "set"` line (around line 9):

```toit
/** Whether $lc is a valid container lifecycle declaration. */
is-valid-lifecycle lc/string -> bool:
  return lc == "run-once" or lc == "run-loop"
```

Change the `run` builder (line 34) to add the parameter and args key:

```toit
  static run --name/string --crc/int --size/int --triggers/Map --runlevel/int=3 --lifecycle/string="run-once" --arguments/List=[] -> Command:
    return Command VERB-RUN {
      "name": name,
      "crc": crc,
      "size": size,
      "triggers": triggers,
      "runlevel": runlevel,
      "lifecycle": lifecycle,
      "arguments": arguments,
    }
```

Add a getter after the `runlevel -> int?` getter (line 77):

```toit
  lifecycle -> string?: return args.get "lifecycle"
```

- [ ] **Step 4: Run test to verify it passes**

Run: `toit gateway/command_test.toit`
Expected: exit 0.

- [ ] **Step 5: Commit**

```bash
git add gateway/command.toit gateway/command_test.toit
git commit -m "feat(gateway): Command.run carries declared lifecycle (run-once default)"
```

---

## Task 2: Gateway — `container install --lifecycle`

**Files:**
- Modify: `gateway/gateway.toit:90-96` (install options), `gateway/gateway.toit:336-364` (`cmd-container-install`).

- [ ] **Step 1: Add the CLI option**

In `gateway/gateway.toit`, add to the `container-install-cmd` `--options` list (after the `runlevel` option, line 95):

```toit
        cli.Option "lifecycle" --help="Container lifecycle: run-once (default) | run-loop." --default="run-once",
```

- [ ] **Step 2: Validate + pass it through**

In `cmd-container-install` (line 336), after `name := parsed["name"]` add:

```toit
  lifecycle := parsed["lifecycle"]
  if not is-valid-lifecycle lifecycle:
    print "Error: invalid --lifecycle '$lifecycle' (expected run-once or run-loop)."
    exit 1
```

Then change the `Command.run` call (line 361) to pass it:

```toit
  run-cmd := Command.run --name=name --crc=crc --size=image.size --triggers=triggers --runlevel=parsed["runlevel"] --lifecycle=lifecycle
```

Confirm `is-valid-lifecycle` is visible: the `import .command show ...` line at the top of `gateway.toit` must include `is-valid-lifecycle` (add it).

- [ ] **Step 3: Compile-check**

Run: `toit compile -s -o /tmp/gateway.snapshot gateway/gateway.toit`
Expected: compiles with no error (exit 0).

- [ ] **Step 4: Commit**

```bash
git add gateway/gateway.toit
git commit -m "feat(gateway): container install --lifecycle flag (validated, run-once default)"
```

---

## Task 3: Device — lifecycle constants + `App.lifecycle`

**Files:**
- Modify: `device/goal_state.toit:5-50`
- Test: `device/goal_state_test.toit`

- [ ] **Step 1: Write the failing test**

Append before the final line of `main` in `device/goal_state_test.toit`:

```toit
  // lifecycle defaults to run-once and round-trips through parse/to-json.
  g-default := GoalState.parse (json.encode {"apps": {"a": {"size": 1, "crc": 2, "triggers": {"boot": 1}, "runlevel": 3}}})
  expect-equals "run-once" g-default.apps["a"].lifecycle
  g-loop := GoalState.parse (json.encode {"apps": {"b": {"size": 1, "crc": 2, "triggers": {"boot": 1}, "runlevel": 3, "lifecycle": "run-loop"}}})
  expect-equals "run-loop" g-loop.apps["b"].lifecycle
  reparsed := GoalState.parse g-loop.to-json
  expect-equals "run-loop" reparsed.apps["b"].lifecycle
```

(If `goal_state_test.toit` does not already `import encoding.json`, add it.)

- [ ] **Step 2: Run test to verify it fails**

Run: `toit device/goal_state_test.toit`
Expected: FAIL (`lifecycle` unresolved on `App`).

- [ ] **Step 3: Implement**

In `device/goal_state.toit`, add constants after the imports (after line 3):

```toit
/** Declared container lifecycle: a run-once container returns; a run-loop one never does. */
LIFECYCLE-RUN-ONCE ::= "run-once"
LIFECYCLE-RUN-LOOP ::= "run-loop"
```

Add the field + constructor param to `class App` (the field block at lines 6-14):

```toit
class App:
  name/string
  size/int       // Image bytes; sizes the ContainerImageWriter.
  crc/int        // CRC32-IEEE of the image; change-detection + verify.
  triggers/Triggers
  runlevel/int
  lifecycle/string
  arguments/List
  constructor --.name --.size --.crc --.triggers --.runlevel=3 --.lifecycle=LIFECYCLE-RUN-ONCE --.arguments=[]:
```

In `GoalState.parse`, add to the `App` construction (after the `--runlevel=...` line):

```toit
          --lifecycle=(spec.get "lifecycle" --if-absent=: LIFECYCLE-RUN-ONCE)
```

In `to-json`, add to the per-app map (after the `"runlevel": app.runlevel,` line):

```toit
        "lifecycle": app.lifecycle,
```

- [ ] **Step 4: Run test to verify it passes**

Run: `toit device/goal_state_test.toit`
Expected: exit 0.

- [ ] **Step 5: Commit**

```bash
git add device/goal_state.toit device/goal_state_test.toit
git commit -m "feat(device): App gains declared lifecycle field (run-once default)"
```

---

## Task 4: Device — `apply-to-goal` carries lifecycle

**Files:**
- Modify: `device/node_command.toit:41-52`
- Test: `device/node_command_test.toit`

- [ ] **Step 1: Write the failing test**

In `device/node_command_test.toit`, change the first run-command assertion block (lines 12-16) to expect a lifecycle, and add an explicit-lifecycle case. Replace lines 11-16 with:

```toit
  // run inserts/replaces the app in the goal map with the fields GoalState needs,
  // defaulting lifecycle to run-once when the command omits it.
  goal := {:}
  apply-to-goal goal run
  expect-structural-equals
      {"blink": {"size": 2048, "crc": 999, "triggers": {"interval": 30}, "runlevel": 3, "lifecycle": "run-once", "arguments": []}}
      goal

  // An explicit lifecycle is carried through.
  run-loop := NodeCommand.decode "{\"verb\":\"run\",\"name\":\"svc\",\"crc\":1,\"size\":2,\"triggers\":{\"boot\":1},\"runlevel\":3,\"lifecycle\":\"run-loop\",\"arguments\":[]}".to-byte-array
  apply-to-goal goal run-loop
  expect-equals "run-loop" goal["svc"]["lifecycle"]
```

- [ ] **Step 2: Run test to verify it fails**

Run: `toit device/node_command_test.toit`
Expected: FAIL (goal entry has no `lifecycle` key).

- [ ] **Step 3: Implement**

In `device/node_command.toit`, in `apply-to-goal` under `VERB-RUN` (lines 42-49), add the `lifecycle` key:

```toit
  if command.verb == VERB-RUN:
    goal[command.args["name"]] = {
      "size": command.args["size"],
      "crc": command.args["crc"],
      "triggers": command.args.get "triggers" --if-absent=: {:},
      "runlevel": command.args.get "runlevel" --if-absent=: 3,
      "lifecycle": command.args.get "lifecycle" --if-absent=: "run-once",
      "arguments": command.args.get "arguments" --if-absent=: [],
    }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `toit device/node_command_test.toit`
Expected: exit 0.

- [ ] **Step 5: Commit**

```bash
git add device/node_command.toit device/node_command_test.toit
git commit -m "feat(device): apply-to-goal carries declared lifecycle (run-once default)"
```

---

## Task 5: Device — `InstalledApp.lifecycle` + NVS codec + to-goal-map

**Files:**
- Modify: `device/inventory.toit` (class `InstalledApp` 7-15; `decode` 35-42; `encode` 45-55; `to-goal-map` 62-72)
- Test: `device/inventory_test.toit`

- [ ] **Step 1: Write the failing test**

Append before the final line of `main` in `device/inventory_test.toit`:

```toit
  // lifecycle survives the NVS encode→decode round-trip and appears in to-goal-map,
  // defaulting to run-once when an older stored tree omits it.
  app := InstalledApp --name="vin" --id=(uuid.Uuid.uuid5 "" "vin") --size=10 --crc=20
      --triggers=(Triggers --boot=true) --runlevel=3 --lifecycle="run-loop"
  inv := Inventory {"vin": app}
  round := Inventory.decode inv.encode
  expect-equals "run-loop" round.apps["vin"].lifecycle
  expect-equals "run-loop" (inv.to-goal-map)["vin"]["lifecycle"]
  // Missing key in a stored tree defaults to run-once.
  legacy := Inventory.decode {"apps": {"old": {"id": (uuid.Uuid.uuid5 "" "old").to-byte-array, "size": 1, "crc": 2, "triggers": {"boot": 1}, "runlevel": 3}}}
  expect-equals "run-once" legacy.apps["old"].lifecycle
```

(Ensure `inventory_test.toit` imports `uuid` and `Triggers` — add `import uuid` and `import .triggers show Triggers` if absent.)

- [ ] **Step 2: Run test to verify it fails**

Run: `toit device/inventory_test.toit`
Expected: FAIL (`lifecycle` unresolved on `InstalledApp`).

- [ ] **Step 3: Implement**

In `device/inventory.toit`, import the constant — change line 3 to:

```toit
import .goal_state show GoalState App LIFECYCLE-RUN-ONCE
```

Add the field + constructor param to `InstalledApp` (lines 7-15):

```toit
class InstalledApp:
  name/string
  id/uuid.Uuid   // Committed image id.
  size/int
  crc/int
  triggers/Triggers
  runlevel/int
  lifecycle/string

  constructor --.name --.id --.size --.crc --.triggers --.runlevel --.lifecycle=LIFECYCLE-RUN-ONCE:
```

In `decode` (line 40), add the field to the `InstalledApp` construction:

```toit
      app := InstalledApp --name=name --id=id --size=m["size"] --crc=m["crc"] --triggers=trig --runlevel=m["runlevel"] --lifecycle=(m.get "lifecycle" --if-absent=: LIFECYCLE-RUN-ONCE)
```

In `encode` (lines 48-54), add to the per-app map:

```toit
        "lifecycle": a.lifecycle,
```

In `to-goal-map` (lines 65-71), add to the per-app map:

```toit
        "lifecycle": a.lifecycle,
```

- [ ] **Step 4: Run test to verify it passes**

Run: `toit device/inventory_test.toit`
Expected: exit 0.

- [ ] **Step 5: Commit**

```bash
git add device/inventory.toit device/inventory_test.toit
git commit -m "feat(device): InstalledApp persists lifecycle through NVS + to-goal-map"
```

---

## Task 6: Device — supervisor install flow captures lifecycle

**Files:**
- Modify: `device/supervisor.toit:133` (the `InstalledApp` constructed during install)

- [ ] **Step 1: Implement**

In `device/supervisor.toit`, in the `recon.to-fetch.do` block, change the `InstalledApp` construction (line 133) to carry the goal app's lifecycle:

```toit
      inventory.apps[app.name] = InstalledApp --name=app.name --id=image-id --size=app.size --crc=app.crc --triggers=app.triggers --runlevel=app.runlevel --lifecycle=app.lifecycle
```

- [ ] **Step 2: Compile-check**

Run: `toit compile -s -o /tmp/supervisor.snapshot device/supervisor.toit`
Expected: compiles (exit 0). (`App.lifecycle` exists from Task 3; round-trip already proven by Task 5.)

- [ ] **Step 3: Commit**

```bash
git add device/supervisor.toit
git commit -m "feat(device): supervisor records installed app lifecycle"
```

---

## Task 7: Device — report echoes lifecycle

**Files:**
- Modify: `device/report.toit:13-25`
- Test: `device/report_test.toit`

- [ ] **Step 1: Write the failing test**

Append before the final line of `main` in `device/report_test.toit` (adapt the app-name to whatever the existing test already installs; this block builds its own inventory so it is self-contained):

```toit
  // The report echoes each app's declared lifecycle (parallel to runlevel).
  a := InstalledApp --name="vin" --id=(uuid.Uuid.uuid5 "" "vin") --size=1 --crc=2
      --triggers=(Triggers --boot=true) --runlevel=3 --lifecycle="run-loop"
  body := build-report (Inventory {"vin": a}) --uptime-us=0 --wakes=1
  decoded := json.decode body
  expect-equals "run-loop" decoded["apps"]["vin"]["lifecycle"]
```

(Ensure `report_test.toit` imports `json`, `uuid`, `Inventory InstalledApp`, and `Triggers` — add any missing imports.)

- [ ] **Step 2: Run test to verify it fails**

Run: `toit device/report_test.toit`
Expected: FAIL (no `lifecycle` key in the report's app map).

- [ ] **Step 3: Implement**

In `device/report.toit`, add `lifecycle` to the per-app echo map (the block at lines 16-20):

```toit
    apps[name] = {
      "crc": a.crc,
      "runlevel": a.runlevel,
      "lifecycle": a.lifecycle,
      "triggers": a.triggers.to-map,
    }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `toit device/report_test.toit`
Expected: exit 0.

- [ ] **Step 5: Commit**

```bash
git add device/report.toit device/report_test.toit
git commit -m "feat(device): report echoes declared lifecycle per app"
```

---

## Task 8: Device — `start-installed` returns run-once handles + `MAX-AWAKE`

**Files:**
- Modify: `device/supervisor.toit` (const block ~29; `start-installed` 151-155; `import .goal_state` line 7)

- [ ] **Step 1: Implement the constant + import**

In `device/supervisor.toit`, change the goal_state import (line 7) to also bring the lifecycle constant:

```toit
import .goal_state show GoalState LIFECYCLE-RUN-ONCE
```

Add the cap constant after `OBSERVE` (line 29):

```toit
/** Upper bound on how long the supervisor waits for run-once payloads before sleeping. */
MAX-AWAKE ::= Duration --s=20
```

- [ ] **Step 2: Rewrite `start-installed` to return run-once handles**

Replace `start-installed` (lines 151-155) with:

```toit
/**
Starts every installed app (deep-sleep cleared running state each wake) and returns
  the $containers.Container handles of the run-once apps, so the caller can `wait` on
  them. run-loop apps are started but their handles are not returned — the caller must
  not block on a container that never exits.
*/
start-installed inventory/Inventory -> List:
  handles := []
  inventory.apps.do: | name/string a/InstalledApp |
    container/containers.Container? := null
    e := catch --trace: container = containers.start a.id
    if e: print "supervisor: could not start $name ($a.id): $e"
    else:
      print "supervisor: started $name ($a.id, $a.lifecycle)"
      if a.lifecycle == LIFECYCLE-RUN-ONCE: handles.add container
  return handles
```

- [ ] **Step 3: Compile-check**

Run: `toit compile -s -o /tmp/supervisor.snapshot device/supervisor.toit`
Expected: a compile error at the `start-installed inventory` call site in `main` (return value now used differently) is acceptable here only if Task 9 is done together; otherwise the call `start-installed inventory` still compiles (return value ignored). Expected: exit 0.

- [ ] **Step 4: Commit**

```bash
git add device/supervisor.toit
git commit -m "feat(device): start-installed returns run-once Container handles; add MAX-AWAKE"
```

---

## Task 9: Device — supervisor `wait`s on run-once payloads (capped)

**Files:**
- Modify: `device/supervisor.toit:64-69` (the `start-installed` / `arm-wakeups` / `OBSERVE` block in `main`)

- [ ] **Step 1: Implement wait-with-cap**

In `device/supervisor.toit` `main`, replace these lines:

```toit
  start-installed inventory
  arm-wakeups inventory

  print "supervisor: observing for $OBSERVE"
  sleep OBSERVE
```

with:

```toit
  run-once-handles := start-installed inventory
  arm-wakeups inventory

  if run-once-handles.is-empty:
    // No run-once payloads to await: preserve the M1-verified deep-sleep timing.
    print "supervisor: observing for $OBSERVE"
    sleep OBSERVE
  else:
    print "supervisor: waiting on $(run-once-handles.size) run-once payload(s) (cap $MAX-AWAKE)"
    e := catch:
      with-timeout MAX-AWAKE:
        run-once-handles.do: | c/containers.Container | c.wait
    if e:
      // Graceful, local, no reboot. (Northbound cap-health reporting is a follow-up.)
      print "supervisor: payload cap hit after $MAX-AWAKE ($e) — proceeding to sleep"
    else:
      print "supervisor: run-once payload(s) finished; proceeding to sleep"
```

- [ ] **Step 2: Compile-check**

Run: `toit compile -s -o /tmp/supervisor.snapshot device/supervisor.toit`
Expected: compiles (exit 0).

- [ ] **Step 3: Commit**

```bash
git add device/supervisor.toit
git commit -m "feat(device): supervisor waits on run-once payloads under MAX-AWAKE cap"
```

---

## Task 10: vin — olympic trimmed mean

**Files:**
- Create: `device/olympic.toit`
- Test: `device/olympic_test.toit`

- [ ] **Step 1: Write the failing test**

Create `device/olympic_test.toit`:

```toit
import expect show *
import .olympic show olympic-mean

main:
  // Drops one high + one low, averages the middle.
  expect-equals 4.0 (olympic-mean [1, 2, 3, 4, 5, 6, 7])     // drop 1 & 7 -> mean(2..6)
  // Eight samples -> middle six (the vin case).
  expect-equals 4.5 (olympic-mean [1, 2, 3, 4, 5, 6, 7, 8])  // drop 1 & 8 -> mean(2..7)
  // A single high spike is trimmed away.
  expect-equals 10.0 (olympic-mean [10, 10, 10, 10, 999])    // drop 999 & one 10
  // Unsorted input is handled.
  expect-equals 4.5 (olympic-mean [8, 1, 5, 3, 7, 2, 6, 4])
  // Minimum size (3 -> single middle element).
  expect-equals 2.0 (olympic-mean [1, 2, 3])
  // Fewer than 3 throws.
  expect-throw "olympic-mean needs >= 3 values": olympic-mean [1, 2]
  print "olympic OK"
```

- [ ] **Step 2: Run test to verify it fails**

Run: `toit device/olympic_test.toit`
Expected: FAIL (cannot find `.olympic`).

- [ ] **Step 3: Implement**

Create `device/olympic.toit`:

```toit
// device/olympic.toit — the "olympic" trimmed mean used by run-once sampling payloads.
/**
Returns the trimmed mean of $values: drop the single highest and single lowest
  sample, then average the rest. Robust to one spike high and one dropout low.
  Requires at least 3 values so the trim leaves at least one. $values is not mutated.
*/
olympic-mean values/List -> float:
  if values.size < 3: throw "olympic-mean needs >= 3 values"
  sorted := values.sort  // ascending copy (non-destructive)
  middle := sorted[1 .. sorted.size - 1]
  sum := 0.0
  middle.do: sum += it
  return sum / middle.size
```

- [ ] **Step 4: Run test to verify it passes**

Run: `toit device/olympic_test.toit`
Expected: exit 0.

- [ ] **Step 5: Commit**

```bash
git add device/olympic.toit device/olympic_test.toit
git commit -m "feat(device): olympic trimmed-mean helper (host-tested)"
```

---

## Task 11: vin — PM1006 frame decoder + UART reader

**Files:**
- Create: `device/pm1006.toit`
- Test: `device/pm1006_test.toit`

Note: vendored from `~/workspaceToit/vindriktning/vindriktning.toit` (kept self-contained, like `chatty`/`control_demo`, to avoid pulling that package's unrelated `mqtt`/`ntp`/`provision` deps and its SDK pin). Pure decode helpers are host-tested; the UART class is compile-only.

- [ ] **Step 1: Write the failing test**

Create `device/pm1006_test.toit`:

```toit
import expect show *
import .pm1006 show pm1006-valid-frame? pm1006-pm25 PM1006-FRAME-SIZE

/** Builds a valid 20-byte PM1006 frame carrying $pm25, with a correcting checksum byte. */
build-frame pm25/int -> ByteArray:
  f := ByteArray PM1006-FRAME-SIZE
  f[0] = 0x16; f[1] = 0x11; f[2] = 0x0b
  f[5] = (pm25 >> 8) & 0xff
  f[6] = pm25 & 0xff
  sum := 0
  19.repeat: sum += f[it]
  f[19] = (-sum) & 0xff   // make the modulo-256 sum zero
  return f

main:
  good := build-frame 42
  expect (pm1006-valid-frame? good)
  expect-equals 42 (pm1006-pm25 good)

  // Two-byte value round-trips.
  big := build-frame 800
  expect (pm1006-valid-frame? big)
  expect-equals 800 (pm1006-pm25 big)

  // Wrong length rejected.
  expect-not (pm1006-valid-frame? (ByteArray 10))
  // Bad header rejected.
  bad-header := build-frame 42
  bad-header[0] = 0x00
  expect-not (pm1006-valid-frame? bad-header)
  // Corrupted body (checksum no longer zero) rejected.
  bad-sum := build-frame 42
  bad-sum[10] = (bad-sum[10] + 1) & 0xff
  expect-not (pm1006-valid-frame? bad-sum)
  print "pm1006 OK"
```

- [ ] **Step 2: Run test to verify it fails**

Run: `toit device/pm1006_test.toit`
Expected: FAIL (cannot find `.pm1006`).

- [ ] **Step 3: Implement**

Create `device/pm1006.toit`:

```toit
// device/pm1006.toit — minimal PM1006 (IKEA VINDRIKTNING) particulate-sensor frame
// reader. Pure decode helpers + a thin UART reader. Vendored from
// ~/workspaceToit/vindriktning/vindriktning.toit to keep the payload self-contained.
import gpio
import uart

PM1006-FRAME-SIZE ::= 20

/**
Whether $bytes is a well-formed PM1006 frame: exactly 20 bytes, header 16 11 0b,
  and a zero modulo-256 checksum over all 20 bytes.
*/
pm1006-valid-frame? bytes/ByteArray -> bool:
  if bytes.size != PM1006-FRAME-SIZE: return false
  if bytes[0] != 0x16 or bytes[1] != 0x11 or bytes[2] != 0x0b: return false
  sum := 0
  bytes.do: sum += it
  return (sum & 0xff) == 0

/** The PM2.5 reading (ppm) carried in a valid PM1006 frame $bytes (bytes 5..6, big-endian). */
pm1006-pm25 bytes/ByteArray -> int:
  return (bytes[5] << 8) | bytes[6]

/** A PM1006 sensor on a UART RX pin (9600 baud, 8N1). TX is unused by the sensor. */
class Pm1006:
  port_/uart.Port

  constructor --rx/int --tx/int=17:
    port_ = uart.Port --tx=(gpio.Pin tx) --rx=(gpio.Pin rx) --baud-rate=9600

  /** Blocks reading the UART until a valid frame arrives; returns its PM2.5 ppm. */
  read-pm25 -> int:
    reader := port_.in
    while true:
      frame := reader.read
      if frame and (pm1006-valid-frame? frame): return pm1006-pm25 frame

  close -> none: port_.close
```

- [ ] **Step 4: Run test to verify it passes**

Run: `toit device/pm1006_test.toit`
Expected: exit 0.

- [ ] **Step 5: Compile-check the UART class**

Run: `toit compile -s -o /tmp/pm1006.snapshot device/pm1006.toit`
Expected: compiles (exit 0).

- [ ] **Step 6: Commit**

```bash
git add device/pm1006.toit device/pm1006_test.toit
git commit -m "feat(device): vendored PM1006 frame decoder + UART reader (host-tested)"
```

---

## Task 12: vin — the payload

**Files:**
- Create: `device/vin.toit`

- [ ] **Step 1: Implement**

Create `device/vin.toit`:

```toit
// device/vin.toit — VINDRIKTNING (PM1006) air-quality payload. boot × run-once:
// per wake read 8 PM2.5 samples, report the olympic (trimmed) mean, return. The
// supervisor waits on this run-once container (under MAX-AWAKE) then deep-sleeps.
// Telemetry forwarding must be on (`gateway device set-console --on`) for the value
// to ship to the gateway.
import .pm1006 show Pm1006
import .olympic show olympic-mean
import .telemetry_service show TelemetryServiceClient

RX-PIN ::= 25     // PM1006 TX -> ESP32 RX. Adjust to your wiring.
SAMPLES ::= 8

main:
  sensor := Pm1006 --rx=RX-PIN
  samples := []
  SAMPLES.repeat: samples.add sensor.read-pm25
  sensor.close

  pm25 := olympic-mean samples

  tel := TelemetryServiceClient
  tel.open
  tel.log "vin: pm25=$pm25 (olympic of $SAMPLES)"
  tel.report "pm25" pm25
  tel.close
  print "vin: done (pm25=$pm25)"
```

- [ ] **Step 2: Compile-check**

Run: `toit compile -s -o /tmp/vin.snapshot device/vin.toit`
Expected: compiles (exit 0). (Sampling logic is already covered by `olympic_test` + `pm1006_test`; `main` itself opens hardware and is verified on-device in Task 15.)

- [ ] **Step 3: Commit**

```bash
git add device/vin.toit
git commit -m "feat(device): vin PM1006 run-once payload (8 samples -> olympic mean -> report)"
```

---

## Task 13: Build the vin image

**Files:**
- Create: `vin.bin` (repo root, alongside `chatty.bin`)

- [ ] **Step 1: Build the binary image**

Run (same pipeline as `chatty`/`control_demo`):

```bash
toit compile -s -o vin.snapshot device/vin.toit
toit tool snapshot-to-image -m32 --format=binary -o vin.bin vin.snapshot
```

Expected: `vin.bin` produced (~38 KB, comparable to `chatty.bin`). Verify: `ls -l vin.bin`.

- [ ] **Step 2: Commit**

```bash
git add vin.bin
git commit -m "build(device): vin.bin image artifact"
```

(If the repo `.gitignore`s `*.bin` / `*.snapshot`, do not force-add — instead note the build command in the task output and skip the commit. Check with `git check-ignore vin.bin`.)

---

## Task 14: Full regression

- [ ] **Step 1: Run every Toit host suite**

Run:

```bash
cd /home/david/workspaceToit/porta
for f in $(find device gateway -name '*_test.toit'); do toit "$f" >/dev/null 2>&1 && echo "ok  $f" || echo "FAIL $f"; done
```

Expected: every line `ok`. Investigate any `FAIL`.

- [ ] **Step 2: Run the Go suites**

Run: `cd gateway-go && go test ./...`
Expected: all packages `ok`.

- [ ] **Step 3: Commit (only if any test scaffolding changed)**

```bash
git add -A
git commit -m "test: vin run-once lifecycle — full host regression green"
```

(If nothing changed in this task, skip the commit.)

---

## Task 15: Hardware verification (manual)

Needs the spare jaguar node `classic-minute` (or any sensor node) and a running gateway. Two checks: (A) the supervisor wait-on-container path using a known run-once payload (`chatty`), independent of the PM1006 sensor; (B) the full vin path if a PM1006 sensor is wired to `RX-PIN`.

- [ ] **Step A1 — supervisor wait-with-cap, no sensor needed.** With the supervisor firmware (built per `host/build-envelope.sh`) flashed to a node and the gateway serving, install `chatty` as run-once and turn forwarding on:

```bash
gateway device set-console --device=<id> --on
gateway container install --device=<id> --trigger=boot --lifecycle=run-once chatty ./chatty.bin
```

Expected on the node serial monitor across the next wake: `supervisor: started chatty (... , run-once)`, then `supervisor: waiting on 1 run-once payload(s) (cap 0:00:20)`, then `chatty: done`, then `supervisor: run-once payload(s) finished; proceeding to sleep` — i.e. the supervisor blocks on the container's exit, NOT a fixed 5 s `OBSERVE`.

- [ ] **Step A2 — cap path.** Temporarily lower `MAX-AWAKE` (e.g. `Duration --s=2`) OR install a payload that sleeps longer than the cap; confirm the node logs `supervisor: payload cap hit after ... — proceeding to sleep` and still deep-sleeps (no reboot/hang). Restore `MAX-AWAKE` afterward.

- [ ] **Step B — full vin (if PM1006 wired).** Wire the PM1006 TX to `RX-PIN` (GPIO 25). Install vin:

```bash
gateway container install --device=<id> --trigger=boot --lifecycle=run-once vin ./vin.bin
```

Expected: node logs `vin: done (pm25=<value>)`; `gateway monitor` shows a `pm25` metric for the node; the awake time per wake is roughly the 8-frame collection time (~8 s), bounded by `MAX-AWAKE`.

- [ ] **Step C — record the result** in `docs/specs/2026-05-24-node-lifecycle-reliability-design.md` (a short "vin run-once: hardware-verified" note) and update memory `porta-vindriktning-lifecycle`.

---

## Self-Review

**Spec coverage (against "Minimal change needed *now*"):**
1. *start-installed returns Container handles + lifecycle* → Tasks 6, 8. ✅
2. *supervisor waits on run-once handles under one with-timeout budget, not on run-loop* → Task 9 (run-loop handles are never collected in Task 8). ✅
3. *container install gains declared lifecycle field in goal/InstalledApp* → Tasks 1–7. ✅
4. *vin payload read 8 → trimmed mean → report → return; needs set-console on* → Tasks 10–13, 15. ✅
- *Lifecycle field plumbing CLI→goal-map→InstalledApp* (open-items bullet) → Tasks 1–6. ✅
- *run-loop on a deep-sleep node = reject unless promoted* — gateway-side reconcile rejection is **out of scope** for this slice (node simply does not `wait` on run-loop; the gateway validator only checks the string is well-formed). Noted as a non-goal here; belongs with the always-on milestone.

**Placeholder scan:** every code step has complete code; test commands have exact invocations + expected exit. No TBD/TODO. ✅

**Type consistency:** `lifecycle/string` everywhere; constants `LIFECYCLE-RUN-ONCE`/`LIFECYCLE-RUN-LOOP` defined once in `goal_state.toit` and imported by `inventory.toit`/`supervisor.toit`; the wire/JSON key is `"lifecycle"` in gateway `Command.run`, device `apply-to-goal`, `App`, `InstalledApp`, and `report`. `start-installed -> List` of `containers.Container`; `MAX-AWAKE/Duration`. `olympic-mean -> float`; `pm1006-valid-frame? -> bool`; `pm1006-pm25 -> int`; `Pm1006 --rx --tx` / `.read-pm25` / `.close`. ✅
