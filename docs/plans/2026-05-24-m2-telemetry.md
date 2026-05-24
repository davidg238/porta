# M2 Telemetry (up-path) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Before writing Toit, consult the toit-conventions, toit-services, and toit-exe skills.

**Goal:** Add a device→gateway telemetry time-series (`data_log`) carrying console lines + structured readings, drained from a device-side telemetry provider and shipped over a separate `data?id=` WRQ, with a `gateway monitor` CLI to read it back — off by default, hardware-verified on `fwkb`.

**Architecture:** A spawned **telemetry provider process** on the device (own heap, crash-isolated, services only — no socket) buffers entries that payload apps submit via `TelemetryService.log`/`report`. The supervisor (which keeps the M1-verified transport) `drain`s it once per wake — after the payload-observe window — and ships JSONL over a new `data?id=` WRQ. The gateway handler ingests it line-by-line into a new `data_log` table. Default-off means the verified M1 path is unchanged unless `set-console on` is issued.

**Tech Stack:** Toit (SDK v2.0.0-alpha.192); `toit-sqlite` for the host gateway; `system.services` for the device provider/client; `pkg-cli` for the CLI; `tftp` for the wire.

**Spec:** `docs/specs/2026-05-24-m2-telemetry-design.md`.

**Toolchain & test commands:**
- Gateway (host, sqlite): `cd /home/david/workspaceToit/porta/gateway && ~/workspaceToit/sqlite/build/bin/toit-sqlite run <file>.toit`
- Device (host-runnable logic tests): `cd /home/david/workspaceToit/porta && toit run device/<file>.toit`
- Device compile gate (supervisor — imports esp32/system, not host-runnable): `cd /home/david/workspaceToit/porta && toit compile -s -o /tmp/sup.snapshot device/supervisor.toit`
- There is no `toit test` on this SDK; every test is a `main` using `import expect show *`.

**Suggested execution order:** Phase B (gateway, host-TDD) and Phase C tasks C1–C5 (device, host-TDD + compile gate) need no hardware — do them first. Then do Phase A (spike) and Task C6 (verification) together on `fwkb`.

---

## File Structure

**Gateway (modify):**
- `gateway/store.toit` — add `data_log` table + `insert-data` / `query-data` / `prune-data`.
- `gateway/handler.toit` — add `data?id=` WRQ ingest (`DataWriter_`, JSONL line-parse).
- `gateway/command.toit` — add `VERB-SET-CONSOLE` + `Command.set-console`.
- `gateway/gateway.toit` — add `monitor` and `device set-console` CLI verbs + `monitor-line_`.

**Gateway (create tests):**
- `gateway/data_log_test.toit`, `gateway/data_ingest_test.toit`, `gateway/monitor_test.toit`, `gateway/set_console_test.toit`.

**Device (create):**
- `device/telemetry_buffer.toit` — bounded ring buffer (drop-oldest + dropped-count), `drain`.
- `device/telemetry_codec.toit` — `build-data-body` (entries → JSONL `ByteArray`).
- `device/telemetry_service.toit` — `TelemetryService` API + client + provider.
- `device/chatty.toit` — test payload that emits telemetry (hardware verification).
- Tests: `device/telemetry_buffer_test.toit`, `device/telemetry_codec_test.toit`, `device/telemetry_service_test.toit`, `device/set_console_apply_test.toit`.

**Device (modify):**
- `device/node_command.toit` — add `VERB-SET-CONSOLE` + `is-set-console`.
- `device/supervisor.toit` — spawn the provider; apply `set-console`; flush telemetry after observe.

---

## Phase A — M2.0 spike (hardware; informs Task C3 only)

### Task A1: PrintService-displacement spike

**Question:** Can a spawned, non-system container register the SDK's `PrintService`
(`system.api.print`, UUID `0b7e3aa1-9fc9-4632-bb09-4605cd11897e`) and thereby capture
`print` output emitted by a *different* container?

**Why it does NOT block the build:** the primary console path in this plan is the
**explicit** `TelemetryServiceClient.log msg` call (vindriktning's proven `LogService`
pattern). The spike only decides whether Task C3's provider *additionally* registers
`PrintService` so unmodified `print` is also captured. If the spike fails, ship with
explicit `log` only.

**Files:**
- Create (throwaway): `device/spike_print_provider.toit`, `device/spike_print_client.toit`

- [ ] **Step 1: Write the provider spike**

```toit
// device/spike_print_provider.toit — does a custom PrintService provider see
// another container's print? Run as the boot container; install the client too.
import system.services
import system.api.print show PrintService

class SpikePrintProvider extends services.ServiceProvider
    implements services.ServiceHandler:
  constructor:
    super "porta/spike-print" --major=0 --minor=1
    provides PrintService.SELECTOR --handler=this --priority=services.ServiceProvider.PRIORITY-PREFERRED
  handle index/int arguments/any --gid/int --client/int -> any:
    if index == PrintService.PRINT-INDEX:
      // Forward to UART so we can SEE captured lines in `jag monitor`.
      print_ "SPIKE-CAPTURED: $arguments"
      return null
    unreachable

main:
  (SpikePrintProvider).install
  while true: sleep (Duration --s=3600)
```

- [ ] **Step 2: Write the client spike (a separate container that just prints)**

```toit
// device/spike_print_client.toit — a separate container that prints normally.
main:
  5.repeat: | i |
    print "client-app: hello $i"
    sleep --ms=500
```

- [ ] **Step 3: Build + flash + observe**

Build the provider into the envelope as boot container, install the client as a
second container (see build/flash recipe in Task C6). In `jag monitor`, look for
`SPIKE-CAPTURED: client-app: hello 0`.

Decision gate:
- **Captured** → in Task C3, also register `PrintService` in the provider, forwarding into the buffer (`kind=log`).
- **Not captured** (or unstable) → skip print-interception; rely on explicit `log`.

- [ ] **Step 4: Record the outcome in the spec**

Append a short "M2.0 spike result" note to `docs/specs/2026-05-24-m2-telemetry-design.md` (captured / not), then delete the two `spike_*` files. Commit:

```bash
git rm -f device/spike_print_provider.toit device/spike_print_client.toit
git add docs/specs/2026-05-24-m2-telemetry-design.md
git commit -m "spike(device): record M2.0 PrintService-displacement result"
```

---

## Phase B — Gateway `data_log` (host-TDD)

### Task B1: `data_log` table + store methods

**Files:**
- Modify: `gateway/store.toit` (add table in `init-schema_`; add three methods)
- Test: `gateway/data_log_test.toit`

- [ ] **Step 1: Write the failing test**

```toit
// gateway/data_log_test.toit
import expect show *
import .store show Store

main:
  store := Store.open ":memory:"
  expect (store.has-table_ "data_log")

  // Insert a metric and a log line for one device.
  store.insert-data "aabbccddeeff" --ts=100 --seq=0 --kind="metric" --name="pm" --value=13.0
  store.insert-data "aabbccddeeff" --ts=101 --seq=1 --kind="log" --text="started blink"
  // A row for a different, later device + time (must not leak into the query below).
  store.insert-data "010203040506" --ts=500 --seq=0 --kind="metric" --name="t" --value=20.5

  rows := store.query-data "aabbccddeeff" --since=0 --until=200
  expect-equals 2 rows.size
  expect-equals "metric" rows[0]["kind"]            // oldest first (ts, seq)
  expect-equals "pm" rows[0]["name"]
  expect-equals 13.0 rows[0]["value"]
  expect-equals "log" rows[1]["kind"]
  expect-equals "started blink" rows[1]["text"]

  // kind filter.
  only-metrics := store.query-data "aabbccddeeff" --since=0 --until=200 --kind="metric"
  expect-equals 1 only-metrics.size

  // time window excludes out-of-range rows.
  expect-equals 0 (store.query-data "aabbccddeeff" --since=200 --until=300).size

  // prune drops rows older than the cutoff.
  store.prune-data --cutoff=101
  expect-equals 1 (store.query-data "aabbccddeeff" --since=0 --until=200).size  // ts=100 pruned, ts=101 kept

  store.close
  print "data_log OK"
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd /home/david/workspaceToit/porta/gateway && ~/workspaceToit/sqlite/build/bin/toit-sqlite run data_log_test.toit`
Expected: FAIL (no `data_log` table / `insert-data` undefined).

- [ ] **Step 3: Add the table to `init-schema_`**

In `gateway/store.toit`, inside `init-schema_`, after the `reports` table block, add:

```toit
    db_.execute """
        CREATE TABLE IF NOT EXISTS data_log (
          id INTEGER PRIMARY KEY AUTOINCREMENT,
          device_id TEXT,
          ts INTEGER,
          seq INTEGER,
          kind TEXT,
          name TEXT,
          value REAL,
          text TEXT)"""
    db_.execute "CREATE INDEX IF NOT EXISTS idx_data_device_ts ON data_log(device_id, ts)"
```

- [ ] **Step 4: Add the three methods**

In `gateway/store.toit`, after `reports`, add:

```toit
  /** Appends one telemetry entry for node $device-id. */
  insert-data device-id/string --ts/int --seq/int --kind/string --name/string?=null --value/float?=null --text/string?=null -> none:
    db_.execute "INSERT INTO data_log (device_id, ts, seq, kind, name, value, text) VALUES (?, ?, ?, ?, ?, ?, ?)"
        [device-id, ts, seq, kind, name, value, text]

  /**
  Returns $device-id's telemetry rows with $since <= ts <= $until (epoch seconds),
    oldest first by (ts, seq); when $kind is given, only that kind.
  */
  query-data device-id/string --since/int --until/int --kind/string?=null -> List:
    result := []
    sql := "SELECT ts, seq, kind, name, value, text FROM data_log WHERE device_id = ? AND ts >= ? AND ts <= ?"
    params := [device-id, since, until]
    if kind != null:
      sql += " AND kind = ?"
      params.add kind
    sql += " ORDER BY ts, seq"
    db_.query sql params: | row |
      result.add {"ts": row[0], "seq": row[1], "kind": row[2], "name": row[3], "value": row[4], "text": row[5]}
    return result

  /** Deletes telemetry rows with ts < $cutoff (epoch seconds). */
  prune-data --cutoff/int -> none:
    db_.execute "DELETE FROM data_log WHERE ts < ?" [cutoff]
```

- [ ] **Step 5: Run to verify it passes**

Run: `cd /home/david/workspaceToit/porta/gateway && ~/workspaceToit/sqlite/build/bin/toit-sqlite run data_log_test.toit`
Expected: PASS, prints `data_log OK`.

- [ ] **Step 6: Commit**

```bash
git add gateway/store.toit gateway/data_log_test.toit
git commit -m "feat(gateway): data_log table + insert/query/prune store methods"
```

### Task B2: `data?id=` WRQ ingest (JSONL, truncation-tolerant)

**Files:**
- Modify: `gateway/handler.toit` (extend `writer-for`; add `DataWriter_`)
- Test: `gateway/data_ingest_test.toit`

- [ ] **Step 1: Write the failing test**

```toit
// gateway/data_ingest_test.toit
import expect show *
import .handler show StoreBackedHandler
import .store show Store
import tftp show STORAGE-ACCESS-DENIED

main:
  store := Store.open ":memory:"
  handler := StoreBackedHandler store
  store.ensure-node "aabbccddeeff" --now=1000

  // A WRQ to "data" ingests JSONL: one entry per line. The trailing line is
  // truncated (no closing brace) and must be skipped, not abort the batch.
  body := ("{\"ts\":100,\"seq\":0,\"kind\":\"metric\",\"name\":\"pm\",\"value\":13}\n"
         + "{\"ts\":101,\"seq\":1,\"kind\":\"log\",\"text\":\"hi\"}\n"
         + "{\"ts\":102,\"seq\":2,\"kind\":\"met").to-byte-array
  w := handler.writer-for "data?id=aabbccddeeff"
  w.write body
  w.close

  rows := store.query-data "aabbccddeeff" --since=0 --until=200
  expect-equals 2 rows.size                          // 2 good lines; truncated 3rd skipped
  expect-equals 13.0 rows[0]["value"]                // int 13 stored as REAL 13.0
  expect-equals "hi" rows[1]["text"]

  // contact was recorded.
  expect ((store.node "aabbccddeeff")["last_seen"]) != null

  // A WRQ to an unknown resource is still refused.
  expect-throw STORAGE-ACCESS-DENIED: handler.writer-for "bogus?id=aabbccddeeff"

  store.close
  print "data ingest OK"
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd /home/david/workspaceToit/porta/gateway && ~/workspaceToit/sqlite/build/bin/toit-sqlite run data_ingest_test.toit`
Expected: FAIL (writer-for refuses "data" → throws ACCESS-DENIED).

- [ ] **Step 3: Add `encoding.json` import and extend `writer-for`**

In `gateway/handler.toit`, add to the imports at the top:

```toit
import encoding.json
```

Replace the existing `writer-for` body with:

```toit
  writer-for name/string --req/Request?=null --tsize-hint/int?=null -> io.CloseableWriter:
    parsed := parse-resource_ name
    base := parsed[0]
    id := parsed[1].get "id"
    if id == null: throw STORAGE-ACCESS-DENIED
    store_.touch-node id --now=now_
    if base == "report": return ReportWriter_ store_ id now_
    if base == "data": return DataWriter_ store_ id now_
    throw STORAGE-ACCESS-DENIED
```

- [ ] **Step 4: Add the `DataWriter_` class**

In `gateway/handler.toit`, after `ReportWriter_`, add:

```toit
/**
An $io.CloseableWriter that buffers a WRQ "data" body (JSONL — one telemetry entry
  per line) and, on close, decodes each line and appends it to the data_log. A line
  that fails to decode (e.g. a truncated final line) is skipped, so a short tail
  costs only that line. Each entry is {"ts"?,"seq"?,"kind","name"?,"value"?,"text"?};
  missing ts/seq default to the gateway receive time / line index.
*/
class DataWriter_ extends io.CloseableWriter:
  store_/Store
  id_/string
  now_/int
  buffer_/Buffer := Buffer
  constructor .store_ .id_ .now_:

  try-write_ data/io.Data from/int to/int -> int:
    buffer_.write data from to
    return to - from

  close_ -> none:
    line-no := 0
    (buffer_.bytes.to-string.split "\n").do: | line/string |
      if line.trim == "": continue.do
      entry/Map? := null
      catch: entry = json.decode line.to-byte-array
      if entry == null: continue.do
      value := entry.get "value"
      if value is int: value = value.to-float
      store_.insert-data id_
          --ts=(entry.get "ts" --if-absent=: now_)
          --seq=(entry.get "seq" --if-absent=: line-no)
          --kind=(entry.get "kind" --if-absent=: "log")
          --name=(entry.get "name")
          --value=value
          --text=(entry.get "text")
      line-no++
```

- [ ] **Step 5: Run to verify it passes**

Run: `cd /home/david/workspaceToit/porta/gateway && ~/workspaceToit/sqlite/build/bin/toit-sqlite run data_ingest_test.toit`
Expected: PASS, prints `data ingest OK`.

- [ ] **Step 6: Confirm the existing handler test still passes (no regression)**

Run: `cd /home/david/workspaceToit/porta/gateway && ~/workspaceToit/sqlite/build/bin/toit-sqlite run handler_test.toit`
Expected: PASS (report WRQ path unchanged; non-report/non-data still refused).

- [ ] **Step 7: Commit**

```bash
git add gateway/handler.toit gateway/data_ingest_test.toit
git commit -m "feat(gateway): ingest data?id= WRQ as JSONL into data_log"
```

### Task B3: `gateway monitor` CLI

**Files:**
- Modify: `gateway/gateway.toit` (add `monitor-cmd`, `cmd-monitor`, `monitor-line_`; register subcommand)
- Test: `gateway/monitor_test.toit`

- [ ] **Step 1: Write the failing test (formatter is the pure, testable unit)**

```toit
// gateway/monitor_test.toit
import expect show *
import .gateway show monitor-line_

main:
  metric := {"ts": 100, "seq": 0, "kind": "metric", "name": "pm", "value": 13.0, "text": null}
  expect-equals "100  metric  pm=13.0" (monitor-line_ metric)
  log := {"ts": 101, "seq": 1, "kind": "log", "name": null, "value": null, "text": "started blink"}
  expect-equals "101  log     started blink" (monitor-line_ log)
  print "monitor-line OK"
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd /home/david/workspaceToit/porta/gateway && ~/workspaceToit/sqlite/build/bin/toit-sqlite run monitor_test.toit`
Expected: FAIL (`monitor-line_` undefined / not exported).

- [ ] **Step 3: Add the formatter + command + handler**

In `gateway/gateway.toit`, add the `monitor-line_` helper near `pad_`:

```toit
/** Formats a data_log row {ts,seq,kind,name,value,text} for `monitor`. */
monitor-line_ r/Map -> string:
  if r["kind"] == "metric": return "$(r["ts"])  metric  $(r["name"])=$(r["value"])"
  return "$(r["ts"])  log     $(r["text"])"
```

Add the command definition in `build-command` (before the final `return`):

```toit
  monitor-cmd := cli.Command "monitor"
      --help="Show a node's telemetry (data_log); --follow tails new rows as wakes deliver them."
      --options=[
        cli.Option "device" --short-name="d" --help="Node name or MAC." --required,
        cli.Option "since" --help="Look-back window, e.g. 1h, 30m (default 1h).",
        cli.Flag "follow" --short-name="f" --help="Keep polling and print new rows until interrupted.",
        cli.Option "kind" --help="Filter to 'log' or 'metric'.",
      ]
      --run=:: cmd-monitor it
```

Register it in the top-level `--subcommands` list:

```toit
      --subcommands=[ serve-cmd, scan-cmd, ping-cmd, device-cmd, container-cmd, log-cmd, monitor-cmd ]
```

Add the command handler near `cmd-log`:

```toit
cmd-monitor parsed/cli.Parsed -> none:
  store := open-store_ parsed
  id := resolve-node-id_ store parsed["device"]
  kind := parsed["kind"]
  since-s := parsed["since"] != null ? (parse-duration-s parsed["since"]) : 3600
  now := now_
  (store.query-data id --since=(now - since-s) --until=now --kind=kind).do: | r/Map |
    print (monitor-line_ r)
  if parsed["follow"]:
    last := now
    while true:
      sleep --ms=2000
      t := now_
      (store.query-data id --since=(last + 1) --until=t --kind=kind).do: | r/Map |
        print (monitor-line_ r)
      last = t
  store.close
```

- [ ] **Step 4: Run to verify it passes**

Run: `cd /home/david/workspaceToit/porta/gateway && ~/workspaceToit/sqlite/build/bin/toit-sqlite run monitor_test.toit`
Expected: PASS, prints `monitor-line OK`.

- [ ] **Step 5: Smoke-check the CLI wiring compiles/parses**

Run: `cd /home/david/workspaceToit/porta/gateway && ~/workspaceToit/sqlite/build/bin/toit-sqlite run gateway.toit -- --db=/tmp/m2.db monitor --help`
Expected: prints the monitor help text (no crash).

- [ ] **Step 6: Commit**

```bash
git add gateway/gateway.toit gateway/monitor_test.toit
git commit -m "feat(gateway): monitor CLI (range + --follow) over data_log"
```

### Task B4: `set-console` command + `device set-console` CLI

**Files:**
- Modify: `gateway/command.toit` (add verb + factory), `gateway/gateway.toit` (CLI verb)
- Test: `gateway/set_console_test.toit`

- [ ] **Step 1: Write the failing test**

```toit
// gateway/set_console_test.toit
import expect show *
import .command show Command VERB-SET-CONSOLE

main:
  cmd := Command.set-console --on=true
  expect-equals VERB-SET-CONSOLE cmd.verb
  expect-equals true cmd.args["on"]
  // Round-trips through the wire codec.
  round := Command.decode cmd.encode
  expect-equals VERB-SET-CONSOLE round.verb
  expect-equals true round.args["on"]
  // It is at least one byte (preserves the drain-sentinel invariant).
  expect (cmd.encode.size >= 1)
  print "set-console command OK"
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd /home/david/workspaceToit/porta/gateway && ~/workspaceToit/sqlite/build/bin/toit-sqlite run set_console_test.toit`
Expected: FAIL (`VERB-SET-CONSOLE` / `Command.set-console` undefined).

- [ ] **Step 3: Add the verb + factory to `command.toit`**

In `gateway/command.toit`, add the constant near the other verbs:

```toit
VERB-SET-CONSOLE ::= "set-console"
```

Add the factory after `set-poll-interval`:

```toit
  /** Builds a command turning the node's console/telemetry forwarding $on. */
  static set-console --on/bool -> Command:
    return Command VERB-SET-CONSOLE {"on": on}
```

- [ ] **Step 4: Run to verify it passes**

Run: `cd /home/david/workspaceToit/porta/gateway && ~/workspaceToit/sqlite/build/bin/toit-sqlite run set_console_test.toit`
Expected: PASS, prints `set-console command OK`.

- [ ] **Step 5: Add the CLI verb**

In `gateway/gateway.toit`, add the command in `build-command`:

```toit
  device-set-console-cmd := cli.Command "set-console"
      --help="Enqueue turning a node's console/telemetry forwarding on or off (off by default)."
      --options=[ cli.Option "device" --short-name="d" --help="Node name or MAC." --required ]
      --rest=[ cli.Option "state" --help="on | off." --required ]
      --run=:: cmd-device-set-console it
```

Add it to `device-cmd`'s subcommands:

```toit
  device-cmd := cli.Command "device"
      --help="Inspect and configure a node."
      --subcommands=[
        device-show-cmd, device-set-max-offline-cmd,
        device-set-poll-interval-cmd, device-name-cmd, device-set-console-cmd,
      ]
```

Add the handler near `cmd-device-set-poll-interval`:

```toit
cmd-device-set-console parsed/cli.Parsed -> none:
  store := open-store_ parsed
  id := resolve-node-id_ store parsed["device"]
  store.ensure-node id --now=now_
  state := parsed["state"]
  if state != "on" and state != "off":
    print "Error: state must be 'on' or 'off'."
    exit 1
  cmd-id := store.enqueue-command id (Command.set-console --on=(state == "on")) --issued-by="cli" --now=now_
  print "$id: enqueued set-console $state (command #$cmd-id)"
  store.close
```

- [ ] **Step 6: Smoke-check the CLI wiring**

Run: `cd /home/david/workspaceToit/porta/gateway && ~/workspaceToit/sqlite/build/bin/toit-sqlite run gateway.toit -- --db=/tmp/m2.db device set-console -d aabbccddeeff on`
Expected: prints `aabbccddeeff: enqueued set-console on (command #1)`.

- [ ] **Step 7: Commit**

```bash
git add gateway/command.toit gateway/gateway.toit gateway/set_console_test.toit
git commit -m "feat(gateway): set-console command + device set-console CLI"
```

---

## Phase C — Device telemetry provider + supervisor wiring

### Task C1: `TelemetryBuffer` (bounded ring)

**Files:**
- Create: `device/telemetry_buffer.toit`
- Test: `device/telemetry_buffer_test.toit`

- [ ] **Step 1: Write the failing test**

```toit
// device/telemetry_buffer_test.toit
import expect show *
import .telemetry_buffer show TelemetryBuffer

main:
  buf := TelemetryBuffer --cap=3
  buf.add {"kind": "log", "text": "a"}
  buf.add {"kind": "metric", "name": "pm", "value": 13.0}
  expect-equals 2 buf.size

  // drain returns all entries (oldest first) and empties the buffer.
  out := buf.drain
  expect-equals 2 out.size
  expect-equals "a" out[0]["text"]
  expect-equals 0 buf.size
  expect-equals 0 (buf.drain).size                 // empty drain → no entries

  // Overflow drops the oldest and prepends a dropped-count marker on the next drain.
  4.repeat: | i | buf.add {"kind": "log", "text": "$i"}   // cap=3, so "0" is dropped
  dumped := buf.drain
  expect-equals 4 dumped.size                       // 1 marker + 3 survivors
  expect-equals "log" dumped[0]["kind"]
  expect (dumped[0]["text"].contains "dropped 1")
  expect-equals "1" dumped[1]["text"]               // oldest survivor
  print "telemetry buffer OK"
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd /home/david/workspaceToit/porta && toit run device/telemetry_buffer_test.toit`
Expected: FAIL (`telemetry_buffer` module / `TelemetryBuffer` undefined).

- [ ] **Step 3: Implement the buffer**

```toit
// device/telemetry_buffer.toit — a bounded in-RAM ring of telemetry entries for
// one wake window. Lives in the spawned telemetry provider process; the supervisor
// drains it once per wake before deep-sleep. RAM-only by design: deep-sleep wipes it.
/**
A bounded buffer of telemetry entries (each a Map). At capacity, $add drops the
  oldest entry and counts the drop; $drain returns every buffered entry oldest-first
  (a leading {"kind":"log"} marker noting how many were dropped, if any) and empties
  the buffer.
*/
class TelemetryBuffer:
  cap_/int
  entries_/Deque := Deque
  dropped_/int := 0

  constructor --cap/int=128:
    cap_ = cap

  /** Appends $entry, dropping the oldest if already at capacity. */
  add entry/Map -> none:
    if entries_.size >= cap_:
      entries_.remove-first
      dropped_++
    entries_.add entry

  /** Number of entries currently buffered. */
  size -> int: return entries_.size

  /** Returns all entries (oldest first, after an optional dropped-count marker) and empties the buffer. */
  drain -> List:
    out := []
    if dropped_ > 0:
      out.add {"kind": "log", "text": "telemetry: dropped $dropped_ entries"}
      dropped_ = 0
    while not entries_.is-empty: out.add entries_.remove-first
    return out
```

- [ ] **Step 4: Run to verify it passes**

Run: `cd /home/david/workspaceToit/porta && toit run device/telemetry_buffer_test.toit`
Expected: PASS, prints `telemetry buffer OK`.

- [ ] **Step 5: Commit**

```bash
git add device/telemetry_buffer.toit device/telemetry_buffer_test.toit
git commit -m "feat(device): bounded telemetry ring buffer"
```

### Task C2: `build-data-body` (entries → JSONL)

**Files:**
- Create: `device/telemetry_codec.toit`
- Test: `device/telemetry_codec_test.toit`

- [ ] **Step 1: Write the failing test**

```toit
// device/telemetry_codec_test.toit
import expect show *
import encoding.json
import .telemetry_codec show build-data-body

main:
  entries := [
    {"kind": "metric", "name": "pm", "value": 13.0},
    {"kind": "log", "text": "hi"},
  ]
  body := build-data-body entries
  // Body is JSONL: one decodable object per non-empty line, in order.
  lines := body.to-string.split "\n"
  // Trailing newline → a final empty element; filter it.
  decoded := []
  lines.do: | l/string | if l.trim != "": decoded.add (json.decode l.to-byte-array)
  expect-equals 2 decoded.size
  expect-equals "pm" decoded[0]["name"]
  expect-equals "hi" decoded[1]["text"]

  // Empty input → empty body.
  expect-equals 0 (build-data-body []).size
  print "telemetry codec OK"
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd /home/david/workspaceToit/porta && toit run device/telemetry_codec_test.toit`
Expected: FAIL (`telemetry_codec` / `build-data-body` undefined).

- [ ] **Step 3: Implement the codec**

```toit
// device/telemetry_codec.toit — encodes telemetry entries as JSONL (one entry per
// line) for the data?id= WRQ. JSONL keeps the device side streaming (encode one
// entry at a time) and the gateway side bounded (decode + insert one line at a time).
import encoding.json
import io.buffer show Buffer

/** Encodes $entries (each a Map) as JSONL: json.encode each, newline-separated. */
build-data-body entries/List -> ByteArray:
  buf := Buffer
  entries.do: | e/Map |
    buf.write (json.encode e)
    buf.write "\n"
  return buf.bytes
```

- [ ] **Step 4: Run to verify it passes**

Run: `cd /home/david/workspaceToit/porta && toit run device/telemetry_codec_test.toit`
Expected: PASS, prints `telemetry codec OK`.

- [ ] **Step 5: Commit**

```bash
git add device/telemetry_codec.toit device/telemetry_codec_test.toit
git commit -m "feat(device): JSONL telemetry body encoder"
```

### Task C3: `TelemetryService` (API + client + provider)

**Files:**
- Create: `device/telemetry_service.toit`
- Test: `device/telemetry_service_test.toit`

- [ ] **Step 1: Write the failing test (provider spawned in-process; client drives it)**

```toit
// device/telemetry_service_test.toit
import expect show *
import .telemetry_buffer show TelemetryBuffer
import .telemetry_service show TelemetryServiceClient TelemetryServiceProvider

main:
  spawn::
    provider := TelemetryServiceProvider (TelemetryBuffer --cap=64)
    provider.install
    sleep (Duration --s=2)
    provider.uninstall
  yield  // let the provider register before we open a client

  client := TelemetryServiceClient
  client.open
  client.log "hello"
  client.report "pm" 13.0
  out := client.drain
  expect-equals 2 out.size
  expect-equals "log" out[0]["kind"]
  expect-equals "hello" out[0]["text"]
  expect-equals "metric" out[1]["kind"]
  expect-equals "pm" out[1]["name"]
  expect-equals 13.0 out[1]["value"]
  // drain emptied the buffer.
  expect-equals 0 (client.drain).size
  client.close
  print "telemetry service OK"
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd /home/david/workspaceToit/porta && toit run device/telemetry_service_test.toit`
Expected: FAIL (`telemetry_service` module undefined).

- [ ] **Step 3: Implement the service**

```toit
// device/telemetry_service.toit — the device-wide telemetry API. Payload apps open
// a TelemetryServiceClient and call `log`/`report`; the provider (spawned by the
// supervisor) buffers entries, and the supervisor `drain`s them once per wake.
import system.services
import .telemetry_buffer show TelemetryBuffer

interface TelemetryService:
  static SELECTOR ::= services.ServiceSelector
      --uuid="7c3a1e90-2b4d-4f8a-9c6e-5a1b2c3d4e5f"
      --major=1
      --minor=0
  log message/string -> none
  report name/string value/float -> none
  drain -> List
  static LOG-INDEX ::= 0
  static REPORT-INDEX ::= 1
  static DRAIN-INDEX ::= 2

class TelemetryServiceClient extends services.ServiceClient implements TelemetryService:
  static SELECTOR ::= TelemetryService.SELECTOR
  constructor selector/services.ServiceSelector=SELECTOR:
    assert: selector.matches SELECTOR
    super selector

  log message/string -> none: invoke_ TelemetryService.LOG-INDEX message
  report name/string value/float -> none: invoke_ TelemetryService.REPORT-INDEX [name, value]
  drain -> List: return invoke_ TelemetryService.DRAIN-INDEX null

class TelemetryServiceProvider extends services.ServiceProvider
    implements TelemetryService services.ServiceHandler:
  buffer_/TelemetryBuffer
  constructor .buffer_:
    super "porta/telemetry" --major=1 --minor=0
    provides TelemetryService.SELECTOR --handler=this

  handle index/int arguments/any --gid/int --client/int -> any:
    if index == TelemetryService.LOG-INDEX: return log arguments
    if index == TelemetryService.REPORT-INDEX: return report arguments[0] arguments[1]
    if index == TelemetryService.DRAIN-INDEX: return drain
    unreachable

  log message/string -> none: buffer_.add {"kind": "log", "text": message}
  report name/string value/float -> none: buffer_.add {"kind": "metric", "name": name, "value": value}
  drain -> List: return buffer_.drain
```

> **M2.0 hook:** if Task A1 showed `print` is capturable, also register
> `PrintService` here (a second `provides` + a `ServiceHandler` whose `print`
> calls `buffer_.add {"kind":"log","text":message}`) so unmodified `print` is
> captured too. Otherwise leave as-is (explicit `log` only).

- [ ] **Step 4: Run to verify it passes**

Run: `cd /home/david/workspaceToit/porta && toit run device/telemetry_service_test.toit`
Expected: PASS, prints `telemetry service OK`.

- [ ] **Step 5: Commit**

```bash
git add device/telemetry_service.toit device/telemetry_service_test.toit
git commit -m "feat(device): TelemetryService (log/report/drain) + provider"
```

### Task C4: `set-console` decode on the device

**Files:**
- Modify: `device/node_command.toit` (add verb + `is-set-console`)
- Test: `device/set_console_apply_test.toit`

- [ ] **Step 1: Write the failing test**

```toit
// device/set_console_apply_test.toit
import expect show *
import encoding.json
import .node_command show NodeCommand

main:
  bytes := json.encode {"verb": "set-console", "on": true}
  cmd := NodeCommand.decode bytes
  expect cmd.is-set-console
  expect-equals true (cmd.args.get "on")
  // A run command is not a set-console.
  run := NodeCommand.decode (json.encode {"verb": "run", "name": "blink", "crc": 1, "size": 2})
  expect-not run.is-set-console
  print "set-console decode OK"
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd /home/david/workspaceToit/porta && toit run device/set_console_apply_test.toit`
Expected: FAIL (`is-set-console` undefined).

- [ ] **Step 3: Add the verb + helper**

In `device/node_command.toit`, add the constant near the other verbs:

```toit
VERB-SET-CONSOLE ::= "set-console"
```

Add the helper next to `is-set-poll`:

```toit
  is-set-console -> bool: return verb == VERB-SET-CONSOLE
```

- [ ] **Step 4: Run to verify it passes**

Run: `cd /home/david/workspaceToit/porta && toit run device/set_console_apply_test.toit`
Expected: PASS, prints `set-console decode OK`.

- [ ] **Step 5: Commit**

```bash
git add device/node_command.toit device/set_console_apply_test.toit
git commit -m "feat(device): decode set-console command"
```

### Task C5: Supervisor wiring (spawn provider, apply set-console, flush telemetry)

**Files:**
- Modify: `device/supervisor.toit`

This task is not host-runnable (imports `esp32`/`system`); the gate is a clean compile.

- [ ] **Step 1: Add imports + the console NVS key**

In `device/supervisor.toit`, add to the imports:

```toit
import .telemetry_buffer show TelemetryBuffer
import .telemetry_service show TelemetryServiceClient TelemetryServiceProvider
import .telemetry_codec show build-data-body
```

Add the NVS key near `POLL-INTERVAL-KEY`:

```toit
CONSOLE-KEY ::= "console_forward"
```

- [ ] **Step 2: Spawn the telemetry provider, and reorder `main` to flush after observe**

Replace the body of `main` (from `start-installed inventory` onward) so the provider
is spawned before payloads start and telemetry is flushed after the observe window.
The new `main` reads:

```toit
main:
  print "supervisor: awake (cause=$esp32.wakeup-cause)"
  id := mac-to-id esp32.mac-address
  bucket := storage.Bucket.open --flash BUCKET-NAME
  inventory := load-inventory bucket
  poll-interval-s := bucket.get POLL-INTERVAL-KEY --if-absent=: DEFAULT-POLL-S
  store := ScheduleStore
  now := clock-us

  // Bring up the telemetry provider before any payload app can emit.
  spawn-remoting_
  sleep --ms=50  // let the provider register before payloads open clients

  // Poll on cold boot (empty inventory) or once the poll interval has elapsed.
  cold := inventory.apps.is-empty
  poll-due := cold or (now - store.last-poll-us) >= (poll-interval-s * 1_000_000)
  if poll-due:
    catch --trace:
      poll-interval-s = poll-and-reconcile bucket inventory id poll-interval-s store
      store.last-poll-us = now

  start-installed inventory
  arm-wakeups inventory

  print "supervisor: observing for $OBSERVE"
  sleep OBSERVE

  // Ship telemetry produced this wake (after payloads ran), if forwarding is on.
  if (bucket.get CONSOLE-KEY --if-absent=: false): flush-telemetry_ id

  print "supervisor: deep-sleeping for $(poll-interval-s)s"
  esp32.deep-sleep (Duration --s=poll-interval-s)
```

- [ ] **Step 3: Add `spawn-remoting_` and `flush-telemetry_`**

Add these helpers to `device/supervisor.toit`:

```toit
/** Spawns the telemetry provider in its own process (services only, no socket). */
spawn-remoting_ -> none:
  spawn::
    provider := TelemetryServiceProvider (TelemetryBuffer --cap=128)
    provider.install
    while true: sleep (Duration --s=3600)  // outlive the wake window; deep-sleep ends it

/**
Drains the telemetry buffer (a client call to the spawned provider) and, if any
  entries accrued this wake, ships them as a JSONL "data?id=" WRQ. Best-effort:
  any failure is traced and the node still deep-sleeps.
*/
flush-telemetry_ id/string -> none:
  catch --trace:
    tclient := TelemetryServiceClient
    tclient.open
    entries := tclient.drain
    tclient.close
    if entries.is-empty: return
    body := build-data-body entries
    gw := (WifiTransport --host=GATEWAY-HOST --port=GATEWAY-PORT).connect
    try:
      gw.put "data?id=$id" body
      print "supervisor: shipped $(entries.size) telemetry entr(ies)"
    finally:
      gw.close
```

- [ ] **Step 4: Apply `set-console` in the command-drain loop**

In `poll-and-reconcile`, extend the drain loop's command dispatch. Replace:

```toit
      if command.is-set-poll:
        poll-interval-s = command.interval-s
        bucket[POLL-INTERVAL-KEY] = poll-interval-s
        print "supervisor: poll interval now $(poll-interval-s)s"
      else:
        apply-to-goal goal-map command
        print "supervisor: applied $command.verb $(command.name)"
```

with:

```toit
      if command.is-set-poll:
        poll-interval-s = command.interval-s
        bucket[POLL-INTERVAL-KEY] = poll-interval-s
        print "supervisor: poll interval now $(poll-interval-s)s"
      else if command.is-set-console:
        on := command.args.get "on" --if-absent=: false
        bucket[CONSOLE-KEY] = on
        print "supervisor: console-forward now $on"
      else:
        apply-to-goal goal-map command
        print "supervisor: applied $command.verb $(command.name)"
```

- [ ] **Step 5: Compile-gate the supervisor**

Run: `cd /home/david/workspaceToit/porta && toit compile -s -o /tmp/sup.snapshot device/supervisor.toit`
Expected: compiles with no errors (a clean snapshot is written).

- [ ] **Step 6: Re-run the device host tests (no regression)**

Run: `cd /home/david/workspaceToit/porta && for t in node_command report inventory goal_state triggers image_writer telemetry_buffer telemetry_codec telemetry_service set_console_apply; do toit run device/$t\_test.toit; done`
Expected: every test prints its OK line, no failures.

- [ ] **Step 7: Commit**

```bash
git add device/supervisor.toit
git commit -m "feat(device): spawn telemetry provider, apply set-console, flush telemetry per wake"
```

### Task C6: Test payload + hardware verification on `fwkb`

**Files:**
- Create: `device/chatty.toit`

This task is manual hardware verification (no TDD cycle). It also folds in the Phase A spike if not already done.

- [ ] **Step 1: Write the chatty test payload**

```toit
// device/chatty.toit — a test payload that emits telemetry each run, to verify the
// M2 up-path end to end. Install it via `gateway container install`.
import .telemetry_service show TelemetryServiceClient

main:
  client := TelemetryServiceClient
  client.open
  5.repeat: | i |
    client.log "chatty: tick $i"
    client.report "counter" i.to-float
    sleep --ms=500
  client.close
  print "chatty: done"
```

- [ ] **Step 2: Build + flash the supervisor firmware**

```bash
cd /home/david/workspaceToit/porta
rm -f firmware-esp32.envelope supervisor.image supervisor.snapshot
bash host/build-envelope.sh
jag flash firmware-esp32.envelope --exclude-jaguar --wifi-ssid "<SSID>" --wifi-password "<PW>" --port /dev/ttyUSB0
```
Expected: `firmware show` lists `supervisor`, no `jaguar`. Device boots; `jag monitor -a --port /dev/ttyUSB0` shows `supervisor: awake`.

- [ ] **Step 3: Start the gateway daemon (own terminal)**

```bash
export TS=~/workspaceToit/sqlite/build/bin/toit-sqlite
cd /home/david/workspaceToit/porta/gateway
$TS run gateway.toit -- --db=/tmp/porta-fwkb.db serve --port=6969
```
Confirm the supervisor's `GATEWAY-HOST`/`GATEWAY-PORT` match this host:6969.

- [ ] **Step 4: Build the chatty payload image**

```bash
cd /home/david/workspaceToit/porta
toit compile -s -o chatty.snapshot device/chatty.toit
toit tool snapshot-to-image -m32 --format=binary -o chatty.bin chatty.snapshot
```

- [ ] **Step 5: Drive a programming session (2nd terminal, same --db)**

Use the node's MAC id (e.g. `30aea41a6208`). Speed up the loop, turn console on, install chatty:

```bash
export TS=~/workspaceToit/sqlite/build/bin/toit-sqlite
cd /home/david/workspaceToit/porta/gateway
$TS run gateway.toit -- --db=/tmp/porta-fwkb.db device set-poll-interval -d <id> 30s
$TS run gateway.toit -- --db=/tmp/porta-fwkb.db device set-console -d <id> on
$TS run gateway.toit -- --db=/tmp/porta-fwkb.db container install chatty ../chatty.bin -d <id> --interval=30s
```

- [ ] **Step 6: Verify telemetry lands and `monitor` reads it**

After the node wakes, drains the commands, installs+starts chatty, observes, and flushes:

```bash
$TS run gateway.toit -- --db=/tmp/porta-fwkb.db monitor -d <id> --since=1h
```
Expected: rows for `counter=0.0 … 4.0` (metric) and `chatty: tick 0 … 4` (log). In `jag monitor`, look for `supervisor: shipped N telemetry entr(ies)`.

- [ ] **Step 7: Verify default-off is quiet**

```bash
$TS run gateway.toit -- --db=/tmp/porta-fwkb.db device set-console -d <id> off
```
After the next wake, confirm `monitor` shows no *new* rows (no `data?id=` WRQ when off), and `jag monitor` shows no "shipped" line. This proves the M1 path is unchanged when off.

- [ ] **Step 8: Commit the payload + record the result**

```bash
git add device/chatty.toit
git commit -m "test(device): chatty telemetry payload for M2 hardware verification"
```
Append a short "M2.1 hardware-verified on fwkb" note (date + what was observed) to `docs/specs/2026-05-24-m2-telemetry-design.md` and commit.

---

## Self-Review

**Spec coverage** (against `2026-05-24-m2-telemetry-design.md`):
- data_log table + insert/query/prune → Task B1. ✓
- `data?id=` WRQ JSONL ingest, truncation-tolerant, report stays lean → Task B2. ✓
- `gateway monitor` (range + follow + kind) → Task B3. ✓
- `set-console` flag, off by default, command-delivered → B4 (gateway) + C4 (device decode) + C5 (apply + gate flush). ✓
- Remoting container = services only, no socket, drained by supervisor → C1/C2/C3 (provider + buffer + codec) + C5 (spawn + drain + ship). Realized as a spawned process (own heap, crash-isolated) — faithful to "separate container, services only"; a separately-installed image is a later, zero-API-change option. ✓
- Transport stays in supervisor; telemetry ships via `GatewayClient.put` → C5 (reuses `WifiTransport`/`put`). ✓
- RAM-only buffer, ship before sleep → C1 (RAM ring) + C5 (flush after observe, before deep-sleep). ✓
- `TelemetryService` dumb; aggregation is the app's job → C3 records as given; chatty app emits directly. ✓
- Power-mode-agnostic → the flush path is identical regardless of what follows; deep-sleep is the only branch here (always-on is the lifecycle sibling spec). ✓
- M2.0 spike (print displacement), fallback proven → Phase A, with explicit `log` as the primary path. ✓
- M2.2 down-path (setpoints) → explicitly out of scope (separate plan). ✓

**Placeholder scan:** none — every code step shows complete code; hardware steps give exact commands + expected observations.

**Type consistency:** `insert-data`/`query-data` signatures match between B1 (definition) and B2/B3 (callers); `TelemetryService` indexes/methods match between C3 (definition) and C5/chatty/tests (callers); `monitor-line_` row keys match `query-data`'s row maps; `CONSOLE-KEY` and `set-console` `{"on":bool}` shape match across B4/C4/C5.

**Known minor (acceptable for M2, noted in spec):** `--follow` dedups by ts-window (`last+1`), so two rows sharing a ts at the polling boundary could be missed or repeated; the extra post-observe `data?id=` connection (only when console-forward is on) interacts with the tftp#5 TID-race, so verify at 30s+ poll.
