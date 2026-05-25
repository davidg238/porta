# D5 — Observed-Config Echo Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The device report carries its applied per-app config (NVS `config` blob); the gateway stores it in the existing `observed_state` blob; `device get` shows desired vs observed per key with `(drift)`/`(pending)` markers.

**Architecture:** Four touch points, no DB schema change. (1) `build-report` emits a `"config"` sibling next to `"apps"`/`"health"`. (2) `ReportWriter_` folds that config into the `observed_state` TEXT blob it already writes. (3) Two pure, host-testable render helpers (`config-marker`, `render-config-table`) live in `gateway/command.toit` beside `project-config`. (4) `cmd-device-get` reads `observed_state` and renders the two-column table via the helpers. The device read path (`ControlServiceProvider`, app `client.get`) is untouched.

**Tech Stack:** Toit. Device under system `toit` (`toit run device/<f>_test.toit`; supervisor is analyze-only via `toit compile -s`). Gateway under `toit-sqlite` (`~/workspaceToit/sqlite/build/bin/toit-sqlite run <f>_test.toit` from `gateway/`). Tests are a `main` with `import expect show *` (no `toit test` on this SDK). Spec: `docs/specs/2026-05-24-d5-observed-config-echo-design.md`.

---

## File Structure

- **`device/report.toit`** (modify) — `build-report` gains `--config/Map={:}`; emits `"config"`.
- **`device/report_test.toit`** (modify) — assert `config` round-trips; empty config emits `{}`.
- **`device/supervisor.toit`** (modify, line ~143) — pass `--config=(load-config bucket)` at the report call.
- **`gateway/handler.toit`** (modify) — `ReportWriter_.close_` reads `config`, includes it in `observed_state`.
- **`gateway/handler_test.toit`** (modify) — report WRQ round-trips `config`; missing `config` → empty.
- **`gateway/command.toit`** (modify) — add pure `config-marker` and `render-config-table`.
- **`gateway/command_test.toit`** (modify) — cover marker + table cases.
- **`gateway/gateway.toit`** (modify, `cmd-device-get` ~line 296) — read `observed_state`, render via helpers.

---

## Task 1: Device — `build-report` carries config

**Files:**
- Modify: `device/report.toit`
- Modify: `device/report_test.toit`
- Modify: `device/supervisor.toit:143`

- [ ] **Step 1: Write the failing test**

Add to the end of `device/report_test.toit`'s `main` (after the existing empty-inventory assertion):

```toit
  // The report carries the applied per-app config blob verbatim.
  with-config := build-report inv
      --config={"blink": {"target": 21.5, "mode": "heat"}}
      --uptime-us=2 --wakes=3
  cfg := (json.decode with-config)["config"]
  expect-equals 21.5 cfg["blink"]["target"]
  expect-equals "heat" cfg["blink"]["mode"]

  // An omitted config defaults to an empty object (uniform body shape).
  expect-structural-equals {:} (json.decode empty)["config"]
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/david/workspaceToit/porta && toit run device/report_test.toit`
Expected: FAIL — `build-report` has no `--config` argument (compile error) / `config` key absent.

- [ ] **Step 3: Add the `--config` parameter and emit it**

In `device/report.toit`, update the signature and JSON body. Replace:

```toit
build-report inventory/Inventory --uptime-us/int --wakes/int -> ByteArray:
  apps := {:}
  inventory.apps.do: | name/string a/InstalledApp |
    apps[name] = {
      "crc": a.crc,
      "runlevel": a.runlevel,
      "triggers": a.triggers.to-map,
    }
  return json.encode {
    "apps": apps,
    "health": {"uptime_us": uptime-us, "wakes": wakes},
  }
```

with:

```toit
build-report inventory/Inventory --config/Map={:} --uptime-us/int --wakes/int -> ByteArray:
  apps := {:}
  inventory.apps.do: | name/string a/InstalledApp |
    apps[name] = {
      "crc": a.crc,
      "runlevel": a.runlevel,
      "triggers": a.triggers.to-map,
    }
  return json.encode {
    "apps": apps,
    "config": config,
    "health": {"uptime_us": uptime-us, "wakes": wakes},
  }
```

Also update the toitdoc line above it (the `{"apps":…,"health":…}` description) to mention the new `config` member:

```toit
/**
Builds the report body as a JSON object {"apps":{name:{crc,runlevel,triggers}},
  "config":{app:{key:value}}, "health":{uptime_us,wakes}}. $config is the node's
  applied per-app config blob (see device/config_store.toit); it defaults to empty.
  Carries no per-app logs and is bounded by the app/config count. $uptime-us is
  monotonic time; $wakes is the cumulative wake count.
*/
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /home/david/workspaceToit/porta && toit run device/report_test.toit`
Expected: PASS (no output / clean exit).

- [ ] **Step 5: Wire the supervisor's report call**

In `device/supervisor.toit`, the report is built at line ~143:

```toit
    body := build-report inventory --uptime-us=clock-us --wakes=store.wakes
```

Replace with (the supervisor already `import .config_store show load-config …` and has `bucket` in scope):

```toit
    body := build-report inventory --config=(load-config bucket) --uptime-us=clock-us --wakes=store.wakes
```

- [ ] **Step 6: Compile-gate the supervisor**

Run: `cd /home/david/workspaceToit/porta && toit compile -s -o /tmp/sup.snapshot device/supervisor.toit`
Expected: exits 0, no errors (supervisor imports esp32/system, so it cannot `toit run`; compile is the gate).

- [ ] **Step 7: Commit**

```bash
cd /home/david/workspaceToit/porta
git add device/report.toit device/report_test.toit device/supervisor.toit
git commit -m "feat(device): build-report carries applied config blob (D5)

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: Gateway handler — fold config into `observed_state`

**Files:**
- Modify: `gateway/handler.toit` (`ReportWriter_.close_`)
- Modify: `gateway/handler_test.toit`

- [ ] **Step 1: Write the failing test**

In `gateway/handler_test.toit`, the report-WRQ block (around line 64-84) builds a body with only `apps`/`health`. Extend that body to include `config`, and add assertions. Replace:

```toit
  body := #[]
  body = "{\"apps\":{\"blink\":{\"crc\":999,\"runlevel\":3}},\"health\":{\"wakes\":4}}".to-byte-array
  w := h2.writer-for "report?id=aabbccddeeff"
  w.write body
  w.close
  reps := store2.reports "aabbccddeeff"
  expect-equals 1 reps.size
  observed := decode-json_ reps[0]["observed_state"]
  expect-equals 999 observed["apps"]["blink"]["crc"]
```

with:

```toit
  body := #[]
  body = "{\"apps\":{\"blink\":{\"crc\":999,\"runlevel\":3}},\"config\":{\"blink\":{\"target\":21.5}},\"health\":{\"wakes\":4}}".to-byte-array
  w := h2.writer-for "report?id=aabbccddeeff"
  w.write body
  w.close
  reps := store2.reports "aabbccddeeff"
  expect-equals 1 reps.size
  observed := decode-json_ reps[0]["observed_state"]
  expect-equals 999 observed["apps"]["blink"]["crc"]
  // Observed config rides in the same observed_state blob.
  expect-equals 21.5 observed["config"]["blink"]["target"]
```

Then, just before `store2.close` in that block, add a second report with no `config` and assert it stores an empty config:

```toit
  // A report body without "config" stores an empty config (old/pre-D5 nodes).
  noconf := "{\"apps\":{},\"health\":{\"wakes\":5}}".to-byte-array
  w2 := h2.writer-for "report?id=aabbccddeeff"
  w2.write noconf
  w2.close
  latest := decode-json_ (store2.reports "aabbccddeeff")[0]["observed_state"]
  expect-structural-equals {:} latest["config"]
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/david/workspaceToit/porta/gateway && ~/workspaceToit/sqlite/build/bin/toit-sqlite run handler_test.toit`
Expected: FAIL — `observed["config"]` is null (key absent from `observed_state`).

- [ ] **Step 3: Fold config into the stored blob**

In `gateway/handler.toit`, `ReportWriter_.close_` (around line 119). Replace:

```toit
  close_ -> none:
    obj := decode-json_ buffer_.bytes.to-string
    apps := obj.get "apps" --if-absent=: {:}
    health := obj.get "health" --if-absent=: {:}
    store_.insert-report id_
        --observed-state=(encode-json_ {"apps": apps})
        --health=(encode-json_ health)
        --now=now_
```

with:

```toit
  close_ -> none:
    obj := decode-json_ buffer_.bytes.to-string
    apps := obj.get "apps" --if-absent=: {:}
    config := obj.get "config" --if-absent=: {:}
    health := obj.get "health" --if-absent=: {:}
    store_.insert-report id_
        --observed-state=(encode-json_ {"apps": apps, "config": config})
        --health=(encode-json_ health)
        --now=now_
```

Update the `ReportWriter_` class toitdoc (lines ~103-107) to note it now also records observed config:

```toit
/**
An $io.CloseableWriter that buffers a WRQ "report" body and, on close, splits it
  into the observed-app state (with the applied config blob) and the health struct,
  recording both via $Store.insert-report. The body is one JSON object
  {"apps":{…}, "config":{…}, "health":{…}}; "config" is absent on pre-D5 nodes and
  defaults to empty.
*/
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /home/david/workspaceToit/porta/gateway && ~/workspaceToit/sqlite/build/bin/toit-sqlite run handler_test.toit`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /home/david/workspaceToit/porta
git add gateway/handler.toit gateway/handler_test.toit
git commit -m "feat(gateway): report ingest folds observed config into observed_state (D5)

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: Gateway — pure render helpers (`config-marker`, `render-config-table`)

**Files:**
- Modify: `gateway/command.toit` (add two functions after `project-config`)
- Modify: `gateway/command_test.toit`

- [ ] **Step 1: Write the failing test**

Append to the end of `gateway/command_test.toit`'s `main`:

```toit
  // config-marker classifies a key across desired/observed.
  d := {"setpoint": 21.5, "mode": "eco", "hyst": 0.5}
  o := {"setpoint": 21.5, "mode": "heat"}
  expect-equals "" (config-marker d o "setpoint")        // present & equal
  expect-equals "(drift)" (config-marker d o "mode")     // present & unequal
  expect-equals "(pending)" (config-marker d o "hyst")   // desired only
  expect-equals "" (config-marker d o "extra")           // neither present
  expect-equals "" (config-marker {:} {"x": 1} "x")      // observed only → no marker

  // render-config-table emits a header + one row per union key, "--" for absent cells.
  lines := render-config-table "thermostat" d o
  joined := lines.join "\n"
  expect (joined.contains "config for thermostat")
  expect (joined.contains "setpoint")
  expect (joined.contains "21.5")
  expect (joined.contains "(drift)")     // mode row
  expect (joined.contains "(pending)")   // hyst row
  expect (joined.contains "--")          // hyst observed cell
  // Empty on both sides → a single "no config" line.
  empty-lines := render-config-table "x" {:} {:}
  expect-equals 1 empty-lines.size
  expect (empty-lines[0].contains "no config")
  // observed-only key still appears (abnormal but rendered).
  oo := render-config-table "a" {:} {"k": 9}
  expect ((oo.join "\n").contains "k")
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/david/workspaceToit/porta/gateway && ~/workspaceToit/sqlite/build/bin/toit-sqlite run command_test.toit`
Expected: FAIL — `config-marker` / `render-config-table` not defined.

- [ ] **Step 3: Implement the helpers**

Append to `gateway/command.toit` (after `project-config`, end of file):

```toit
/**
Classifies config $key across the $desired and $observed maps for `device get`:
  "(drift)" when both are present and unequal, "(pending)" when desired is present
  but observed is absent (the node has not yet converged), else "" (equal, or the
  key is desired-absent). Values compare with `==` on the JSON-decoded scalars.
*/
config-marker desired/Map observed/Map key/string -> string:
  has-d := desired.contains key
  has-o := observed.contains key
  if has-d and has-o: return desired[key] == observed[key] ? "" : "(drift)"
  if has-d: return "(pending)"
  return ""

/**
Renders the desired-vs-observed config table for app $app as a list of printable
  lines (caller adds any node-id prefix). Covers the union of $desired and
  $observed keys (desired order first, then observed-only keys); an absent value
  cell renders "--", and each row carries the $config-marker. Both maps empty →
  a single "$app has no config" line.
*/
render-config-table app/string desired/Map observed/Map -> List:
  if desired.is-empty and observed.is-empty:
    return ["$app has no config"]
  keys := []
  desired.do --keys: keys.add it
  observed.do --keys: if not desired.contains it: keys.add it
  lines := ["config for $app", "  $(pad-col_ "KEY" 12)$(pad-col_ "DESIRED" 12)OBSERVED"]
  keys.do: | k/string |
    d-cell := desired.contains k ? "$desired[k]" : "--"
    o-cell := observed.contains k ? "$observed[k]" : "--"
    marker := config-marker desired observed k
    row := "  $(pad-col_ k 12)$(pad-col_ d-cell 12)$(pad-col_ o-cell 10)$marker"
    lines.add row.trim --right
  return lines

/** Right-pads $s with spaces to at least $width columns (one trailing space min). */
pad-col_ s/string width/int -> string:
  pad := width - s.size
  return pad > 0 ? "$s$(" " * pad)" : "$s "
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /home/david/workspaceToit/porta/gateway && ~/workspaceToit/sqlite/build/bin/toit-sqlite run command_test.toit`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /home/david/workspaceToit/porta
git add gateway/command.toit gateway/command_test.toit
git commit -m "feat(gateway): desired-vs-observed config render helpers (D5)

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: Gateway — `cmd-device-get` renders desired vs observed

**Files:**
- Modify: `gateway/gateway.toit` (`cmd-device-get`, ~line 296)

This task wires the CLI command to the Task 3 helpers and the observed_state blob. `cmd-device-get` prints directly and takes a `cli.Parsed`, so there is no host unit test for it; verification is the full host suite plus a manual `device get` against an in-memory store (Step 4).

- [ ] **Step 1: Rewrite `cmd-device-get`**

In `gateway/gateway.toit`, replace the whole `cmd-device-get` function:

```toit
cmd-device-get parsed/cli.Parsed -> none:
  store := open-store_ parsed
  id := resolve-node-id_ store parsed["device"]
  app := parsed["app"]
  key := parsed.was-provided "key" ? parsed["key"] : null
  commands := (store.command-log id).map: | e/Map | Command e["verb"] e["args"]
  desired := (project-config commands).get app --if-absent=: {:}
  node := store.node id
  observed-all := (node == null or node["observed_state"] == null)
      ? {:}
      : ((decode-json_ node["observed_state"]).get "config" --if-absent=: {:})
  observed := observed-all.get app --if-absent=: {:}
  if key != null:
    d-cell := desired.contains key ? "$desired[key]" : "--"
    o-cell := observed.contains key ? "$observed[key]" : "--"
    marker := config-marker desired observed key
    print "$id: $app.$key desired=$d-cell observed=$o-cell $marker".trim
    store.close
    return
  lines := render-config-table app desired observed
  print "$id: $lines[0]"
  lines[1..].do: print it
  store.close
```

(`decode-json_` is already imported in `gateway.toit` — it is used by `cmd-container-list` at line ~362. `config-marker` and `render-config-table` come from the existing `import .command show *`.)

- [ ] **Step 2: Confirm the command imports cover the new symbols**

Run: `cd /home/david/workspaceToit/porta/gateway && grep -n "import .command\|decode-json_" gateway.toit | head`
Expected: shows `import .command show *` (or an explicit list — if explicit, add `config-marker render-config-table`) and that `decode-json_` is already imported from `.store`.

- [ ] **Step 3: Compile-gate the gateway**

Run: `cd /home/david/workspaceToit/porta/gateway && ~/workspaceToit/sqlite/build/bin/toit-sqlite run integration_test.toit`
Expected: PASS — exercises the gateway end-to-end and confirms `gateway.toit` compiles with the rewritten command.

- [ ] **Step 4: Manual verify the full path against a real store**

This drives the actual stored `observed_state` blob (not a hand-built map) so it exercises Task 2 + Task 3 together: enqueue three sets, ingest a report whose observed config agrees on `setpoint`, drifts on `mode`, and omits `hyst`, then render exactly as `cmd-device-get` does.

```bash
cd /home/david/workspaceToit/porta/gateway && cat > /tmp/d5_demo.toit <<'EOF'
import .store show Store decode-json_
import .handler show StoreBackedHandler
import .command show Command project-config render-config-table

main:
  store := Store.open ":memory:"
  store.ensure-node "aabbccddeeff" --now=1
  store.enqueue-command "aabbccddeeff" (Command.set --app="thermostat" --key="setpoint" --value=21.5) --issued-by="t" --now=1
  store.enqueue-command "aabbccddeeff" (Command.set --app="thermostat" --key="mode" --value="eco") --issued-by="t" --now=1
  store.enqueue-command "aabbccddeeff" (Command.set --app="thermostat" --key="hyst" --value=0.5) --issued-by="t" --now=1
  // Ingest a report: setpoint agrees, mode drifted to "heat", hyst not yet applied.
  h := StoreBackedHandler store
  w := h.writer-for "report?id=aabbccddeeff"
  w.write "{\"apps\":{},\"config\":{\"thermostat\":{\"setpoint\":21.5,\"mode\":\"heat\"}},\"health\":{}}".to-byte-array
  w.close
  // Replay cmd-device-get's projection over the real store.
  commands := (store.command-log "aabbccddeeff").map: | e/Map | Command e["verb"] e["args"]
  desired := (project-config commands)["thermostat"]
  observed-all := (decode-json_ (store.node "aabbccddeeff")["observed_state"])["config"]
  observed := observed-all["thermostat"]
  print ((render-config-table "thermostat" desired observed).join "\n")
  store.close
EOF
~/workspaceToit/sqlite/build/bin/toit-sqlite run /tmp/d5_demo.toit; rm -f /tmp/d5_demo.toit
```

Expected output (column spacing approximate; markers are what matters):

```
config for thermostat
  KEY         DESIRED     OBSERVED
  setpoint    21.5        21.5
  mode        eco         heat      (drift)
  hyst        0.5         --        (pending)
```

- [ ] **Step 5: Commit**

```bash
cd /home/david/workspaceToit/porta
git add gateway/gateway.toit
git commit -m "feat(gateway): device get shows desired vs observed config (D5)

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: Full host suite + spec note

**Files:**
- Modify: `docs/specs/2026-05-24-d5-observed-config-echo-design.md` (status line)

- [ ] **Step 1: Run every device + gateway host suite**

```bash
cd /home/david/workspaceToit/porta
for t in device/*_test.toit; do echo "== $t ==" ; toit run $t || echo "FAILED: $t" ; done
cd gateway
for t in *_test.toit; do echo "== $t ==" ; ~/workspaceToit/sqlite/build/bin/toit-sqlite run $t || echo "FAILED: $t" ; done
```

Expected: no `FAILED:` lines.

- [ ] **Step 2: Re-run the supervisor compile gate**

Run: `cd /home/david/workspaceToit/porta && toit compile -s -o /tmp/sup.snapshot device/supervisor.toit`
Expected: exits 0.

- [ ] **Step 3: Mark the spec implemented (host-verified)**

In `docs/specs/2026-05-24-d5-observed-config-echo-design.md`, change the `**Status:** Approved` line to:

```markdown
**Status:** Implemented — host-verified 2026-05-24. Hardware echo confirmation
pending a poll-wake on the device (observed config refreshes on the next report).
```

- [ ] **Step 4: Commit**

```bash
cd /home/david/workspaceToit/porta
git add docs/specs/2026-05-24-d5-observed-config-echo-design.md
git commit -m "docs(specs): D5 observed-config echo host-verified

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review

**Spec coverage:**
- "build-report carries config" → Task 1. ✓
- "supervisor passes load-config" → Task 1 Step 5. ✓
- "ReportWriter_ folds config into observed_state" → Task 2. ✓
- "device get desired-vs-observed table, drift/pending markers" → Tasks 3 (helpers) + 4 (wiring). ✓
- "single-key form shows one row" → Task 4 Step 1 (`key != null` branch). ✓
- "no schema change / pre-D5 nodes tolerated" → Task 2 (`--if-absent` empty config; test asserts it). ✓
- Tests named in spec (report_test, handler_test, device-get rendering) → Tasks 1, 2, 3. ✓
- "device read path unchanged" → no task touches control_service / app reads. ✓

**Placeholder scan:** No TBD/TODO; every code step shows full code. ✓

**Type consistency:** `render-config-table app/string desired/Map observed/Map -> List` and `config-marker desired/Map observed/Map key/string -> string` are used with the same signatures in Tasks 3 and 4. `build-report … --config/Map={:}` matches its call in Task 1 Step 5. `observed_state` decoded via `decode-json_` (already imported) in both Task 4 and the demo. ✓