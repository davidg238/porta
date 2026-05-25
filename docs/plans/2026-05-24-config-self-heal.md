# Config Self-Heal Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The gateway re-issues any config `set` that was delivered to a node but did not take, by diffing desired (the `set` command-log) against observed (the D5 report echo) after every report, and warns in `device get` when a key keeps failing to apply.

**Architecture:** Gateway-side reconcile-on-report; the node firmware is unchanged. A pure `reconcile-config` function (the generic diff seam) returns the latest `set` log rows replayed verbatim as `Command`s for any delivered-but-divergent `(app, key)`. `ReportWriter_.close_` enqueues them tagged `issued_by="gateway-reconcile"` after `insert-report` commits. An in-flight guard (only re-issue delivered sets) makes re-issue self-throttling, so a repeated re-issue count surfaces a real crash-loop warning. No DB schema change.

**Tech Stack:** Toit (gateway runs under `toit-sqlite`); sqlite store; tests are `main`-with-`import expect show *` run via `toit-sqlite run`.

**Spec:** `docs/specs/2026-05-24-config-self-heal-design.md`

**Before you start — toolchain:** every command below assumes:
```bash
export TS=~/workspaceToit/sqlite/build/bin/toit-sqlite
cd /home/david/workspaceToit/porta/gateway
```
There is no `toit test` on this SDK; each `*_test.toit` is a `main` you run directly. `$TS analyze <file>.toit` is the compile gate (exit 0, no output = clean).

**Toit reminders (bite you if forgotten):** empty Map literal is `{:}` (`{}` is a Set); `Map` `==` is identity, so compare maps with `expect-structural-equals`; `expect-throw` matches the thrown string exactly; a re-issued `Command` is built with the primary constructor `Command verb args` (`command.toit:24`).

---

## File Structure

- **`gateway/command.toit`** (modify) — add two pure functions next to `project-config`/`config-marker`: `reconcile-config` (the diff seam) and `reconcile-count` (the warning counter).
- **`gateway/command_test.toit`** (modify) — unit tests for both new functions.
- **`gateway/handler.toit`** (modify) — wire `reconcile-config` into `ReportWriter_.close_`; extend the `.command` import.
- **`gateway/handler_test.toit`** (modify) — integration test: a divergent delivered report enqueues exactly one re-issue; self-throttle holds.
- **`gateway/gateway.toit`** (modify) — `cmd-device-get` prints the self-heal warning per still-divergent, ≥2×-reconciled key.

---

## Task 1: `reconcile-config` — the pure diff seam

**Files:**
- Modify: `gateway/command.toit` (add after `project-config`, ends `command.toit:159`)
- Test: `gateway/command_test.toit` (add cases to the existing `main`)

- [ ] **Step 1: Write the failing test**

Add this block to the end of `main` in `gateway/command_test.toit` (the existing tests build `Command` and call `project-config` the same way, so the helpers below match the codebase). `reconcile-config` takes the `command-log` shape `Store.command-log` returns — maps with `verb`, decoded `args`, `issued_by`, `delivered_at` — and an `observed` map `app → {key:value}`. It returns the `Command`s to re-issue.

```toit
  // --- reconcile-config: diff desired (set log) vs observed, re-issue divergent delivered sets ---
  // Build a command-log entry the way Store.command-log returns it.
  set-entry := : | app/string key/string value/any delivered/any |
    {"verb": VERB-SET, "args": {"app": app, "key": key, "value": value},
     "issued_by": "cli", "delivered_at": delivered}

  // delivered + drift (observed present but unequal) → re-issue, replayed verbatim.
  drift-log := [set-entry.call "t" "mode" "heat" 100]
  drift := reconcile-config drift-log {"t": {"mode": "eco"}}
  expect-equals 1 drift.size
  expect-equals VERB-SET drift[0].verb
  expect-structural-equals {"app": "t", "key": "mode", "value": "heat"} drift[0].args

  // delivered + pending (observed absent) → re-issue.
  pending := reconcile-config [set-entry.call "t" "mode" "heat" 100] {"t": {:}}
  expect-equals 1 pending.size

  // undelivered + drift → SKIP (in-flight guard).
  inflight := reconcile-config [set-entry.call "t" "mode" "heat" null] {"t": {"mode": "eco"}}
  expect (inflight.is-empty)

  // converged (delivered + equal) → skip.
  expect (reconcile-config [set-entry.call "t" "mode" "heat" 100] {"t": {"mode": "heat"}}).is-empty

  // observed-only key (no desired set) → skip (desired never shrinks).
  expect (reconcile-config [] {"t": {"ghost": 1}}).is-empty

  // multi-app/key: only divergent delivered keys re-issue.
  multi-log := [
    set-entry.call "t" "mode" "heat" 100,    // drift → reissue
    set-entry.call "t" "sp" 22 100,          // converged → skip
    set-entry.call "s" "thr" 5 100,          // pending → reissue
  ]
  multi := reconcile-config multi-log {"t": {"mode": "eco", "sp": 22}, "s": {:}}
  expect-equals 2 multi.size

  // scalar type fidelity: a float that round-trips equal does NOT re-issue (no false drift),
  // and a re-issued command's args equal the original row's args (verbatim replay).
  expect (reconcile-config [set-entry.call "t" "f" 21.5 100] {"t": {"f": 21.5}}).is-empty
  fid := reconcile-config [set-entry.call "t" "f" 21.5 100] {"t": {"f": 22.5}}
  expect (fid[0].args["value"] is float)
  expect-equals 21.5 fid[0].args["value"]

  // self-throttle: latest set for the key is an undelivered gateway-reconcile set → skip.
  throttle-log := [
    set-entry.call "t" "mode" "heat" 100,    // delivered cli set
    {"verb": VERB-SET, "args": {"app": "t", "key": "mode", "value": "heat"},
     "issued_by": "gateway-reconcile", "delivered_at": null},  // in-flight reissue (latest)
  ]
  expect (reconcile-config throttle-log {"t": {"mode": "eco"}}).is-empty
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `$TS run command_test.toit`
Expected: FAIL — `reconcile-config` is not defined (analysis/runtime error naming `reconcile-config`).

- [ ] **Step 3: Implement `reconcile-config`**

Add to `gateway/command.toit` immediately after `project-config` (after line 159, before `config-marker`):

```toit
/**
Diffs desired config (projected from the $command-log) against $observed config and
  returns the $Command s to re-issue to self-heal divergence. For each divergent
  (app, key) it returns that key's *latest `set` log entry replayed verbatim* — the
  original command rebuilt via `Command verb args` from the stored row, not from
  extracted scalars, so the re-issued args (and scalar types) are identical.

A key is re-issued only when its latest `set` is already delivered
  (`delivered_at` != null) AND the observed value diverges (absent, or
  present-but-unequal). An undelivered latest `set` legitimately lags (in-flight) and
  is skipped — and since a re-issued set is itself undelivered next report, re-issue is
  self-throttling. Observed keys with no desired `set` are left alone (desired never
  shrinks). This is the generic diff seam: goal/apps can later feed a different
  projection of the same `command-log` shape.

$command-log is $Store.command-log output: maps carrying `verb`, decoded `args`,
  `issued_by`, `delivered_at`. $observed is the report echo: app -> {key: value}.
*/
reconcile-config command-log/List observed/Map -> List:
  // Latest set entry per (app, key), in log order — last write wins (like project-config).
  latest := {:}  // app -> { key -> log-entry Map }
  command-log.do: | e/Map |
    if e["verb"] == VERB-SET:
      args := e["args"]
      (latest.get args["app"] --init=: {:})[args["key"]] = e
  reissues := []
  latest.do: | app/string keys/Map |
    obs-app := observed.get app --if-absent=: {:}
    keys.do: | key/string entry/Map |
      if entry["delivered_at"] != null:
        desired-val := entry["args"]["value"]
        converged := (obs-app.contains key) and obs-app[key] == desired-val
        if not converged:
          reissues.add (Command entry["verb"] entry["args"])
  return reissues
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `$TS run command_test.toit`
Expected: PASS (no output, exit 0 — the existing suite plus the new cases all pass).

- [ ] **Step 5: Commit**

```bash
git add command.toit command_test.toit
git commit -m "feat(gateway): reconcile-config diffs desired vs observed config (P1 #1)"
```

---

## Task 2: `reconcile-count` — the self-heal attempt counter

**Files:**
- Modify: `gateway/command.toit` (add after `reconcile-config`)
- Test: `gateway/command_test.toit` (add cases to the existing `main`)

- [ ] **Step 1: Write the failing test**

Add to the end of `main` in `gateway/command_test.toit`:

```toit
  // --- reconcile-count: how many gateway-reconcile sets targeted (app, key) ---
  count-log := [
    {"verb": VERB-SET, "args": {"app": "t", "key": "mode", "value": "heat"},
     "issued_by": "cli", "delivered_at": 1},                 // cli, not counted
    {"verb": VERB-SET, "args": {"app": "t", "key": "mode", "value": "heat"},
     "issued_by": "gateway-reconcile", "delivered_at": 2},   // counted
    {"verb": VERB-SET, "args": {"app": "t", "key": "mode", "value": "heat"},
     "issued_by": "gateway-reconcile", "delivered_at": 3},   // counted
    {"verb": VERB-SET, "args": {"app": "t", "key": "other", "value": 1},
     "issued_by": "gateway-reconcile", "delivered_at": 4},   // different key
  ]
  expect-equals 2 (reconcile-count count-log "t" "mode")
  expect-equals 1 (reconcile-count count-log "t" "other")
  expect-equals 0 (reconcile-count count-log "t" "absent")
  expect-equals 0 (reconcile-count [] "t" "mode")
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `$TS run command_test.toit`
Expected: FAIL — `reconcile-count` is not defined.

- [ ] **Step 3: Implement `reconcile-count`**

Add to `gateway/command.toit` immediately after `reconcile-config`:

```toit
/**
Counts the `gateway-reconcile` `set` commands targeting ($app, $key) in the
  $command-log — the self-heal attempt count. Because re-issue is self-throttled
  (one per delivered-but-still-failed report), a count >= 2 for a still-divergent key
  means the node delivered and failed to apply twice: a real apply crash-loop, not
  reconcile noise. Used by `device get` to surface a warning.
*/
reconcile-count command-log/List app/string key/string -> int:
  count := 0
  command-log.do: | e/Map |
    if e["verb"] == VERB-SET and e["issued_by"] == "gateway-reconcile":
      args := e["args"]
      if args["app"] == app and args["key"] == key: count++
  return count
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `$TS run command_test.toit`
Expected: PASS (no output, exit 0).

- [ ] **Step 5: Commit**

```bash
git add command.toit command_test.toit
git commit -m "feat(gateway): reconcile-count tallies gateway-reconcile sets per key"
```

---

## Task 3: Wire reconcile-on-report into `ReportWriter_.close_`

**Files:**
- Modify: `gateway/handler.toit` — extend the `.command` import (line 12) and `ReportWriter_.close_` (`handler.toit:121-129`)
- Test: `gateway/handler_test.toit` (add a block to the existing `main`)

- [ ] **Step 1: Write the failing test**

Add to the end of `main` in `gateway/handler_test.toit` (it already imports `Command` and `decode-json_`; mirror the existing "WRQ to report" block above it):

```toit
  // --- reconcile-on-report: a delivered-but-divergent config re-issues exactly once ---
  store4 := Store.open ":memory:"
  h4 := StoreBackedHandler store4
  store4.ensure-node "aabbccddeeff" --now=4000
  // A cli set lands and is delivered, but the node will report a different value.
  cid := store4.enqueue-command "aabbccddeeff" (Command.set --app="thermostat" --key="mode" --value="heat") --issued-by="cli" --now=4000
  store4.mark-delivered cid --now=4001
  // Node reports observed mode=eco (the set did not take).
  rbody := "{\"apps\":{},\"config\":{\"thermostat\":{\"mode\":\"eco\"}},\"health\":{\"wakes\":1}}".to-byte-array
  rw := h4.writer-for "report?id=aabbccddeeff"
  rw.write rbody
  rw.close
  // Reconcile enqueued exactly one gateway-reconcile set for the divergent key.
  undel := store4.undelivered-commands "aabbccddeeff"
  expect-equals 1 undel.size
  expect-equals VERB-SET undel[0]["verb"]
  expect-equals "thermostat" undel[0]["args"]["app"]
  expect-equals "heat" undel[0]["args"]["value"]
  reissue-log := store4.command-log "aabbccddeeff"
  expect-equals 1 (reconcile-count reissue-log "thermostat" "mode")

  // Self-throttle: a second report BEFORE the reissue delivers must NOT double-issue.
  rw2 := h4.writer-for "report?id=aabbccddeeff"
  rw2.write rbody
  rw2.close
  expect-equals 1 (store4.undelivered-commands "aabbccddeeff").size
  expect-equals 1 (reconcile-count (store4.command-log "aabbccddeeff") "thermostat" "mode")
  store4.close
```

`handler_test.toit` must see `VERB-SET` and `reconcile-count`. Change its command import (`handler_test.toit:4`) from:
```toit
import .command show Command
```
to:
```toit
import .command show Command VERB-SET reconcile-count
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `$TS run handler_test.toit`
Expected: FAIL — the report closes but no command is enqueued, so `undel.size` is 0 (reconcile not wired yet).

- [ ] **Step 3: Wire reconcile into the handler**

In `gateway/handler.toit`, extend the command import (line 12) from:
```toit
import .command show Command
```
to:
```toit
import .command show Command reconcile-config
```

Then replace `ReportWriter_.close_` (`handler.toit:121-129`):
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
    // Self-heal: re-issue delivered-but-divergent config sets. The report is already
    // committed, so a reconcile failure must never lose it.
    catch --trace:
      reissues := reconcile-config (store_.command-log id_) config
      reissues.do: | cmd/Command |
        store_.enqueue-command id_ cmd --issued-by="gateway-reconcile" --now=now_
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `$TS run handler_test.toit`
Expected: PASS (no output, exit 0 — the new block plus the existing report/drain tests all pass; the existing "report without config" test still passes because `reconcile-config` over an empty command-log returns no re-issues).

- [ ] **Step 5: Commit**

```bash
git add handler.toit handler_test.toit
git commit -m "feat(gateway): reconcile config on report ingest (P1 #1)"
```

---

## Task 4: Surface the self-heal warning in `device get`

**Files:**
- Modify: `gateway/gateway.toit` — `cmd-device-get` (`gateway.toit:296-318`)

`cmd-device-get` already builds `desired` and `observed` and renders the table. It currently maps the log down to `Command` objects (dropping `issued_by`), so capture the **raw** command-log once and reuse it for both `project-config` (via the mapped list) and `reconcile-count` (raw). After printing the table, print a warning line for each key that is still divergent (`config-marker` non-empty) and has been reconciled ≥2×.

- [ ] **Step 1: Update `cmd-device-get`**

Replace `cmd-device-get` (`gateway.toit:296-318`) with:
```toit
cmd-device-get parsed/cli.Parsed -> none:
  store := open-store_ parsed
  id := resolve-node-id_ store parsed["device"]
  app := parsed["app"]
  key := parsed.was-provided "key" ? parsed["key"] : null
  log := store.command-log id
  commands := log.map: | e/Map | Command e["verb"] e["args"]
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
    print-reconcile-warnings_ id app desired observed log [key]
    store.close
    return
  lines := render-config-table app desired observed
  print "$id: $lines[0]"
  lines[1..].do: print it
  keys := []
  desired.do --keys: keys.add it
  observed.do --keys: | k | if not desired.contains k: keys.add k
  print-reconcile-warnings_ id app desired observed log keys
  store.close

/**
Prints a self-heal warning for each of $keys that is still divergent (a non-empty
  $config-marker between $desired and $observed) and has been re-issued by
  gateway-reconcile >= 2 times in $log — a node that keeps failing to apply.
*/
print-reconcile-warnings_ id/string app/string desired/Map observed/Map log/List keys/List -> none:
  keys.do: | k/string |
    if (config-marker desired observed k) != "":
      n := reconcile-count log app k
      if n >= 2:
        print "$id: ⚠ $app.$k: self-healed $(n)× — node may be failing to apply"
```

- [ ] **Step 2: Verify it compiles**

Run: `$TS analyze gateway.toit`
Expected: exit 0, no output.

- [ ] **Step 3: Smoke-test the warning end-to-end on host**

Run (drives the store directly, no hardware — proves the warning fires and that a converged/once-reconciled key stays quiet):
```bash
$TS run gateway.toit -- --db=/tmp/porta-heal.db device set thermostat mode=heat -d aabbccddeeff
```
Then simulate two delivered reconcile re-issues against a node reporting the wrong value. Because there is no node to PUT reports, seed the divergence with a tiny throwaway script:
```bash
cat > /tmp/heal_smoke.toit <<'EOF'
import .store show Store encode-json_
import .command show Command
main:
  store := Store.open "/tmp/porta-heal.db"
  dev := "aabbccddeeff"
  store.ensure-node dev --now=1
  // Two delivered gateway-reconcile sets that never converged.
  2.repeat:
    cid := store.enqueue-command dev (Command.set --app="thermostat" --key="mode" --value="heat") --issued-by="gateway-reconcile" --now=(10 + it)
    store.mark-delivered cid --now=(20 + it)
  store.insert-report dev --observed-state=(encode-json_ {"apps": {:}, "config": {"thermostat": {"mode": "eco"}}}) --health=(encode-json_ {:}) --now=30
  store.close
EOF
cp /tmp/heal_smoke.toit ./heal_smoke.toit
$TS run heal_smoke.toit
$TS run gateway.toit -- --db=/tmp/porta-heal.db device get thermostat -d aabbccddeeff
rm heal_smoke.toit /tmp/heal_smoke.toit /tmp/porta-heal.db
```
Expected: the final `device get` prints the config table with `mode` marked `(drift)` AND a line like:
```
aabbccddeeff: ⚠ thermostat.mode: self-healed 2× — node may be failing to apply
```

- [ ] **Step 4: Commit**

```bash
git add gateway.toit
git commit -m "feat(gateway): device get warns when a key keeps failing to self-heal"
```

---

## Task 5: Hardware verification (fwkd)

**No code.** Confirm the loop heals on a live node and the warning surfaces under repeated failure. Build/flash/daemon recipe is in the `porta-toit-gateway` memory (build-envelope.sh → `jag flash … --exclude-jaguar`; daemon `$TS run gateway.toit -- --db=/tmp/porta-heal.db serve --port=6969`). Operate at 30s+ poll (tftp#5).

- [ ] **Step 1: Establish a converged baseline.** With a control app installed (e.g. `control-demo`), `device set control-demo target 21.5`; wait a poll; `device get control-demo` shows `target` converged (no marker), no warning.

- [ ] **Step 2: Force a divergence and confirm self-heal.** Cause the node to drop/ignore the applied value for one key (simplest: temporarily run a payload that does not persist the set, or hand-edit NVS), so its report echoes a stale/absent value while the set is delivered. Within one further poll, confirm the gateway daemon logs a `gateway-reconcile` enqueue and the next wake re-applies it — `device get` returns to converged.

- [ ] **Step 3: Force repeated failure and confirm the warning.** Keep the node failing to apply across ≥2 delivered reconcile re-issues; confirm `device get <app>` prints `⚠ <app>.<key>: self-healed N× — node may be failing to apply` (N ≥ 2). Confirm a healthy key never warns.

- [ ] **Step 4: Record the result** in `docs/specs/2026-05-24-config-self-heal-design.md` (Status line) and update the `porta-toit-gateway` memory.

---

## Self-review notes

- **Spec coverage:** reconcile seam → Task 1; in-flight guard + self-throttle → encoded in Task 1's `reconcile-config` logic and asserted by its in-flight/self-throttle tests; reconcile-on-report wiring (catch --trace, after insert-report) → Task 3; warning + `reconcile-count` → Tasks 2 & 4; observed-only / desired-never-shrinks edge → Task 1 "observed-only key" test; hardware → Task 5. No pre-D5 gate (removed from spec — all nodes run echo firmware).
- **Type consistency:** `reconcile-config command-log/List observed/Map -> List`, `reconcile-count command-log/List app/string key/string -> int`, re-issue via `Command verb args` and `enqueue-command … --issued-by="gateway-reconcile"` — identical across spec, tasks, and tests. The literal tag string is `"gateway-reconcile"` everywhere.
- **No placeholders:** every step has runnable code/commands and expected output.
