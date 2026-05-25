# M2.2 Down-Path (Config / Setpoints) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the symmetric write-side of the data plane — an operator can `gateway device set <app> <key>=<value>`, the value rides the existing command queue to the node, persists in a per-app NVS config store, and a running app reads it via a `ControlService`.

**Architecture:** Mirror M2.1. A new `set` command verb travels the *existing* command queue (no new gateway table). The node applies `set` during its poll drain by writing a single NVS blob `config = {appName: {key: value}}` (a plane *separate* from triggers/goal — `set` never touches `apply-to-goal`). A spawned `ControlServiceProvider` (alongside the telemetry provider) serves config to apps; under D4=A the app passes its own name: `client.get "<app>" "<key>"`. Values are typed scalars (int/float/bool/string), inferred from the CLI string, symmetric with the telemetry up-path. `device get` is desired-only — it projects the `set` commands from the command log (no device change).

**Tech Stack:** Toit (device under system `toit` v2.0.0-alpha.192; gateway under `toit-sqlite` at `~/workspaceToit/sqlite/build/bin/toit-sqlite`). `system.services` for the provider/client, `system.storage` Bucket for NVS. Tests are a `main` with `import expect show *` (no `toit test` on this SDK).

**Locked design decisions (from the 2026-05-24 brainstorm):**
- **D1** — values are typed scalars; the CLI infers `true`/`false`→bool, integer→int, decimal→float, else string. Symmetric with `TelemetryService.report`.
- **D2/D3** — one key per `set`; per-app config accumulates with last-write-wins; stored as a single NVS key `"config"` → `{appName: {key: value}}` (matches how `inventory` is one blob).
- **D4=A** — `ControlService.get app/string key/string`; the app passes its own registered name. Generalizes to always-on later (unlike injecting config via container args).
- **D5** — `device get` is desired-only (project the `set` command log). Observed-echo (report carries applied config) is a deliberate fast-follow, **out of scope here**.
- **Boundary** — *triggers* (`interval`, `boot`, `gpio-*`) are scheduling, supervisor-owned, already on the `run` command. *Config* (this plan) is app setpoints, app-owned. They do not interact.

**Toolchain setup (every gateway task):**
```bash
export TS=~/workspaceToit/sqlite/build/bin/toit-sqlite
cd /home/david/workspaceToit/porta/gateway
```
**Device test command:** `cd /home/david/workspaceToit/porta && toit run device/<file>_test.toit`
**Supervisor compile gate:** `toit compile -s -o /tmp/sup.snapshot device/supervisor.toit` (analyze-only; imports esp32/system, cannot `toit run`).

**Toit gotchas carried from M1/M2 (apply throughout):**
- `{:}` is an empty Map; `{}` is an empty Set.
- Map `==` is identity → use `expect-structural-equals` for Map equality.
- `map.get key --init=: <default>` is the get-or-create idiom (inserts and returns the default if absent).
- `json.encode` yields a ByteArray; the store's `encode-json_`/`decode-json_` already handle the TEXT-column round-trip — `command-log` returns `args` already decoded to a Map.
- A multiline call with trailing named args can misparse → hoist into a local.
- RPC across `system.services` preserves scalar runtime types (int stays int, float stays float — proven by `telemetry_service_test`).

---

## File Structure

**Gateway (host):**
- `gateway/command.toit` — MODIFY: add `VERB-SET`, `Command.set`, config accessors, `project-config`, `infer-scalar`.
- `gateway/command_test.toit` — MODIFY: cover the above.
- `gateway/gateway.toit` — MODIFY: add `device set` / `device get` subcommands + handlers.

**Device:**
- `device/config_store.toit` — CREATE: pure config-map helpers (`set-config`/`get-config`) + NVS load/save (`load-config`/`save-config`) + `CONFIG-KEY`.
- `device/config_store_test.toit` — CREATE.
- `device/control_service.toit` — CREATE: `ControlService` interface + `ControlServiceClient` + `ControlServiceProvider`.
- `device/control_service_test.toit` — CREATE.
- `device/node_command.toit` — MODIFY: add `VERB-SET`, `is-set`, config accessors.
- `device/node_command_test.toit` — MODIFY: cover `set` decode.
- `device/supervisor.toit` — MODIFY: apply `set` in the drain loop; spawn the control provider beside the telemetry provider.
- `device/control_demo.toit` — CREATE (Phase F): hardware payload that reads config and reports it back up.

---

## Phase D — Gateway host (no hardware)

### Task 1: `set` command model, projection, and value inference

**Files:**
- Modify: `gateway/command.toit`
- Test: `gateway/command_test.toit`

- [ ] **Step 1: Add the failing tests** — append to `gateway/command_test.toit` inside `main`, before any final print:

```toit
  // set round-trips through the wire encoding (typed value preserved).
  st := Command.decode (Command.set --app="thermostat" --key="target-c" --value=21.5).encode
  expect-equals VERB-SET st.verb
  expect-equals "thermostat" st.app
  expect-equals "target-c" st.config-key
  expect-equals 21.5 st.config-value
  expect (st.config-value is float)
  // int / bool / string values survive too.
  expect (Command.decode (Command.set --app="a" --key="n" --value=7).encode).config-value is int
  expect-equals true (Command.decode (Command.set --app="a" --key="on" --value=true).encode).config-value
  expect-equals "heat" (Command.decode (Command.set --app="a" --key="mode" --value="heat").encode).config-value

  // project-config folds set commands to {app:{key:value}}, last-write-wins, by app.
  cfg := project-config [
    Command.set --app="t" --key="target" --value=20,
    Command.set --app="t" --key="mode" --value="heat",
    Command.set --app="t" --key="target" --value=22,   // overwrites
    Command.set --app="s" --key="threshold" --value=100,
  ]
  expect-equals 22 cfg["t"]["target"]
  expect-equals "heat" cfg["t"]["mode"]
  expect-equals 100 cfg["s"]["threshold"]
  // a set does not appear in the goal-app projection (separate plane).
  expect (project [Command.set --app="t" --key="x" --value=1]).is-empty

  // infer-scalar types a CLI string.
  expect-equals true (infer-scalar "true")
  expect-equals false (infer-scalar "false")
  expect (infer-scalar "42") is int
  expect-equals 42 (infer-scalar "42")
  expect (infer-scalar "1.5") is float
  expect-equals 1.5 (infer-scalar "1.5")
  expect-equals "auto" (infer-scalar "auto")
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd /home/david/workspaceToit/porta/gateway && ~/workspaceToit/sqlite/build/bin/toit-sqlite run command_test.toit`
Expected: FAIL — `VERB-SET`/`Command.set`/`project-config`/`infer-scalar` undefined.

- [ ] **Step 3: Implement in `gateway/command.toit`**

Add the verb constant beside the others:
```toit
VERB-SET ::= "set"
```

Add the factory beside `Command.set-console`:
```toit
  /** Builds a command setting app $app's config $key to scalar $value (int/float/bool/string). */
  static set --app/string --key/string --value -> Command:
    return Command VERB-SET {"app": app, "key": key, "value": value}
```

Add accessors beside the existing ones (after `interval-s`):
```toit
  app -> string?: return args.get "app"
  config-key -> string?: return args.get "key"
  config-value -> any: return args.get "value"
```

Add `project-config` after `project`:
```toit
/**
Folds an ordered list of $commands into the desired config map: app name →
  {key: value}. Later sets for the same app/key win (declarative & absolute, like
  $project). Non-set verbs are ignored — config is a plane separate from the goal.
*/
project-config commands/List -> Map:
  config := {:}
  commands.do: | c/Command |
    if c.verb == VERB-SET:
      (config.get c.app --init=: {:})[c.config-key] = c.config-value
  return config
```

Add `infer-scalar` after `triggers-from-flags`:
```toit
/**
Types a CLI value string: "true"/"false" → bool, an integer → int, a decimal →
  float, anything else → the string unchanged. Mirrors the scalar surface of
  TelemetryService.report so the down-path and up-path agree on value types.
*/
infer-scalar value-str/string -> any:
  if value-str == "true": return true
  if value-str == "false": return false
  as-int := int.parse value-str --if-error=: null
  if as-int != null: return as-int
  as-float := float.parse value-str --if-error=: null
  if as-float != null: return as-float
  return value-str
```

- [ ] **Step 4: Run to verify it passes**

Run: `cd /home/david/workspaceToit/porta/gateway && ~/workspaceToit/sqlite/build/bin/toit-sqlite run command_test.toit`
Expected: PASS (no assertion failures; process exits 0).

- [ ] **Step 5: Commit**

```bash
cd /home/david/workspaceToit/porta
git add gateway/command.toit gateway/command_test.toit
git commit -m "feat(gateway): set command verb + project-config + infer-scalar (M2.2 down-path)"
```

### Task 2: `device set` / `device get` CLI

**Files:**
- Modify: `gateway/gateway.toit` (add two subcommands to `device-cmd`; add two handlers)

- [ ] **Step 1: Add the subcommands.** In `build-command`, after `device-set-console-cmd`, add:

```toit
  device-set-cmd := cli.Command "set"
      --help="Enqueue setting an app's config key (value typed: true/false→bool, int, float, else string)."
      --options=[ cli.Option "device" --short-name="d" --help="Node name or MAC." --required ]
      --rest=[
        cli.Option "app" --help="Target app name." --required,
        cli.Option "key" --help="Config key." --required,
        cli.Option "value" --help="Config value." --required,
      ]
      --run=:: cmd-device-set it

  device-get-cmd := cli.Command "get"
      --help="Show an app's desired config (projected from the set command log)."
      --options=[ cli.Option "device" --short-name="d" --help="Node name or MAC." --required ]
      --rest=[
        cli.Option "app" --help="Target app name." --required,
        cli.Option "key" --help="Optional single key; omit to show all.",
      ]
      --run=:: cmd-device-get it
```

Then add both to the `device-cmd` subcommand list:
```toit
  device-cmd := cli.Command "device"
      --help="Inspect and configure a node."
      --subcommands=[
        device-show-cmd, device-set-max-offline-cmd,
        device-set-poll-interval-cmd, device-name-cmd, device-set-console-cmd,
        device-set-cmd, device-get-cmd,
      ]
```

- [ ] **Step 2: Add the handlers.** After `cmd-device-set-console`, add:

```toit
cmd-device-set parsed/cli.Parsed -> none:
  store := open-store_ parsed
  id := resolve-node-id_ store parsed["device"]
  store.ensure-node id --now=now_
  app := parsed["app"]
  key := parsed["key"]
  value := infer-scalar parsed["value"]
  cmd-id := store.enqueue-command id (Command.set --app=app --key=key --value=value) --issued-by="cli" --now=now_
  print "$id: enqueued set $app.$key=$value (command #$cmd-id)"
  store.close

cmd-device-get parsed/cli.Parsed -> none:
  store := open-store_ parsed
  id := resolve-node-id_ store parsed["device"]
  app := parsed["app"]
  key := parsed.get "key"
  commands := (store.command-log id).map: | e/Map | Command e["verb"] e["args"]
  desired := (project-config commands).get app --if-absent=: {:}
  if key != null:
    if desired.contains key: print "$id: $app.$key = $(desired[key])"
    else: print "$id: $app.$key is unset"
    store.close
    return
  if desired.is-empty:
    print "$id: $app has no desired config"
  else:
    print "$id: desired config for $app:"
    desired.do: | k v | print "  $k = $v"
  store.close
```

(`Command e["verb"] e["args"]` reuses the public `Command` constructor; `command-log` already returns `args` decoded to a Map. No store change is needed — the command queue is verb-agnostic.)

- [ ] **Step 3: Verify the CLI end-to-end against a temp db.** Run:

```bash
cd /home/david/workspaceToit/porta/gateway
export TS=~/workspaceToit/sqlite/build/bin/toit-sqlite
rm -f /tmp/m22-cli.db
$TS gateway.toit --db /tmp/m22-cli.db device set -d aabbccddeeff thermostat target-c 21.5
$TS gateway.toit --db /tmp/m22-cli.db device set -d aabbccddeeff thermostat mode heat
$TS gateway.toit --db /tmp/m22-cli.db device set -d aabbccddeeff thermostat target-c 22
$TS gateway.toit --db /tmp/m22-cli.db device get -d aabbccddeeff thermostat
$TS gateway.toit --db /tmp/m22-cli.db device get -d aabbccddeeff thermostat target-c
$TS gateway.toit --db /tmp/m22-cli.db device get -d aabbccddeeff thermostat missing
```
Expected output (last four lines):
```
aabbccddeeff: desired config for thermostat:
  target-c = 22
  mode = heat
aabbccddeeff: thermostat.target-c = 22
aabbccddeeff: thermostat.missing is unset
```
(`target-c` shows `22` as an int because `infer-scalar "22"` → int; the last write wins over `21.5`.)

- [ ] **Step 4: Commit**

```bash
cd /home/david/workspaceToit/porta
git add gateway/gateway.toit
git commit -m "feat(gateway): device set/get CLI for app config (M2.2 down-path)"
```

---

## Phase E — Device host (no hardware)

### Task 3: `config_store.toit` — pure config-map helpers + NVS load/save

**Files:**
- Create: `device/config_store.toit`
- Test: `device/config_store_test.toit`

- [ ] **Step 1: Write the failing test** — create `device/config_store_test.toit`:

```toit
// device/config_store_test.toit
import expect show *
import .config_store show set-config get-config

main:
  c := {:}
  set-config c "thermostat" "target-c" 21.5
  set-config c "thermostat" "mode" "heat"
  set-config c "sampler" "threshold" 100
  expect-equals 21.5 (get-config c "thermostat" "target-c")
  expect (get-config c "thermostat" "target-c") is float
  expect-equals "heat" (get-config c "thermostat" "mode")
  expect-equals 100 (get-config c "sampler" "threshold")
  expect-null (get-config c "thermostat" "missing")   // unknown key
  expect-null (get-config c "absent" "k")              // unknown app
  set-config c "thermostat" "target-c" 22.0            // overwrite
  expect-equals 22.0 (get-config c "thermostat" "target-c")
  print "config_store OK"
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd /home/david/workspaceToit/porta && toit run device/config_store_test.toit`
Expected: FAIL — cannot find `.config_store`.

- [ ] **Step 3: Implement `device/config_store.toit`**

```toit
// device/config_store.toit — the node's per-app config (setpoints) store. The
// config is one NVS blob: {appName: {key: value}}, separate from the goal/triggers
// plane. The supervisor writes it when a `set` command is drained; the
// ControlServiceProvider reads it for apps. Map helpers are pure (host-testable);
// load/save wrap the NVS bucket.
import system.storage

/** NVS key (in the supervisor's bucket) holding the {app:{key:value}} config blob. */
CONFIG-KEY ::= "config"

/** Sets app $app's $key to $value in the in-memory config map $config (creates the app sub-map). */
set-config config/Map app/string key/string value -> none:
  (config.get app --init=: {:})[key] = value

/** Returns app $app's $key from $config, or null if the app or key is absent. */
get-config config/Map app/string key/string -> any:
  app-map := config.get app --if-absent=: return null
  return app-map.get key

/** Loads the config blob from NVS, or an empty map if none stored yet. */
load-config bucket/storage.Bucket -> Map:
  return bucket.get CONFIG-KEY --if-absent=: {:}

/** Persists the config blob to NVS. */
save-config bucket/storage.Bucket config/Map -> none:
  bucket[CONFIG-KEY] = config
```

- [ ] **Step 4: Run to verify it passes**

Run: `cd /home/david/workspaceToit/porta && toit run device/config_store_test.toit`
Expected: PASS — prints `config_store OK`.

- [ ] **Step 5: Commit**

```bash
cd /home/david/workspaceToit/porta
git add device/config_store.toit device/config_store_test.toit
git commit -m "feat(device): per-app config store helpers (M2.2 down-path)"
```

### Task 4: `control_service.toit` — the ControlService provider/client

**Files:**
- Create: `device/control_service.toit`
- Test: `device/control_service_test.toit`

- [ ] **Step 1: Write the failing test** — create `device/control_service_test.toit` (mirrors `telemetry_service_test.toit`: spawn the provider, then drive a client; the provider reads config via an injected lambda so the test needs no flash):

```toit
// device/control_service_test.toit
import expect show *
import .control_service show ControlServiceClient ControlServiceProvider

main:
  config := {
    "thermostat": {"target-c": 21.5, "mode": "heat"},
    "sampler": {"threshold": 100, "enabled": true},
  }
  spawn::
    provider := ControlServiceProvider:: config   // read-config lambda
    provider.install
    sleep (Duration --s=2)
    provider.uninstall
  sleep --ms=200  // let the provider register before we open a client.

  client := ControlServiceClient
  client.open
  // Typed values survive the service boundary.
  expect-equals 21.5 (client.get "thermostat" "target-c")
  expect (client.get "thermostat" "target-c") is float
  expect-equals "heat" (client.get "thermostat" "mode")
  expect-equals 100 (client.get "sampler" "threshold")
  expect (client.get "sampler" "threshold") is int
  expect-equals true (client.get "sampler" "enabled")
  // Absent app or key → null.
  expect-null (client.get "thermostat" "missing")
  expect-null (client.get "absent" "k")
  client.close
  print "control service OK"
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd /home/david/workspaceToit/porta && toit run device/control_service_test.toit`
Expected: FAIL — cannot find `.control_service`.

- [ ] **Step 3: Implement `device/control_service.toit`**

```toit
// device/control_service.toit — the device-wide config (setpoints) read API.
// A payload app opens a ControlServiceClient and calls `get <its-app-name> <key>`.
// The provider (spawned by the supervisor) answers from a config map supplied by a
// read-config lambda — in production that lambda reads the NVS config blob live, so
// a `set` drained earlier this wake is visible. See device/config_store.toit.
import system.services
import .config_store show get-config

interface ControlService:
  static SELECTOR ::= services.ServiceSelector
      --uuid="9d4e1f72-6a3b-4c2e-8f1d-2b7c5a9e0d83"
      --major=1
      --minor=0
  /** Returns app $app's config $key (int/float/bool/string), or null if unset. */
  get app/string key/string -> any
  static GET-INDEX ::= 0

class ControlServiceClient extends services.ServiceClient implements ControlService:
  static SELECTOR ::= ControlService.SELECTOR
  constructor selector/services.ServiceSelector=SELECTOR:
    assert: selector.matches SELECTOR
    super selector

  get app/string key/string -> any: return invoke_ ControlService.GET-INDEX [app, key]

class ControlServiceProvider extends services.ServiceProvider
    implements ControlService services.ServiceHandler:
  read-config_/Lambda   // -> Map ; called per get so config stays live
  constructor .read-config_:
    super "porta/control" --major=1 --minor=0
    provides ControlService.SELECTOR --handler=this

  handle index/int arguments/any --gid/int --client/int -> any:
    if index == ControlService.GET-INDEX: return get arguments[0] arguments[1]
    unreachable

  get app/string key/string -> any: return get-config read-config_.call app key
```

- [ ] **Step 4: Run to verify it passes**

Run: `cd /home/david/workspaceToit/porta && toit run device/control_service_test.toit`
Expected: PASS — prints `control service OK`.

- [ ] **Step 5: Commit**

```bash
cd /home/david/workspaceToit/porta
git add device/control_service.toit device/control_service_test.toit
git commit -m "feat(device): ControlService provider/client for app config (M2.2 down-path)"
```

### Task 5: decode the `set` command on the device

**Files:**
- Modify: `device/node_command.toit`
- Test: `device/node_command_test.toit`

- [ ] **Step 1: Add the failing test** — append inside `main` in `device/node_command_test.toit`:

```toit
  // set decodes with typed value; it is NOT applied to the goal-app map.
  set-cmd := NodeCommand.decode (json.encode {"verb": "set", "app": "thermostat", "key": "target-c", "value": 21.5})
  expect set-cmd.is-set
  expect-equals "thermostat" set-cmd.app
  expect-equals "target-c" set-cmd.config-key
  expect-equals 21.5 set-cmd.config-value
  expect (set-cmd.config-value is float)
  goal := {:}
  apply-to-goal goal set-cmd      // set is a no-op on the goal plane
  expect goal.is-empty
```

Confirm `import encoding.json` is present at the top of `device/node_command_test.toit`; if not, add it.

- [ ] **Step 2: Run to verify it fails**

Run: `cd /home/david/workspaceToit/porta && toit run device/node_command_test.toit`
Expected: FAIL — `is-set`/`app`/`config-key`/`config-value` undefined.

- [ ] **Step 3: Implement in `device/node_command.toit`**

Add the verb constant beside the others:
```toit
VERB-SET ::= "set"
```

Add accessors + predicate beside the existing ones (after `is-set-console`):
```toit
  is-set -> bool: return verb == VERB-SET
  app -> string?: return args.get "app"
  config-key -> string?: return args.get "key"
  config-value -> any: return args.get "value"
```

Leave `apply-to-goal` unchanged — `set` is applied by the supervisor to NVS, not to the goal map.

- [ ] **Step 4: Run to verify it passes**

Run: `cd /home/david/workspaceToit/porta && toit run device/node_command_test.toit`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /home/david/workspaceToit/porta
git add device/node_command.toit device/node_command_test.toit
git commit -m "feat(device): decode set command (M2.2 down-path)"
```

### Task 6: wire the supervisor — apply `set`, spawn the control provider

**Files:**
- Modify: `device/supervisor.toit`

This task has no host unit test (the supervisor imports `esp32`/`system` and is analyze-only). The gate is a clean compile; behavior is verified on hardware in Task 7.

- [ ] **Step 1: Add the import.** With the other `.`-imports near the top of `device/supervisor.toit`, add:

```toit
import .config_store show CONFIG-KEY load-config save-config set-config
import .control_service show ControlServiceProvider
```

- [ ] **Step 2: Apply `set` during the drain.** In `poll-and-reconcile`, in the `while true` command-drain loop, add a branch before the final `else:` (which currently calls `apply-to-goal`):

```toit
      else if command.is-set:
        config := load-config bucket
        set-config config command.app command.config-key command.config-value
        save-config bucket config
        print "supervisor: set $(command.app).$(command.config-key) = $(command.config-value)"
```

So the chain reads `if command.is-set-poll: … else if command.is-set-console: … else if command.is-set: … else: apply-to-goal …`.

- [ ] **Step 3: Spawn the control provider beside the telemetry provider.** Replace the body of `spawn-remoting_` with:

```toit
spawn-remoting_ -> none:
  spawn::
    catch --trace:
      provider := TelemetryServiceProvider (TelemetryBuffer --cap=128)
      provider.install
      // ControlService reads the NVS config blob live (its own bucket handle in
      // this spawned process), so a `set` drained later this wake is visible.
      config-bucket := storage.Bucket.open --flash BUCKET-NAME
      control := ControlServiceProvider:: load-config config-bucket
      control.install
      print "supervisor: telemetry + control providers registered"
      while true: sleep (Duration --s=3600)  // outlive the wake window; deep-sleep ends it
```

(`storage` and `BUCKET-NAME` are already in scope in `supervisor.toit`.)

- [ ] **Step 4: Compile gate**

Run: `cd /home/david/workspaceToit/porta && toit compile -s -o /tmp/sup.snapshot device/supervisor.toit`
Expected: compiles; the only warning is the pre-existing `schedule_store.toit:26 rtc-user-bytes` deprecation. No errors.

- [ ] **Step 5: Run the full device host suite (regression check)**

Run:
```bash
cd /home/david/workspaceToit/porta
for t in node_command report inventory goal_state triggers image_writer telemetry_buffer telemetry_codec telemetry_service set_console_apply config_store control_service; do
  echo "== $t ==" ; toit run device/$t\_test.toit ; done
```
Expected: every suite prints its `OK` line / exits 0.

- [ ] **Step 6: Commit**

```bash
cd /home/david/workspaceToit/porta
git add device/supervisor.toit
git commit -m "feat(device): apply set to NVS config + spawn ControlService provider (M2.2 down-path)"
```

---

## Phase F — Hardware verification (fwkd)

### Task 7: end-to-end down-path on fwkd

**Files:**
- Create: `device/control_demo.toit`

This proves the full loop: operator `set` → command queue → node NVS → `ControlService` → app reads its own value → app reports it back up → `gateway monitor` shows it.

- [ ] **Step 1: Create the demo payload** `device/control_demo.toit`:

```toit
// device/control_demo.toit — M2.2 hardware demo. Reads its own config via
// ControlService and echoes it back up via TelemetryService, so the down-path is
// observable in `gateway monitor`. Install as app name "control-demo".
import .control_service show ControlServiceClient
import .telemetry_service show TelemetryServiceClient

APP ::= "control-demo"

main:
  control := ControlServiceClient
  control.open
  target := control.get APP "target"      // set via `device set -d <id> control-demo target=<v>`
  control.close

  tel := TelemetryServiceClient
  tel.open
  tel.log "control-demo: target=$target"
  if target != null: tel.report "target" target   // echo the typed value back up
  tel.close
  print "control-demo: done (target=$target)"
```

- [ ] **Step 2: Build the payload image (SDK-matched, 32-bit)**

```bash
cd /home/david/workspaceToit/porta
toit compile -s -o control_demo.snapshot device/control_demo.toit
toit tool snapshot-to-image -m32 --format=binary -o control_demo.bin control_demo.snapshot
ls -la control_demo.bin   # expect a non-zero .bin
```

- [ ] **Step 3: Rebuild + flash the supervisor envelope** (picks up the Task 6 supervisor + new device files via the tftp path-dep recompile):

```bash
cd /home/david/workspaceToit/porta
rm -f firmware-esp32.envelope supervisor.image supervisor.snapshot
bash host/build-envelope.sh    # expect: supervisor installed, no jaguar, SDK v2.0.0-alpha.192
# Flash (your WiFi creds; device on /dev/ttyUSB0):
#   jag flash firmware-esp32.envelope --exclude-jaguar --wifi-ssid <S> --wifi-password <P> --port /dev/ttyUSB0
```
(If fwkd already runs a current supervisor and only device-side service code changed, a reflash is still required — the providers live in the supervisor image.)

- [ ] **Step 4: Start the gateway daemon on a fresh db** (separate terminal; note: gateway CLI uses NO `--` separator):

```bash
export TS=~/workspaceToit/sqlite/build/bin/toit-sqlite
cd /home/david/workspaceToit/porta/gateway
$TS gateway.toit --db /tmp/porta-fwkd-m22.db serve
# expect: "gateway: serving command queue + payloads on UDP/6969"
```

- [ ] **Step 5: Provision the down-path.** After the node registers (`$TS gateway.toit --db /tmp/porta-fwkd-m22.db scan` shows `30aea41a6208`), from a second terminal:

```bash
export TS=~/workspaceToit/sqlite/build/bin/toit-sqlite
cd /home/david/workspaceToit/porta/gateway
ID=30aea41a6208
$TS gateway.toit --db /tmp/porta-fwkd-m22.db container install control-demo ../control_demo.bin -d $ID --interval=30s
$TS gateway.toit --db /tmp/porta-fwkd-m22.db device set-console on -d $ID
$TS gateway.toit --db /tmp/porta-fwkd-m22.db device set -d $ID control-demo target 42
$TS gateway.toit --db /tmp/porta-fwkd-m22.db device get -d $ID control-demo
# expect: "30aea41a6208: control-demo.target = 42"
```

- [ ] **Step 6: Observe the loop close.** Watch `jag monitor -a --port /dev/ttyUSB0` for, across wakes:
  - `supervisor: telemetry + control providers registered`
  - `supervisor: set control-demo.target = 42` (the `set` drained)
  - `supervisor: installed control-demo …` then `control-demo: done (target=42)`

  Then on the gateway:
```bash
$TS gateway.toit --db /tmp/porta-fwkd-m22.db monitor -d $ID --since 1h
# expect rows: `log  control-demo: target=42` and `metric  target=42`
```
  This confirms: the typed value flowed operator → queue → NVS → ControlService → app, and the app read *its own* config by name (D4=A) and echoed it back up.

- [ ] **Step 7: Verify a live update.** Change the value and confirm the next wake reflects it:

```bash
$TS gateway.toit --db /tmp/porta-fwkd-m22.db device set -d $ID control-demo target 99
# wait one wake, then:
$TS gateway.toit --db /tmp/porta-fwkd-m22.db monitor -d $ID --since 1h --kind metric
# expect a newer `target=99` row
```
  This is the cross-process NVS read check: the `set` written by the supervisor's main process during poll is seen by the spawned provider's `load-config` on the same wake.

  **Contingency (if the spawned provider can't open a second `--flash` bucket concurrently with main, or reads stale):** install `ControlServiceProvider` in the supervisor's *main* process (it already holds `bucket`) just before `start-installed`, instead of inside `spawn-remoting_`; the main process services RPC during the `sleep OBSERVE` window. Re-verify Steps 6–7. Record whichever path worked.

- [ ] **Step 8: Record the result + commit.** Add a "Hardware verification result" note to `docs/specs/2026-05-24-m2-telemetry-design.md` (under the M2.2 milestone) capturing: fwkd node id, the observed monitor rows, and which provider-hosting path (spawned vs main) was used.

```bash
cd /home/david/workspaceToit/porta
git add device/control_demo.toit docs/specs/2026-05-24-m2-telemetry-design.md
git commit -m "test(device): M2.2 down-path hardware-verified on fwkd (control-demo)"
```

(`control_demo.snapshot`/`.bin` are build artifacts — confirm they match the existing `.gitignore` payload-artifact patterns; add `/control_demo.bin` and `/control_demo.snapshot` there if not covered.)

---

## Self-Review

**1. Spec coverage** (against `m2-telemetry-design.md` §94–96, §210–216, §266 and the brainstorm decisions):
- `set <app> <key>=<value>` verb on the command queue, no new table → Task 1 (model) + Task 2 (CLI) + reuses `enqueue-command`/`command-log`. ✓
- Per-app NVS config store → Task 3 (`config_store`) + Task 6 (supervisor apply). ✓
- `ControlService` serving config to apps, D4=A (app passes name) → Task 4 + Task 6 (spawn) + Task 7 (app reads by name). ✓
- D1 typed values, symmetric with telemetry → `infer-scalar` (Task 1) + typed assertions in Tasks 1/3/4 + RPC type-preservation note. ✓
- D5 desired-only `get` (no report change) → Task 2 `cmd-device-get` projects the command log; no device report edit. ✓
- Triggers/config separation → Task 5 asserts `apply-to-goal` ignores `set`; Task 1 asserts `project` ignores `set`. ✓

**2. Placeholder scan:** every code step shows complete code; every run step gives an exact command + expected output. Task 7 is hardware (manual) by nature, with exact commands and a named contingency. No TBD/TODO/"handle errors". ✓

**3. Type consistency:** accessor names `app` / `config-key` / `config-value` and verb `VERB-SET`/`"set"` are identical across `gateway/command.toit` and `device/node_command.toit`. `ControlService.get app key` matches client, provider, test, and `control_demo`. `set-config`/`get-config`/`load-config`/`save-config` signatures match between `config_store.toit`, its test, and the supervisor call sites. `CONFIG-KEY` is defined once (in `config_store.toit`) and imported by the supervisor. ✓
