# Gateway B1 — TFTP-free core Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the entire host-testable core of the Porta Toit gateway — the sqlite store, the command wire-codec, and the `jag`-aligned CLI — with zero dependency on the TFTP refactor (Spec A) or hardware.

**Architecture:** A new flat Toit app package `gateway/` (replacing the Go `gateway/`, which is renamed `gateway-go/`). A `Store` class wraps a sqlite database (`nodes`, `payloads`, `command_queue`, `reports`); a `Command` value type encodes/decodes the declarative wire commands and projects a command list to a goal-app map; `gateway.toit` is a `pkg-cli` command tree whose every leaf opens the store and reads/writes it. The TFTP daemon (`serve.toit`), the `StoreBackedHandler`, and the device changes are **B2** and are deliberately absent here.

**Tech Stack:** Toit (run on host via the prebuilt `toit-sqlite` binary), `sqlite` (`~/workspaceToit/sqlite`), `pkg-cli` 1.7.0, `pkg-host`, `crypto.crc` (core), `encoding.json` (core).

---

## Conventions for every task

- **The toolchain is the prebuilt `toit-sqlite` binary** (the system `toit` is SDK alpha-192; the `sqlite` package pins alpha-193, so plain `toit` cannot resolve it — `toit-sqlite` carries its own bundled SDK). Set this once per shell:

  ```bash
  export TS=~/workspaceToit/sqlite/build/bin/toit-sqlite
  $TS version    # sanity: prints a v2.0.0-alpha.x line
  ```

  If the binary is missing, build it: `make -C ~/workspaceToit/sqlite` (produces `build/bin/toit-sqlite`).

- **Run all `$TS` test/CLI commands from the `gateway/` directory** (so package resolution finds `gateway/package.yaml`). Each step's `Run:` assumes `cwd = porta/gateway`.
- **Toit conventions:** kebab-case functions/vars, `PascalCase` classes, `KEBAB-CASE` constants, 2-space indent, 4-space continuation; private members end `_`; comments are full sentences; Toitdoc method comments start with a third-person verb and use `$name` to reference code. Filenames use the porta convention: lowercase, words joined (`store.toit`), tests suffixed `_test.toit` alongside source (mirrors `device/goal_state_test.toit`).
- **A test file** is `import expect show *` + a `main:` that asserts with `expect-equals` / `expect` / `expect-throw`; it passes iff the process exits 0. There is no test framework — `$TS some_test.toit` runs it.
- **JSON-in-sqlite rule (verified):** `json.encode` returns a `ByteArray`. Storing that into a TEXT column makes it a BLOB and it reads back as a `ByteArray`. So **always store JSON as a string** (`(json.encode obj).to-string`) and decode with `json.decode s.to-byte-array`. The store's `encode-json_` / `decode-json_` helpers (Task 7) enforce this.

## File structure (final state after B1)

```
porta/
  gateway-go/          ← the former Go gateway/ (renamed; untouched otherwise)
  gateway/             ← NEW flat Toit app package (this plan)
    package.yaml         deps: sqlite (path), cli, host
    crc32.toit           CRC32-IEEE, byte-identical to the device's verify
    duration.toit        "30s"/"5m"/"1h"/"2d"/bare-int → seconds
    command.toit         Command value type + wire codec + triggers-from-flags + project
    names.toit           deterministic jag-style auto-name from a MAC
    store.toit           Store: sqlite schema + nodes/payloads/command_queue/reports
    gateway.toit         pkg-cli command tree (main); every leaf opens the Store
    crc32_test.toit  duration_test.toit  command_test.toit  names_test.toit
    store_test.toit  integration_test.toit
    README.md
```

`serve.toit`, `handler.toit`, and all device-side changes are **B2** and are not created here.

---

### Task 1: Scaffold the package and rename the Go gateway

**Files:**
- Rename: `gateway/` → `gateway-go/`
- Create: `gateway/package.yaml`
- Create: `gateway/.gitignore`

- [ ] **Step 1: Rename the Go gateway out of the way**

```bash
cd ~/workspaceToit/porta
git mv gateway gateway-go
```

- [ ] **Step 2: Verify the Go module still builds under its new path** (a directory move does not change the Go module path in `go.mod`, so this should be a no-op confirmation)

Run: `cd ~/workspaceToit/porta/gateway-go && go build ./... 2>&1 | head`
Expected: no output (clean build), or the same pre-existing warnings as before the move. Then `cd ~/workspaceToit/porta`.

- [ ] **Step 3: Create the new package manifest**

`gateway/package.yaml`:

```yaml
name: gateway
description: Porta Toit gateway — command-queue control plane + sqlite store (host).
dependencies:
  # NOTE: machine-specific absolute path (per CLAUDE.md, packages live under
  # ~/workspaceToit/). A clone on a different layout must adjust this path.
  sqlite:
    path: /home/david/workspaceToit/sqlite
  cli:
    url: github.com/toitlang/pkg-cli
    version: ^1.7.0
  host:
    url: github.com/toitlang/pkg-host
    version: ^1.16.0
```

`gateway/.gitignore`:

```
.packages/
*.db
*.db-wal
*.db-shm
```

- [ ] **Step 4: Resolve dependencies**

Run: `cd ~/workspaceToit/porta/gateway && export TS=~/workspaceToit/sqlite/build/bin/toit-sqlite && $TS pkg install`
Expected: creates `gateway/package.lock` and a `.packages/` tree; no error.

- [ ] **Step 5: Commit**

```bash
cd ~/workspaceToit/porta
git add -A
git commit -m "feat(gateway): scaffold Toit gateway package; rename Go gateway → gateway-go

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: CRC32-IEEE helper

**Files:**
- Create: `gateway/crc32.toit`
- Test: `gateway/crc32_test.toit`

- [ ] **Step 1: Write the failing test**

`gateway/crc32_test.toit`:

```toit
import expect show *
import .crc32 show crc32

main:
  // Canonical CRC32-IEEE check value for the ASCII string "123456789".
  expect-equals 0xCBF4_3926 (crc32 "123456789".to-byte-array)
  // Empty input: initial 0xffffffff XOR-ed with 0xffffffff is 0.
  expect-equals 0 (crc32 #[])
  // Stability: same bytes → same value.
  bytes := #[0xde, 0xad, 0xbe, 0xef]
  expect-equals (crc32 bytes) (crc32 bytes)
```

- [ ] **Step 2: Run test to verify it fails**

Run: `$TS crc32_test.toit`
Expected: FAIL — cannot resolve import `.crc32` / `crc32` not defined.

- [ ] **Step 3: Write minimal implementation**

`gateway/crc32.toit`:

```toit
// gateway/crc32.toit — CRC32-IEEE, byte-identical to the device-side image check.
import crypto.crc

/**
Computes the CRC32-IEEE checksum of $bytes.

Uses the same parameters as the device's image verifier
  (device/image_writer.toit) and jaguar's X-Jaguar-CRC32, so a value computed
  here matches what the node recomputes while streaming the image.
*/
crc32 bytes/ByteArray -> int:
  summer := crc.Crc.little-endian 32
      --polynomial=0xEDB88320
      --initial-state=0xffff_ffff
      --xor-result=0xffff_ffff
  summer.add bytes
  return summer.get-as-int
```

- [ ] **Step 4: Run test to verify it passes**

Run: `$TS crc32_test.toit`
Expected: PASS (process exits 0, no output).

- [ ] **Step 5: Commit**

```bash
cd ~/workspaceToit/porta
git add gateway/crc32.toit gateway/crc32_test.toit
git commit -m "feat(gateway): CRC32-IEEE helper matching device image verify

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: Duration parsing

**Files:**
- Create: `gateway/duration.toit`
- Test: `gateway/duration_test.toit`

- [ ] **Step 1: Write the failing test**

`gateway/duration_test.toit`:

```toit
import expect show *
import .duration show parse-duration-s

main:
  expect-equals 30 (parse-duration-s "30s")
  expect-equals 300 (parse-duration-s "5m")
  expect-equals 3600 (parse-duration-s "1h")
  expect-equals 172800 (parse-duration-s "2d")
  expect-equals 45 (parse-duration-s "45")        // bare integer = seconds
  expect-throw "invalid duration: ": parse-duration-s ""
  expect-throw "invalid duration unit: 10x": parse-duration-s "10x"
  expect-throw "invalid duration: ah": parse-duration-s "ah"
```

- [ ] **Step 2: Run test to verify it fails**

Run: `$TS duration_test.toit`
Expected: FAIL — `parse-duration-s` not defined.

- [ ] **Step 3: Write minimal implementation**

`gateway/duration.toit`:

```toit
// gateway/duration.toit — parse jag/artemis-style durations to whole seconds.

/**
Parses a duration $text such as "30s", "5m", "1h", "2d", or a bare integer
  (interpreted as seconds), returning whole seconds.

Throws a descriptive string on an empty value, a non-numeric magnitude, or an
  unknown unit suffix.
*/
parse-duration-s text/string -> int:
  if text == "": throw "invalid duration: (empty)"
  last := text[text.size - 1]
  if '0' <= last <= '9':
    return int.parse text --on-error=: throw "invalid duration: $text"
  magnitude := int.parse text[..text.size - 1] --on-error=: throw "invalid duration: $text"
  if last == 's': return magnitude
  if last == 'm': return magnitude * 60
  if last == 'h': return magnitude * 3600
  if last == 'd': return magnitude * 86400
  throw "invalid duration unit: $text"
```

- [ ] **Step 4: Run test to verify it passes**

Run: `$TS duration_test.toit`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd ~/workspaceToit/porta
git add gateway/duration.toit gateway/duration_test.toit
git commit -m "feat(gateway): duration string → seconds parser

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: Command model + wire codec

**Files:**
- Create: `gateway/command.toit`
- Test: `gateway/command_test.toit`

- [ ] **Step 1: Write the failing test**

`gateway/command_test.toit`:

```toit
import expect show *
import .command show *

main:
  // run round-trips through the wire encoding.
  c := Command.run --name="blink" --crc=7 --triggers={"interval": 30}
  bytes := c.encode
  expect (bytes.size >= 1)            // the "real command ≥ 1 byte" invariant
  d := Command.decode bytes
  expect-equals VERB-RUN d.verb
  expect-equals "blink" d.name
  expect-equals 7 d.crc
  expect-equals 30 d.triggers["interval"]
  expect-equals 3 d.runlevel          // default
  expect-equals [] d.arguments        // default

  // stop.
  s := Command.decode (Command.stop --name="blink").encode
  expect-equals VERB-STOP s.verb
  expect-equals "blink" s.name

  // set-poll-interval.
  p := Command.decode (Command.set-poll-interval --interval-s=5).encode
  expect-equals VERB-SET-POLL-INTERVAL p.verb
  expect-equals 5 p.interval-s
```

- [ ] **Step 2: Run test to verify it fails**

Run: `$TS command_test.toit`
Expected: FAIL — `Command` / `VERB-RUN` not defined.

- [ ] **Step 3: Write minimal implementation**

`gateway/command.toit`:

```toit
// gateway/command.toit — the operator command model, its wire codec, and the
// goal projection used to reason about idempotency.
import encoding.json

VERB-RUN ::= "run"
VERB-STOP ::= "stop"
VERB-SET-POLL-INTERVAL ::= "set-poll-interval"

/**
An operator command targeted at a node.

Commands are declarative and absolute, so applying one is idempotent and safe to
  redeliver. A command is stored as a $verb plus an $args map (the verb-specific
  payload) and travels on the wire as a single JSON object ($encode). Any real
  command encodes to at least one byte, which is what lets a zero-byte TFTP body
  mean "command queue drained".
*/
class Command:
  verb/string
  args/Map

  constructor .verb .args:

  /**
  Builds a run command: the node should run app $name from image $crc under the
    given $triggers ({type:value} map, see device/triggers.toit), at $runlevel,
    with container $arguments.
  */
  static run --name/string --crc/int --triggers/Map --runlevel/int=3 --arguments/List=[] -> Command:
    return Command VERB-RUN {
      "name": name,
      "crc": crc,
      "triggers": triggers,
      "runlevel": runlevel,
      "arguments": arguments,
    }

  /** Builds a stop command: the node should not run app $name. */
  static stop --name/string -> Command:
    return Command VERB-STOP {"name": name}

  /** Builds a command setting the node's wake/poll cadence to $interval-s seconds. */
  static set-poll-interval --interval-s/int -> Command:
    return Command VERB-SET-POLL-INTERVAL {"interval": interval-s}

  /** Serializes this command to its JSON wire form. */
  encode -> ByteArray:
    m := {"verb": verb}
    args.do: | key value | m[key] = value
    return json.encode m

  /** Reconstructs a $Command from its JSON wire form $bytes. */
  static decode bytes/ByteArray -> Command:
    obj := json.decode bytes
    a := {:}
    obj.do: | key value | if key != "verb": a[key] = value
    return Command obj["verb"] a

  name -> string?: return args.get "name"
  crc -> int?: return args.get "crc"
  triggers -> Map?: return args.get "triggers"
  runlevel -> int?: return args.get "runlevel"
  arguments -> List?: return args.get "arguments"
  interval-s -> int?: return args.get "interval"
```

- [ ] **Step 4: Run test to verify it passes**

Run: `$TS command_test.toit`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd ~/workspaceToit/porta
git add gateway/command.toit gateway/command_test.toit
git commit -m "feat(gateway): Command model + JSON wire codec

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: Trigger-flag parsing + goal projection (idempotency)

**Files:**
- Modify: `gateway/command.toit` (append two functions)
- Modify: `gateway/command_test.toit` (append assertions)

- [ ] **Step 1: Add the failing assertions**

Append to `gateway/command_test.toit`'s `main:`:

```toit
  // triggers-from-flags builds the device {type:value} trigger map.
  t := triggers-from-flags ["boot", "gpio-high=33"] --interval-s=30
  expect-equals 1 t["boot"]
  expect-equals 33 t["gpio-high:33"]
  expect-equals 30 t["interval"]
  expect-throw "unknown trigger: bogus": triggers-from-flags ["bogus"] --interval-s=null

  // project folds a command list to the goal-app map; it is idempotent.
  run-x := Command.run --name="x" --crc=1 --triggers={"interval": 10}
  expect-equals (project [run-x]) (project [run-x, run-x])      // re-run is a no-op
  expect (project [run-x, (Command.stop --name="x")]).is-empty  // stop removes
  later := project [run-x, (Command.run --name="x" --crc=2 --triggers={})]
  expect-equals 2 later["x"]["crc"]                             // later run wins
```

- [ ] **Step 2: Run test to verify it fails**

Run: `$TS command_test.toit`
Expected: FAIL — `triggers-from-flags` / `project` not defined.

- [ ] **Step 3: Implement**

Append to `gateway/command.toit`:

```toit
/**
Builds the {type:value} trigger map (device/triggers.toit form) from repeatable
  --trigger $flags (each "boot", "interval=<s>", "install=<n>",
  "gpio-high=<pin>", "gpio-low=<pin>", or "gpio-touch=<pin>") plus the optional
  --interval shorthand $interval-s (seconds, or null).

Throws on an unknown trigger type or a non-integer value.
*/
triggers-from-flags flags/List --interval-s/int? -> Map:
  m := {:}
  if interval-s != null: m["interval"] = interval-s
  flags.do: | spec/string |
    eq := spec.index-of "="
    if eq < 0:
      if spec == "boot": m["boot"] = 1
      else: throw "unknown trigger: $spec"
    else:
      type := spec[..eq]
      value := int.parse spec[eq + 1..] --on-error=: throw "invalid trigger value: $spec"
      if type == "interval": m["interval"] = value
      else if type == "install": m["install"] = value
      else if type == "gpio-high" or type == "gpio-low" or type == "gpio-touch":
        m["$type:$value"] = value
      else: throw "unknown trigger: $type"
  return m

/**
Folds an ordered list of $commands into the goal-app map a node would converge
  to: app name → {"crc", "triggers", "runlevel", "arguments"}.

A run sets (or replaces) its app; a stop removes it; set-poll-interval does not
  affect the app set. Because commands are absolute, re-applying a run is a no-op
  and a later run for the same name wins — this function makes that idempotency
  testable on host and is reused by the device-side apply in B2.
*/
project commands/List -> Map:
  goal := {:}
  commands.do: | c/Command |
    if c.verb == VERB-RUN:
      goal[c.name] = {
        "crc": c.crc,
        "triggers": c.triggers,
        "runlevel": c.runlevel,
        "arguments": c.arguments,
      }
    else if c.verb == VERB-STOP:
      goal.remove c.name
  return goal
```

- [ ] **Step 4: Run test to verify it passes**

Run: `$TS command_test.toit`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd ~/workspaceToit/porta
git add gateway/command.toit gateway/command_test.toit
git commit -m "feat(gateway): trigger-flag parsing + idempotent goal projection

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: Deterministic node auto-naming

**Files:**
- Create: `gateway/names.toit`
- Test: `gateway/names_test.toit`

- [ ] **Step 1: Write the failing test**

`gateway/names_test.toit`:

```toit
import expect show *
import .names show node-name-for

main:
  mac := "a0b1c2d3e4f5"
  // Deterministic: same MAC → same name.
  expect-equals (node-name-for mac) (node-name-for mac)
  // Shape: "adjective-noun".
  name := node-name-for mac
  expect (name.contains "-")
  // Different MACs usually differ (these two are chosen to differ).
  expect-not-equals (node-name-for "000000000001") (node-name-for "ffffffffffff")
```

- [ ] **Step 2: Run test to verify it fails**

Run: `$TS names_test.toit`
Expected: FAIL — `node-name-for` not defined.

- [ ] **Step 3: Write minimal implementation**

`gateway/names.toit`:

```toit
// gateway/names.toit — deterministic jag-style auto-names keyed by MAC.

ADJECTIVES_ ::= [
  "amber", "brave", "calm", "clever", "eager", "fancy", "gentle", "happy",
  "jolly", "keen", "lively", "merry", "noble", "proud", "quiet", "rapid",
  "shiny", "swift", "tidy", "witty",
]
NOUNS_ ::= [
  "antler", "badger", "cedar", "comet", "dune", "ember", "falcon", "grove",
  "harbor", "ibex", "jaguar", "kestrel", "lynx", "maple", "nimbus", "otter",
  "pine", "quartz", "raven", "summit",
]

/**
Returns a stable, friendly "adjective-noun" name for the node identified by $mac
  (lowercase hex).

The mapping is deterministic, so the same MAC always yields the same name across
  runs and processes. Collisions are accepted — the gateway is small and the
  operator can override the name via `device name`.
*/
node-name-for mac/string -> string:
  h := 0
  mac.do --runes: | c | h = (h * 31 + c) & 0x7fff_ffff
  adjective := ADJECTIVES_[h % ADJECTIVES_.size]
  noun := NOUNS_[(h / ADJECTIVES_.size) % NOUNS_.size]
  return "$adjective-$noun"
```

- [ ] **Step 4: Run test to verify it passes**

Run: `$TS names_test.toit`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd ~/workspaceToit/porta
git add gateway/names.toit gateway/names_test.toit
git commit -m "feat(gateway): deterministic jag-style node auto-naming

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 7: Store — open + schema

**Files:**
- Create: `gateway/store.toit`
- Test: `gateway/store_test.toit`

- [ ] **Step 1: Write the failing test**

`gateway/store_test.toit`:

```toit
import expect show *
import .store show Store

main:
  store := Store.open ":memory:"
  // The four M1 tables exist after open.
  expect (store.has-table_ "nodes")
  expect (store.has-table_ "payloads")
  expect (store.has-table_ "command_queue")
  expect (store.has-table_ "reports")
  store.close
```

- [ ] **Step 2: Run test to verify it fails**

Run: `$TS store_test.toit`
Expected: FAIL — `Store` not defined.

- [ ] **Step 3: Write minimal implementation**

`gateway/store.toit`:

```toit
// gateway/store.toit — the gateway's sqlite store: nodes, payloads,
// command_queue, reports. Shared (in B2) by the daemon and the CLI.
import sqlite
import encoding.json

DEFAULT-POLL-INTERVAL-S ::= 30
DEFAULT-MAX-OFFLINE-S ::= 300

/** The gateway's sqlite-backed store. */
class Store:
  db_/sqlite.Database

  /** Opens (creating if absent) the database at $path and ensures the schema. */
  constructor.open path/string:
    db_ = sqlite.open path
    db_.execute "PRAGMA journal_mode = WAL"
    init-schema_

  /** Closes the underlying database. */
  close -> none:
    db_.close

  init-schema_ -> none:
    db_.execute """
        CREATE TABLE IF NOT EXISTS nodes (
          id TEXT PRIMARY KEY,
          name TEXT,
          source_addr TEXT,
          first_seen INTEGER,
          last_seen INTEGER,
          poll_interval_s INTEGER,
          max_offline_s INTEGER,
          last_report_at INTEGER,
          observed_state TEXT)"""
    db_.execute """
        CREATE TABLE IF NOT EXISTS payloads (
          crc INTEGER PRIMARY KEY,
          name TEXT,
          size INTEGER,
          image BLOB)"""
    db_.execute """
        CREATE TABLE IF NOT EXISTS command_queue (
          id INTEGER PRIMARY KEY AUTOINCREMENT,
          device_id TEXT,
          seq INTEGER,
          verb TEXT,
          args TEXT,
          issued_at INTEGER,
          issued_by TEXT,
          delivered_at INTEGER)"""
    db_.execute """
        CREATE TABLE IF NOT EXISTS reports (
          id INTEGER PRIMARY KEY AUTOINCREMENT,
          device_id TEXT,
          ts INTEGER,
          observed_state TEXT,
          health TEXT)"""

  /** Whether a table named $name exists. Test/diagnostic helper. */
  has-table_ name/string -> bool:
    return (db_.query-one "SELECT 1 FROM sqlite_master WHERE type='table' AND name=?" [name]) != null

// Stores $obj as a JSON string (sqlite TEXT). json.encode yields a ByteArray,
// which a TEXT column would store as a BLOB; .to-string keeps it textual.
encode-json_ obj -> string:
  return (json.encode obj).to-string

// Decodes a JSON string $s (as read back from a TEXT column) to a Toit value.
decode-json_ s/string -> any:
  return json.decode s.to-byte-array
```

- [ ] **Step 4: Run test to verify it passes**

Run: `$TS store_test.toit`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd ~/workspaceToit/porta
git add gateway/store.toit gateway/store_test.toit
git commit -m "feat(gateway): Store open + sqlite schema (nodes/payloads/queue/reports)

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 8: Store — nodes

**Files:**
- Modify: `gateway/store.toit` (add `import .names`, add node methods)
- Modify: `gateway/store_test.toit` (append assertions)

- [ ] **Step 1: Add the failing assertions**

Append to `gateway/store_test.toit`'s `main:` (before `store.close`):

```toit
  // touch-node creates a row on first contact (auto-named, default windows).
  store.touch-node "aabbccddeeff" --source-addr="10.0.0.5:6969" --now=1000
  n := store.node "aabbccddeeff"
  expect-equals "aabbccddeeff" n["id"]
  expect (n["name"].contains "-")                 // auto-named
  expect-equals 1000 n["first_seen"]
  expect-equals 1000 n["last_seen"]
  expect-equals DEFAULT-POLL-INTERVAL-S n["poll_interval_s"]
  expect-equals DEFAULT-MAX-OFFLINE-S n["max_offline_s"]

  // A second contact updates last_seen, not first_seen.
  store.touch-node "aabbccddeeff" --now=2000
  expect-equals 1000 (store.node "aabbccddeeff")["first_seen"]
  expect-equals 2000 (store.node "aabbccddeeff")["last_seen"]

  // ensure-node creates a never-contacted row (last_seen stays null).
  store.ensure-node "010203040506" --now=1500
  expect-equals null (store.node "010203040506")["last_seen"]

  // lookups, listing, and setters.
  expect-equals null (store.node "ffffffffffff")              // unknown → null
  expect-equals "aabbccddeeff" (store.node-by-name (store.node "aabbccddeeff")["name"])["id"]
  expect-equals 2 store.nodes.size
  store.set-node-name "aabbccddeeff" "kitchen"
  expect-equals "kitchen" (store.node "aabbccddeeff")["name"]
  store.set-max-offline "aabbccddeeff" 60
  expect-equals 60 (store.node "aabbccddeeff")["max_offline_s"]
  store.set-poll-interval-intended "aabbccddeeff" 1
  expect-equals 1 (store.node "aabbccddeeff")["poll_interval_s"]
```

Also add to the imports at the top of `store_test.toit`:

```toit
import .store show Store DEFAULT-POLL-INTERVAL-S DEFAULT-MAX-OFFLINE-S
```

(Replace the existing `import .store show Store` line.)

- [ ] **Step 2: Run test to verify it fails**

Run: `$TS store_test.toit`
Expected: FAIL — `touch-node` not defined.

- [ ] **Step 3: Implement**

Add to the top of `gateway/store.toit` (after `import encoding.json`):

```toit
import .names show node-name-for
```

Add these methods to the `Store` class:

```toit
  /**
  Records contact from node $id (a MAC) at $now (epoch seconds).

  Inserts a new, auto-named row with default poll/offline windows on first
    contact; on later contacts updates last_seen and (if given) $source-addr.
  */
  touch-node id/string --source-addr/string?=null --now/int -> none:
    if (node id) == null:
      db_.execute "INSERT INTO nodes (id, name, source_addr, first_seen, last_seen, poll_interval_s, max_offline_s) VALUES (?, ?, ?, ?, ?, ?, ?)"
          [id, (node-name-for id), source-addr, now, now, DEFAULT-POLL-INTERVAL-S, DEFAULT-MAX-OFFLINE-S]
    else:
      db_.execute "UPDATE nodes SET last_seen = ?, source_addr = COALESCE(?, source_addr) WHERE id = ?"
          [now, source-addr, id]

  /**
  Ensures a row exists for node $id without recording contact (last_seen stays
    null), so an operator can address a never-yet-seen node by its MAC. $now
    seeds first_seen for ordering.
  */
  ensure-node id/string --now/int -> none:
    if (node id) == null:
      db_.execute "INSERT INTO nodes (id, name, first_seen, poll_interval_s, max_offline_s) VALUES (?, ?, ?, ?, ?)"
          [id, (node-name-for id), now, DEFAULT-POLL-INTERVAL-S, DEFAULT-MAX-OFFLINE-S]

  /** Returns the node row for $id as a map, or null if unknown. */
  node id/string -> Map?:
    return node-row_ (db_.query-one "$NODE-SELECT_ WHERE id = ?" [id])

  /** Returns the node row whose name is $name, or null. */
  node-by-name name/string -> Map?:
    return node-row_ (db_.query-one "$NODE-SELECT_ WHERE name = ?" [name])

  /** Returns all node rows, ordered by name. */
  nodes -> List:
    result := []
    db_.query "$NODE-SELECT_ ORDER BY name": | row | result.add (node-row_ row)
    return result

  /** Overrides node $id's friendly name. */
  set-node-name id/string name/string -> none:
    db_.execute "UPDATE nodes SET name = ? WHERE id = ?" [name, id]

  /** Sets node $id's offline threshold to $seconds (a gateway-side config row). */
  set-max-offline id/string seconds/int -> none:
    db_.execute "UPDATE nodes SET max_offline_s = ? WHERE id = ?" [seconds, id]

  /** Records the intended poll cadence ($seconds) for display; the authoritative change is the enqueued command. */
  set-poll-interval-intended id/string seconds/int -> none:
    db_.execute "UPDATE nodes SET poll_interval_s = ? WHERE id = ?" [seconds, id]

  node-row_ row/List? -> Map?:
    if row == null: return null
    return {
      "id": row[0], "name": row[1], "source_addr": row[2],
      "first_seen": row[3], "last_seen": row[4],
      "poll_interval_s": row[5], "max_offline_s": row[6],
      "last_report_at": row[7], "observed_state": row[8],
    }
```

Add this constant near the top of `store.toit` (after the `DEFAULT-*` constants):

```toit
NODE-SELECT_ ::= "SELECT id, name, source_addr, first_seen, last_seen, poll_interval_s, max_offline_s, last_report_at, observed_state FROM nodes"
```

- [ ] **Step 4: Run test to verify it passes**

Run: `$TS store_test.toit`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd ~/workspaceToit/porta
git add gateway/store.toit gateway/store_test.toit
git commit -m "feat(gateway): Store node upsert/lookup/list/setters

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 9: Store — payloads (BLOBs)

**Files:**
- Modify: `gateway/store.toit` (add payload methods)
- Modify: `gateway/store_test.toit` (append assertions)

- [ ] **Step 1: Add the failing assertions**

Append to `gateway/store_test.toit`'s `main:` (before `store.close`):

```toit
  image := #[0xca, 0xfe, 0xba, 0xbe, 0x00, 0x01]
  expect-not (store.payload-exists 12345)
  store.register-payload --crc=12345 --name="blink" --image=image
  expect (store.payload-exists 12345)
  p := store.payload 12345
  expect-equals "blink" p["name"]
  expect-equals 6 p["size"]
  expect-equals image p["image"]                 // BLOB round-trips byte-identical
  // Re-register the same crc replaces (idempotent registration).
  store.register-payload --crc=12345 --name="blink" --image=#[0x01]
  expect-equals 1 (store.payload 12345)["size"]
  expect-equals null (store.payload 999)         // unknown crc → null
```

- [ ] **Step 2: Run test to verify it fails**

Run: `$TS store_test.toit`
Expected: FAIL — `register-payload` not defined.

- [ ] **Step 3: Implement**

Add to the `Store` class:

```toit
  /** Stores image $image (keyed by $crc, labelled $name); replaces any existing row for $crc. */
  register-payload --crc/int --name/string --image/ByteArray -> none:
    db_.execute "INSERT OR REPLACE INTO payloads (crc, name, size, image) VALUES (?, ?, ?, ?)"
        [crc, name, image.size, image]

  /** Whether a payload with $crc is stored. */
  payload-exists crc/int -> bool:
    return (db_.query-one "SELECT 1 FROM payloads WHERE crc = ?" [crc]) != null

  /** Returns the payload for $crc as {"crc","name","size","image"}, or null. */
  payload crc/int -> Map?:
    row := db_.query-one "SELECT crc, name, size, image FROM payloads WHERE crc = ?" [crc]
    if row == null: return null
    return {"crc": row[0], "name": row[1], "size": row[2], "image": row[3]}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `$TS store_test.toit`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd ~/workspaceToit/porta
git add gateway/store.toit gateway/store_test.toit
git commit -m "feat(gateway): Store payload BLOB register/exists/get

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 10: Store — command queue

**Files:**
- Modify: `gateway/store.toit` (add `import .command`, add queue methods)
- Modify: `gateway/store_test.toit` (append assertions)

- [ ] **Step 1: Add the failing assertions**

Append to `gateway/store_test.toit`'s `main:` (before `store.close`):

```toit
  dev := "aabbccddeeff"
  id1 := store.enqueue-command dev (Command.run --name="blink" --crc=7 --triggers={"interval": 30}) --issued-by="cli" --now=3000
  id2 := store.enqueue-command dev (Command.stop --name="old") --issued-by="cli" --now=3001
  expect (id2 > id1)

  // FIFO: next-undelivered returns the oldest; mark-delivered advances it.
  first := store.next-undelivered dev
  expect-equals id1 first["id"]
  expect-equals "run" first["verb"]
  expect-equals 7 first["args"]["crc"]            // args decoded to a map
  expect-equals 2 (store.undelivered-commands dev).size
  store.mark-delivered id1 --now=3100
  expect-equals id2 (store.next-undelivered dev)["id"]
  expect-equals 1 (store.undelivered-commands dev).size

  // The log is the full audit history with delivery stamps.
  log := store.command-log dev
  expect-equals 2 log.size
  expect-equals 3100 log[0]["delivered_at"]
  expect-equals null log[1]["delivered_at"]

  // Queues are per device.
  expect-equals 0 (store.undelivered-commands "010203040506").size
```

Add `Command` to the test imports (top of `store_test.toit`):

```toit
import .command show Command
```

- [ ] **Step 2: Run test to verify it fails**

Run: `$TS store_test.toit`
Expected: FAIL — `enqueue-command` not defined.

- [ ] **Step 3: Implement**

Add to the top of `gateway/store.toit` (after `import .names ...`):

```toit
import .command show Command
```

Add to the `Store` class:

```toit
  /**
  Appends $command to node $device-id's FIFO queue, recording $issued-by and
    $now (epoch seconds). Returns the new command id.
  */
  enqueue-command device-id/string command/Command --issued-by/string --now/int -> int:
    db_.execute "INSERT INTO command_queue (device_id, seq, verb, args, issued_at, issued_by, delivered_at) VALUES (?, ?, ?, ?, ?, ?, NULL)"
        [device-id, (next-seq_ device-id), command.verb, (encode-json_ command.args), now, issued-by]
    return db_.last-insert-rowid

  next-seq_ device-id/string -> int:
    return (db_.query-one "SELECT COALESCE(MAX(seq), 0) + 1 FROM command_queue WHERE device_id = ?" [device-id])[0]

  /** Returns $device-id's undelivered commands, oldest first. */
  undelivered-commands device-id/string -> List:
    result := []
    db_.query "SELECT id, verb, args, issued_at, issued_by FROM command_queue WHERE device_id = ? AND delivered_at IS NULL ORDER BY id" [device-id]: | row |
      result.add (command-row_ row)
    return result

  /** Returns the oldest undelivered command for $device-id, or null. */
  next-undelivered device-id/string -> Map?:
    row := db_.query-one "SELECT id, verb, args, issued_at, issued_by FROM command_queue WHERE device_id = ? AND delivered_at IS NULL ORDER BY id LIMIT 1" [device-id]
    if row == null: return null
    return command-row_ row

  /** Marks command $id delivered at $now (epoch seconds). */
  mark-delivered id/int --now/int -> none:
    db_.execute "UPDATE command_queue SET delivered_at = ? WHERE id = ?" [now, id]

  /** Returns the full command history for $device-id, oldest first (the audit log). */
  command-log device-id/string -> List:
    result := []
    db_.query "SELECT id, verb, args, issued_at, issued_by, delivered_at FROM command_queue WHERE device_id = ? ORDER BY id" [device-id]: | row |
      result.add {
        "id": row[0], "verb": row[1], "args": (decode-json_ row[2]),
        "issued_at": row[3], "issued_by": row[4], "delivered_at": row[5],
      }
    return result

  command-row_ row/List -> Map:
    return {
      "id": row[0], "verb": row[1], "args": (decode-json_ row[2]),
      "issued_at": row[3], "issued_by": row[4],
    }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `$TS store_test.toit`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd ~/workspaceToit/porta
git add gateway/store.toit gateway/store_test.toit
git commit -m "feat(gateway): Store per-node FIFO command queue + audit log

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 11: Store — reports

**Files:**
- Modify: `gateway/store.toit` (add report methods)
- Modify: `gateway/store_test.toit` (append assertions)

- [ ] **Step 1: Add the failing assertions**

Append to `gateway/store_test.toit`'s `main:` (before `store.close`):

```toit
  observed := encode-json_ {"apps": {"blink": {"crc": 7, "runlevel": 3}}}
  health := encode-json_ {"uptime_s": 12, "free_heap": 50000, "wakes": 3}
  store.insert-report "aabbccddeeff" --observed-state=observed --health=health --now=4000
  // The latest observed state + report time are cached on the node row.
  refreshed := store.node "aabbccddeeff"
  expect-equals 4000 refreshed["last_report_at"]
  decoded := decode-json_ refreshed["observed_state"]
  expect-equals 7 decoded["apps"]["blink"]["crc"]
  // The append-only reports table holds the history (newest first).
  reports := store.reports "aabbccddeeff"
  expect-equals 1 reports.size
  expect-equals 4000 reports[0]["ts"]
```

The test already imports `encode-json_`/`decode-json_`? They are private (`_` suffix) top-level functions in `store.toit`. Re-export them to the test by adding to the test's store import:

```toit
import .store show Store DEFAULT-POLL-INTERVAL-S DEFAULT-MAX-OFFLINE-S encode-json_ decode-json_
```

(Replace the existing `import .store ...` line. Private `_` names are importable when named explicitly in a `show` list within the same package.)

- [ ] **Step 2: Run test to verify it fails**

Run: `$TS store_test.toit`
Expected: FAIL — `insert-report` not defined.

- [ ] **Step 3: Implement**

Add to the `Store` class:

```toit
  /**
  Records a node's observed state from a wake.

  Appends a row to the reports audit table (with $observed-state and $health,
    each a JSON string, at $now epoch seconds) and refreshes the cached
    observed_state / last_report_at on the $device-id node row.
  */
  insert-report device-id/string --observed-state/string --health/string --now/int -> none:
    db_.execute "INSERT INTO reports (device_id, ts, observed_state, health) VALUES (?, ?, ?, ?)"
        [device-id, now, observed-state, health]
    db_.execute "UPDATE nodes SET observed_state = ?, last_report_at = ? WHERE id = ?"
        [observed-state, now, device-id]

  /** Returns $device-id's reports, newest first. */
  reports device-id/string -> List:
    result := []
    db_.query "SELECT ts, observed_state, health FROM reports WHERE device_id = ? ORDER BY ts DESC" [device-id]: | row |
      result.add {"ts": row[0], "observed_state": row[1], "health": row[2]}
    return result
```

- [ ] **Step 4: Run test to verify it passes**

Run: `$TS store_test.toit`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd ~/workspaceToit/porta
git add gateway/store.toit gateway/store_test.toit
git commit -m "feat(gateway): Store report ingest + cached observed-state

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 12: CLI scaffold — node resolution, `scan`, `ping`

**Files:**
- Create: `gateway/gateway.toit`

This task has no unit test — the CLI is exercised manually here and end-to-end in Task 16. Each later CLI task extends this file.

- [ ] **Step 1: Write the CLI skeleton with `scan` and `ping`**

`gateway/gateway.toit`:

```toit
// gateway/gateway.toit — the Porta gateway CLI (pkg-cli). Every leaf command
// opens the sqlite Store and reads/writes it. The TFTP daemon (`serve`) and the
// store-backed request handler are B2 and are intentionally not here yet.
import cli
import host.file
import .store show Store DEFAULT-MAX-OFFLINE-S
import .command show *
import .crc32 show crc32
import .duration show parse-duration-s

main args:
  (build-command).run args

/** Builds the full `gateway` command tree. */
build-command -> cli.Command:
  scan-cmd := cli.Command "scan"
      --help="List known nodes and their online/offline health."
      --options=[ cli.Flag "include-never-seen" --help="Also list nodes that have never polled." ]
      --run=:: cmd-scan it

  ping-cmd := cli.Command "ping"
      --help="Report whether a node has been seen within its max-offline window."
      --options=[ cli.Option "device" --short-name="d" --help="Node name or MAC." --required ]
      --run=:: cmd-ping it

  return cli.Command "gateway"
      --help="Porta LAN gateway — command-queue control plane for Toit nodes."
      --options=[ cli.Option "db" --help="Path to the sqlite store." --default="porta.db" ]
      --subcommands=[ scan-cmd, ping-cmd ]

// --- shared helpers ----------------------------------------------------------

now_ -> int: return Time.now.s-since-epoch

open-store_ parsed/cli.Parsed -> Store: return Store.open parsed["db"]

/** Whether $s is a 12-hex-digit base MAC. */
is-mac_ s/string -> bool:
  if s.size != 12: return false
  s.do --runes: | c |
    if not ('0' <= c <= '9' or 'a' <= c <= 'f'): return false
  return true

/**
Resolves a node name-or-MAC $key to a node id, or aborts the process.

Matches an existing id, then an existing name; failing both, accepts a literal
  12-hex-digit MAC (so a node can be addressed before its first poll).
*/
resolve-node-id_ store/Store key/string -> string:
  if (store.node key) != null: return key
  by-name := store.node-by-name key
  if by-name != null: return by-name["id"]
  if is-mac_ key: return key
  print "Error: unknown node '$key' (not a known name and not a 12-hex-digit MAC)."
  exit 1
  unreachable

/** Whether $node (a node row map, possibly null) is within its max-offline window at $now. */
online_ node/Map? --now/int -> bool:
  if node == null or node["last_seen"] == null: return false
  return (now - node["last_seen"]) <= node["max_offline_s"]

// --- commands ----------------------------------------------------------------

cmd-scan parsed/cli.Parsed -> none:
  store := open-store_ parsed
  include-never := parsed["include-never-seen"]
  now := now_
  print "DEVICE        NAME             LAST-SEEN  STATUS"
  store.nodes.do: | node/Map |
    if node["last_seen"] == null and not include-never: continue.do
    status := node["last_seen"] == null ? "never-seen" : (online_ node --now=now ? "online" : "offline")
    last := node["last_seen"] == null ? "-" : "$(now - node["last_seen"])s ago"
    print "$(node["id"])  $(pad_ node["name"] 15)  $(pad_ last 9)  $status"
  store.close

cmd-ping parsed/cli.Parsed -> none:
  store := open-store_ parsed
  id := resolve-node-id_ store parsed["device"]
  node := store.node id
  now := now_
  if node == null or node["last_seen"] == null:
    print "$id: never seen"
  else if online_ node --now=now:
    print "$id: online (last seen $(now - node["last_seen"])s ago)"
  else:
    print "$id: offline (last seen $(now - node["last_seen"])s ago)"
  store.close

/** Right-pads $s with spaces to at least $width characters. */
pad_ s/string width/int -> string:
  if s.size >= width: return s
  return s + (" " * (width - s.size))
```

- [ ] **Step 2: Manually verify against a scratch store**

Run:

```bash
rm -f /tmp/b1.db
$TS gateway.toit --db /tmp/b1.db scan
```

Expected: prints the header row and nothing else (empty store), exits 0.

- [ ] **Step 3: Verify `ping` aborts cleanly on an unknown node**

Run: `$TS gateway.toit --db /tmp/b1.db ping -d nope`
Expected: prints `Error: unknown node 'nope' ...` and exits non-zero.

- [ ] **Step 4: Commit**

```bash
cd ~/workspaceToit/porta
git add gateway/gateway.toit
git commit -m "feat(gateway): CLI scaffold + node resolution + scan/ping

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 13: CLI — `device` subcommands

**Files:**
- Modify: `gateway/gateway.toit`

- [ ] **Step 1: Add the `device` command group**

In `build-command`, before the `return cli.Command "gateway" ...`, add:

```toit
  device-show-cmd := cli.Command "show"
      --help="Show a node's last contact, observed state, and queued commands."
      --options=[ cli.Option "device" --short-name="d" --help="Node name or MAC." --required ]
      --run=:: cmd-device-show it

  device-set-max-offline-cmd := cli.Command "set-max-offline"
      --help="Set the offline threshold used to judge a node's health."
      --options=[ cli.Option "device" --short-name="d" --help="Node name or MAC." --required ]
      --rest=[ cli.Option "duration" --help="e.g. 30s, 5m, 1h." --required ]
      --run=:: cmd-device-set-max-offline it

  device-set-poll-interval-cmd := cli.Command "set-poll-interval"
      --help="Enqueue a change to the node's wake/poll cadence."
      --options=[ cli.Option "device" --short-name="d" --help="Node name or MAC." --required ]
      --rest=[ cli.Option "duration" --help="e.g. 1s, 30s, 5m." --required ]
      --run=:: cmd-device-set-poll-interval it

  device-name-cmd := cli.Command "name"
      --help="Override a node's auto-assigned friendly name."
      --options=[ cli.Option "device" --short-name="d" --help="Node name or MAC." --required ]
      --rest=[ cli.Option "new-name" --help="The new name." --required ]
      --run=:: cmd-device-name it

  device-cmd := cli.Command "device"
      --help="Inspect and configure a node."
      --subcommands=[
        device-show-cmd, device-set-max-offline-cmd,
        device-set-poll-interval-cmd, device-name-cmd,
      ]
```

Then add `device-cmd` to the root command's `--subcommands` list:

```toit
      --subcommands=[ scan-cmd, ping-cmd, device-cmd ]
```

- [ ] **Step 2: Implement the command bodies**

Append to `gateway/gateway.toit`:

```toit
cmd-device-show parsed/cli.Parsed -> none:
  store := open-store_ parsed
  id := resolve-node-id_ store parsed["device"]
  node := store.node id
  if node == null:
    print "$id: no row yet (never seen, never configured)"
    store.close
    return
  now := now_
  print "id:            $(node["id"])"
  print "name:          $(node["name"])"
  print "last-seen:     $(node["last_seen"] == null ? "never" : "$(now - node["last_seen"])s ago")"
  print "poll-interval: $(node["poll_interval_s"])s"
  print "max-offline:   $(node["max_offline_s"])s"
  print "observed:      $(node["observed_state"] == null ? "(no report yet)" : node["observed_state"])"
  undelivered := store.undelivered-commands id
  print "queued (undelivered): $undelivered.size"
  undelivered.do: | c/Map | print "  #$(c["id"]) $(c["verb"]) $(c["args"])"
  store.close

cmd-device-set-max-offline parsed/cli.Parsed -> none:
  store := open-store_ parsed
  id := resolve-node-id_ store parsed["device"]
  store.ensure-node id --now=now_
  seconds := parse-duration-s parsed["duration"]
  store.set-max-offline id seconds
  print "$id: max-offline = $(seconds)s"
  store.close

cmd-device-set-poll-interval parsed/cli.Parsed -> none:
  store := open-store_ parsed
  id := resolve-node-id_ store parsed["device"]
  store.ensure-node id --now=now_
  seconds := parse-duration-s parsed["duration"]
  cmd-id := store.enqueue-command id (Command.set-poll-interval --interval-s=seconds) --issued-by="cli" --now=now_
  store.set-poll-interval-intended id seconds
  print "$id: enqueued set-poll-interval $(seconds)s (command #$cmd-id)"
  store.close

cmd-device-name parsed/cli.Parsed -> none:
  store := open-store_ parsed
  id := resolve-node-id_ store parsed["device"]
  store.ensure-node id --now=now_
  store.set-node-name id parsed["new-name"]
  print "$id: name = $(parsed["new-name"])"
  store.close
```

- [ ] **Step 3: Manually verify**

Run:

```bash
rm -f /tmp/b1.db
$TS gateway.toit --db /tmp/b1.db device set-poll-interval -d aabbccddeeff 1s
$TS gateway.toit --db /tmp/b1.db device name -d aabbccddeeff kitchen
$TS gateway.toit --db /tmp/b1.db device show -d kitchen
```

Expected: the first prints `... enqueued set-poll-interval 1s (command #1)`; the last prints the node detail with `name: kitchen`, `poll-interval: 1s`, and one queued (undelivered) command.

- [ ] **Step 4: Commit**

```bash
cd ~/workspaceToit/porta
git add gateway/gateway.toit
git commit -m "feat(gateway): CLI device show/set-max-offline/set-poll-interval/name

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 14: CLI — `container` subcommands

**Files:**
- Modify: `gateway/gateway.toit`

- [ ] **Step 1: Add the `container` command group**

In `build-command`, before the `return cli.Command "gateway" ...`, add:

```toit
  container-install-cmd := cli.Command "install"
      --help="""
        Register a container image for a node and enqueue a run command.

        <file> dispatches by extension: .bin is a prebuilt image (M1); .pod and
        .toit are accepted but not yet supported (scheduled for M3 / M4).
        """
      --options=[
        cli.Option "device" --short-name="d" --help="Node name or MAC." --required,
        cli.OptionInt "crc" --help="Image CRC32 (computed from the file if omitted).",
        cli.Option "interval" --help="Run-interval shorthand, e.g. 30s (a --trigger interval=…).",
        cli.Option "trigger" --multi --help="Repeatable trigger: boot | interval=<s> | gpio-high=<pin> | gpio-low=<pin> | gpio-touch=<pin>.",
        cli.OptionInt "runlevel" --help="Container runlevel." --default=3,
      ]
      --rest=[
        cli.Option "name" --help="App name on the node." --required,
        cli.Option "file" --help="Image file (.bin | .pod | .toit)." --required,
      ]
      --examples=[
        cli.Example "Install blink to run every 30 s on a node addressed by MAC:"
            --arguments="--device=aabbccddeeff --interval=30s blink ./blink.bin"
            --global-priority=5,
      ]
      --run=:: cmd-container-install it

  container-uninstall-cmd := cli.Command "uninstall"
      --help="Enqueue a stop command so the node no longer runs an app."
      --options=[ cli.Option "device" --short-name="d" --help="Node name or MAC." --required ]
      --rest=[ cli.Option "name" --help="App name to stop." --required ]
      --run=:: cmd-container-uninstall it

  container-list-cmd := cli.Command "list"
      --help="List a node's apps from its latest report."
      --options=[ cli.Option "device" --short-name="d" --help="Node name or MAC." --required ]
      --run=:: cmd-container-list it

  container-cmd := cli.Command "container"
      --help="Install, remove, and list a node's containers."
      --subcommands=[ container-install-cmd, container-uninstall-cmd, container-list-cmd ]
```

Then add `container-cmd` to the root command's `--subcommands` list:

```toit
      --subcommands=[ scan-cmd, ping-cmd, device-cmd, container-cmd ]
```

- [ ] **Step 2: Implement the command bodies**

Append to `gateway/gateway.toit`. (Note `import .store` already imports `encode-json_`? No — extend the store import.) First, replace the store import line at the top with:

```toit
import .store show Store DEFAULT-MAX-OFFLINE-S decode-json_
```

Then append:

```toit
cmd-container-install parsed/cli.Parsed -> none:
  store := open-store_ parsed
  id := resolve-node-id_ store parsed["device"]
  store.ensure-node id --now=now_
  name := parsed["name"]
  path := parsed["file"]

  if path.ends-with ".pod":
    print "Error: .pod ingestion is not yet supported (scheduled for M3)."
    exit 1
  if path.ends-with ".toit":
    print "Error: .toit source-compile is not yet supported (scheduled for M4)."
    exit 1
  if not path.ends-with ".bin":
    print "Error: unsupported file type for '$path' (expected .bin, .pod, or .toit)."
    exit 1

  image := file.read-contents path
  crc := parsed["crc"] != null ? parsed["crc"] : (crc32 image)
  triggers := triggers-from-flags parsed["trigger"]
      --interval-s=(parsed["interval"] != null ? (parse-duration-s parsed["interval"]) : null)
  if triggers.is-empty:
    print "Note: no triggers given — '$name' will be installed but not started until a trigger is added."

  store.register-payload --crc=crc --name=name --image=image
  cmd-id := store.enqueue-command id
      (Command.run --name=name --crc=crc --triggers=triggers --runlevel=parsed["runlevel"])
      --issued-by="cli" --now=now_
  print "$id: registered $name@$crc ($(image.size) B); enqueued run (command #$cmd-id)"
  store.close

cmd-container-uninstall parsed/cli.Parsed -> none:
  store := open-store_ parsed
  id := resolve-node-id_ store parsed["device"]
  store.ensure-node id --now=now_
  name := parsed["name"]
  cmd-id := store.enqueue-command id (Command.stop --name=name) --issued-by="cli" --now=now_
  print "$id: enqueued stop $name (command #$cmd-id)"
  store.close

cmd-container-list parsed/cli.Parsed -> none:
  store := open-store_ parsed
  id := resolve-node-id_ store parsed["device"]
  node := store.node id
  if node == null or node["observed_state"] == null:
    print "$id: no report yet"
    store.close
    return
  observed := decode-json_ node["observed_state"]
  apps := observed.get "apps" --if-absent=: {:}
  print "DEVICE        IMAGE       NAME"
  apps.do: | name/string spec/Map |
    print "$(node["id"])  $(pad_ "$(spec.get "crc")" 10)  $name"
  store.close
```

- [ ] **Step 3: Manually verify install + uninstall**

Run:

```bash
rm -f /tmp/b1.db
head -c 64 /dev/urandom > /tmp/blink.bin
$TS gateway.toit --db /tmp/b1.db container install -d aabbccddeeff --interval=30s blink /tmp/blink.bin
$TS gateway.toit --db /tmp/b1.db device show -d aabbccddeeff
$TS gateway.toit --db /tmp/b1.db container uninstall -d aabbccddeeff blink
```

Expected: install prints `registered blink@<crc> (64 B); enqueued run (command #1)`; `device show` lists the queued run command with the `interval` trigger; uninstall enqueues a stop as command #2. Also confirm the `.pod` stub:

```bash
$TS gateway.toit --db /tmp/b1.db container install -d aabbccddeeff x /tmp/x.pod
```

Expected: prints the "scheduled for M3" message and exits non-zero.

- [ ] **Step 4: Commit**

```bash
cd ~/workspaceToit/porta
git add gateway/gateway.toit
git commit -m "feat(gateway): CLI container install/uninstall/list (.bin; .pod/.toit stubbed)

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 15: CLI — `log`

**Files:**
- Modify: `gateway/gateway.toit`

- [ ] **Step 1: Add the `log` command**

In `build-command`, add:

```toit
  log-cmd := cli.Command "log"
      --help="Show a node's command audit history (issued and delivered)."
      --options=[ cli.Option "device" --short-name="d" --help="Node name or MAC." --required ]
      --run=:: cmd-log it
```

Add `log-cmd` to the root's `--subcommands`:

```toit
      --subcommands=[ scan-cmd, ping-cmd, device-cmd, container-cmd, log-cmd ]
```

- [ ] **Step 2: Implement**

Append to `gateway/gateway.toit`:

```toit
cmd-log parsed/cli.Parsed -> none:
  store := open-store_ parsed
  id := resolve-node-id_ store parsed["device"]
  entries := store.command-log id
  if entries.is-empty:
    print "$id: no commands"
    store.close
    return
  print "ID   VERB              DELIVERED  ARGS"
  entries.do: | e/Map |
    delivered := e["delivered_at"] == null ? "pending" : "yes"
    print "$(pad_ "#$(e["id"])" 4) $(pad_ e["verb"] 17) $(pad_ delivered 10) $(e["args"])"
  store.close
```

- [ ] **Step 3: Manually verify**

Run (reusing `/tmp/b1.db` from Task 14):

```bash
$TS gateway.toit --db /tmp/b1.db log -d aabbccddeeff
```

Expected: a table listing the run (#1) and stop (#2) commands, both `DELIVERED = pending` (nothing marks delivery until B2's handler).

- [ ] **Step 4: Commit**

```bash
cd ~/workspaceToit/porta
git add gateway/gateway.toit
git commit -m "feat(gateway): CLI log (command audit history)

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 16: Integration test, README, full suite

**Files:**
- Create: `gateway/integration_test.toit`
- Create: `gateway/README.md`

- [ ] **Step 1: Write the integration test**

`gateway/integration_test.toit` — a fake node draining a seeded queue, pulling a payload, and reporting, asserting the store reaches the expected delivered/observed truth (no wire, no device):

```toit
import expect show *
import .store show Store encode-json_ decode-json_
import .command show Command project

main:
  store := Store.open ":memory:"
  dev := "aabbccddeeff"

  // Operator seeds a programming session.
  image := #[0x01, 0x02, 0x03, 0x04]
  crc := 99
  store.register-payload --crc=crc --name="blink" --image=image
  store.enqueue-command dev (Command.run --name="blink" --crc=crc --triggers={"interval": 5}) --issued-by="cli" --now=10
  store.enqueue-command dev (Command.set-poll-interval --interval-s=1) --issued-by="cli" --now=11

  // A node wakes: record contact, drain the queue to exhaustion, applying each.
  store.touch-node dev --source-addr="10.0.0.5:6969" --now=100
  applied := []
  while true:
    next := store.next-undelivered dev
    if next == null: break
    applied.add (Command next["verb"] next["args"])
    // A run pulls the payload it lacks.
    if next["verb"] == "run":
      expect (store.payload-exists next["args"]["crc"])
    store.mark-delivered next["id"] --now=101

  // Every command was delivered exactly once, in FIFO order.
  expect-equals 2 applied.size
  expect-equals "run" applied[0].verb
  expect-equals "set-poll-interval" applied[1].verb
  expect (store.undelivered-commands dev).is-empty
  (store.command-log dev).do: | e/Map | expect-equals 101 e["delivered_at"]

  // The node reports its converged state; the gateway caches it.
  goal := project applied
  store.insert-report dev --observed-state=(encode-json_ {"apps": goal}) --health=(encode-json_ {"wakes": 1}) --now=200
  node := store.node dev
  expect-equals 200 node["last_report_at"]
  observed := decode-json_ node["observed_state"]
  expect-equals crc observed["apps"]["blink"]["crc"]

  store.close
  print "integration OK"
```

- [ ] **Step 2: Run the integration test (expect pass)**

Run: `$TS integration_test.toit`
Expected: prints `integration OK`, exits 0.

- [ ] **Step 3: Run the entire B1 test suite**

Run:

```bash
cd ~/workspaceToit/porta/gateway
for t in crc32_test duration_test command_test names_test store_test integration_test; do
  echo "RUN $t"; $TS "$t.toit" || { echo "FAIL $t"; break; }
done
echo "all tests done"
```

Expected: each `RUN <t>` is followed by no error; ends with `all tests done` and no `FAIL`.

- [ ] **Step 4: Write the README**

`gateway/README.md`:

```markdown
# gateway — Porta Toit gateway (B1: TFTP-free core)

The host side of the Porta LAN gateway: a sqlite store, the command wire-codec,
and a `jag`-aligned CLI. This is **B1** — the daemon that serves nodes over
TFTP (`serve.toit`), the store-backed request handler, and the device-side
drain/apply/report changes are **B2** (and depend on Spec A's TFTP refactor).

See `docs/specs/2026-05-23-porta-toit-gateway-design.md` (Spec B) and
`docs/plans/2026-05-23-gateway-b1-tftp-free-core.md`.

## Toolchain

Runs on the host via the prebuilt `toit-sqlite` binary (the `sqlite` package
needs its bundled SDK):

    export TS=~/workspaceToit/sqlite/build/bin/toit-sqlite
    cd gateway && $TS pkg install

## CLI

    $TS gateway.toit --db porta.db <command>

| Command | Effect |
|---|---|
| `scan [--include-never-seen]` | list nodes + online/offline health |
| `ping -d <node>` | recently-seen check |
| `device show -d <node>` | last contact, observed state, queued commands |
| `device set-max-offline -d <node> <dur>` | offline threshold (config) |
| `device set-poll-interval -d <node> <dur>` | enqueue a poll-cadence change |
| `device name -d <node> <new-name>` | override the auto-name |
| `container install <name> <file> -d <node> [--crc N] [--interval <dur>] [--trigger t=v]… [--runlevel N]` | register a `.bin` image + enqueue run |
| `container uninstall <name> -d <node>` | enqueue stop |
| `container list -d <node>` | apps from the latest report |
| `log -d <node>` | command audit history |

`<node>` is a node name or its base-MAC hex. `.pod` and `.toit` inputs are
accepted by `container install` but report "scheduled for M3 / M4".

## Tests

    cd gateway
    for t in crc32_test duration_test command_test names_test store_test integration_test; do $TS "$t.toit"; done
```

- [ ] **Step 5: Commit**

```bash
cd ~/workspaceToit/porta
git add gateway/integration_test.toit gateway/README.md
git commit -m "test(gateway): end-to-end store integration; add README

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## What B1 deliberately defers to B2

- `serve.toit` (the TFTP/UDP daemon) and `handler.toit` (`StoreBackedHandler implements Storage`, `?id=<mac>` parsing, `commands`/`payload`/`report` dispatch, `on-transfer-complete` delivery marking). These depend on **Spec A**'s seams.
- Device supervisor changes (drain/apply/report + node-local poll interval) and the hardware verification on `fwkb`.
- Retiring `host/serve.toit`.

The store methods B2's handler will call (`touch-node`, `next-undelivered`, `mark-delivered`, `payload`, `insert-report`) are all built and tested here, so B2 is pure wiring on top of a verified core.

## Self-review notes (author checklist, completed)

- **Spec coverage:** store schema (Tasks 7–11) covers all four M1 tables; command vocabulary `run`/`stop`/`set-poll-interval` (Tasks 4–5, 13–14); CLI surface rows from the spec table — `scan`, `ping`, `device show/set-max-offline/set-poll-interval/name`, `container install/uninstall/list`, `log` (Tasks 12–15); the file-type-dispatched ingestion seam with `.bin` real and `.pod`/`.toit` stubbed (Task 14); auto-naming on first contact + override (Tasks 6, 8, 13); idempotency (Task 5); BLOB payloads keyed by crc (Task 9); report ingest + cached observed-state (Task 11). `serve`/handler/device are out of scope by the agreed B1/B2 split.
- **Type consistency:** `Command verb args` primary constructor with `Command.run/stop/set-poll-interval` factories and accessor methods is used identically in `command.toit`, `store.toit` (enqueue/command-row), `gateway.toit`, and both tests. `node`/`command`/`payload`/`report` rows are always `Map` with the documented keys. `encode-json_`/`decode-json_` are the only JSON-to-sqlite path.
- **Toolchain:** every `Run:` uses `$TS` (the prebuilt `toit-sqlite`), validated against the live binary while authoring (sqlite blob round-trip, JSON-as-string rule, nested `cli.Command` parsing, global `--db` propagation, `exit`, `Time.now.s-since-epoch`, `crypto.crc` parameters).
