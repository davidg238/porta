# porta Go core — B3 telemetry plane (design)

**Status:** approved-pending-self-review, ready for implementation plan
**Sub-project:** B3 of the Go-mainline renovation (B1 shipped `@cf2e958`, B2 shipped `@ff415a2`)
**Charter (parity port):** the `data?id=` JSONL ingest (telemetry up-path), the
`set-console` verb (forwarding toggle), and the `monitor` CLI — at parity with
the reference impl in `examples/toit-gateway` (see prior Toit-side spec
`docs/specs/2026-05-24-m2-telemetry-design.md`).

This spec covers the **Go core** (`cmd/porta` + `internal/…`) only. Node
firmware lives in nodus and is unchanged; the parked Toit gateway in
`examples/toit-gateway` is the behavioral reference and stays at parity.

## 1. What B3 does *not* change

- **No schema migration.** The `data_log` table and its
  `idx_data_device_ts` index are already in `internal/store/store.go:52-63`
  (carried over with B1). B3 only starts *using* them.
- **No wire shape change.** Both `set-console` (§2.4) and the `data?id=`
  JSONL body (§6) are already pinned in `docs/PROTOCOL.md`. B3 is the Go
  implementation of the gateway side.
- **No node firmware change.** Telemetry up-path and the forwarding toggle
  are already shipped on the nodus side under the Toit gateway (M2.1
  hardware-verified on `fwkb` 2026-05-24). The Go gateway is the only thing
  being updated.

## 2. Wire contracts (canonical in `docs/PROTOCOL.md` §2.4, §6 — unchanged)

**`set-console` (down-path).** Flat top-level JSON:
```json
{"verb":"set-console","on":true}
```
`on` is the boolean toggle; node persists it as the forwarding flag (default
`false` if absent at read time).

**`data?id=<mac>` (up-path).** WRQ body is **JSONL** — one JSON object per
line. Each line is one telemetry entry:

```json
{"ts":1716500000,"seq":0,"kind":"metric","name":"pm","value":13}
{"ts":1716500001,"seq":1,"kind":"log","text":"supervisor: started blink"}
```

Per `PROTOCOL.md` §6, all keys are optional with the following defaults:

| Key | Default at gateway | Notes |
|-----|--------------------|-------|
| `kind` | `"log"` | `"log"` or `"metric"`. |
| `name` | `null` | metric/series name. |
| `value` | `null` | int / float / bool / string scalar. |
| `text` | `null` | log line (or string-valued reading; see §5.1). |
| `ts` | gateway-receive epoch s | timestamp. |
| `seq` | line index | within-batch ordering. |

A line that fails to JSON-decode (e.g. a truncated final line) is skipped;
the rest of the batch is unaffected. A line that decodes to a non-object is
also skipped (graceful tolerance).

## 3. Architecture: package layout

The pure telemetry logic lives in a **new `internal/telemetry` package**
(same shape as B2's `internal/config`):

```
internal/
  command/    verbs + wire encoding             (no new deps)
  store/      sqlite data layer                  + InsertData/QueryData/PruneData
  config/     (B2) pure reconcile logic          unchanged
  telemetry/  NEW — pure JSONL parse + monitor formatter
  handler/    TFTP dispatch                      + data?id= WRQ branch
  portacli/   cobra CLI                          + set-console + monitor
```

Rationale: parsing one JSONL line and formatting one `data_log` row are pure
functions with no DB / no socket dependencies. Putting them in a small package
mirrors `internal/config` and makes them trivially unit-testable. The handler
imports `telemetry` and calls `ParseLine` in a loop; the `monitor` CLI imports
`telemetry` and calls `FormatLine` per row.

## 4. New code surface

### 4.1 `internal/store/data.go` — NEW file

Three methods plus a row type, separate file to keep `store.go` focused:

```go
// DataRow is one row from data_log; Value's runtime type matches the
// declared ValueType ("int"→int64, "float"→float64, "bool"→int64 0/1,
// "string"/log→Value==nil and Text holds the payload). NULL columns
// surface as zero-valued sql.Null* / "" / nil.
type DataRow struct {
    TS        int64
    Seq       int64
    Kind      string  // "log" or "metric"
    Name      string  // metric name, "" for log
    Value     any     // int64, float64, or nil
    Text      string  // log text or string-valued metric, "" otherwise
    ValueType string  // "int"|"float"|"bool"|"string"; "" for log or degraded
}

// InsertData appends one telemetry entry. value's runtime type drives the
// binding: int64 → INTEGER storage; float64 → REAL; nil → NULL.
func (s *Store) InsertData(deviceID string, ts, seq int64, kind, name string,
    value any, text, valueType string) error

// QueryData returns rows for deviceID with since ≤ ts ≤ until, ordered by
// (ts, seq). If kind is non-empty, filters to that kind.
func (s *Store) QueryData(deviceID string, since, until int64, kind string) ([]DataRow, error)

// PruneData deletes rows with ts < cutoff.
func (s *Store) PruneData(cutoff int64) error
```

**NUMERIC affinity gotcha (intentionally preserved):** when the value is a
whole-number float (e.g. `13.0`), SQLite NUMERIC stores it as INTEGER —
so `QueryData` reads it back as `int64(13)`. `value_type` is still `"float"`,
and `telemetry.FormatLine` renders by `value_type` (so a `"float"` always
prints with a decimal point). Parity with the Toit reference (see
`examples/toit-gateway/data_log_test.toit:44-50`).

### 4.2 `internal/telemetry/` — NEW package

Two pure files, ≤120 LoC each.

**`parse.go`** — `Entry` type + `ParseLine`:

```go
// Entry is a parsed JSONL line ready for Store.InsertData.
type Entry struct {
    TS        int64  // 0 if absent — caller substitutes receive time
    HasTS     bool   // distinguishes "absent" from "explicitly zero"
    Seq       int64  // 0 if absent — caller substitutes line index
    HasSeq    bool
    Kind      string // "log" if absent
    Name      string // "" if absent
    Value     any    // int64 / float64 / nil (the bound DB value)
    Text      string // "" if absent
    ValueType string // "int"|"float"|"bool"|"string"|""
}

// ParseLine decodes one JSONL line. ok==false means: blank line, JSON
// decode failure, or non-object — caller skips.
func ParseLine(line []byte) (e Entry, ok bool)
```

Value-type inference (parity with `handler.toit:160-184`):
- raw is `bool` → `Value = int64(0|1)`, `ValueType = "bool"`.
- raw is `json.Number` and integer-shaped (no `.`/`e`/`E`) and parses as
  `int64` → `Value = int64(...)`, `ValueType = "int"`.
- raw is `json.Number` otherwise (or int parse failed) → parses as
  `float64`, `Value = float64(...)`, `ValueType = "float"`.
- raw is `string` → `Text = raw`, `Value = nil`, `ValueType = "string"`.
- raw is `null` / array / object → `Value = nil`, `ValueType = ""` (graceful
  degradation: the row still inserts; `monitor` renders `<name>=null`).

If the entry has a top-level `"text"` key and `value` is not a string, the
explicit `text` is used (logs and string-tagged metrics).

**`format.go`** — `FormatLine`:

```go
// FormatLine renders one DataRow for `porta monitor`.
//   log:    "<ts>  log     <text>"
//   metric: "<ts>  metric  <name>=<rendered>"
// rendered ∈ value_type:
//   "int"    → integer text (no decimal)
//   "float"  → strconv.FormatFloat(asFloat64, 'f', -1, 64) but always with a
//              decimal point ("13" → "13.0"); see NUMERIC gotcha above
//   "bool"   → "true" / "false" (from 0/1)
//   "string" → row.Text
//   "" / unknown → "null" (graceful degradation)
func FormatLine(row store.DataRow) string
```

Parity with `examples/toit-gateway/gateway.toit:215-225` and
`examples/toit-gateway/monitor_test.toit`.

### 4.3 `internal/command/command.go` — `SetConsole` constructor

Add one constructor alongside `Run`/`Stop`/`SetPollInterval`/`Set`:

```go
// SetConsole builds the telemetry-forwarding toggle command.
func SetConsole(on bool) Command
```

Marshals `{"on":true}` (or `{"on":false}`). Existing `EncodeWire` splices
`"verb":"set-console"` in.

### 4.4 `internal/handler/handler.go` — `data?id=` WRQ branch

Two small changes; no new file.

- `AcceptWrite` accepts `base == "data"` (with `id` required) in addition to
  `report`.
- `Write` dispatches on `base`: existing path for `report`, new `writeData`
  for `data`.

```go
// writeData ingests a JSONL telemetry body: parse each line, infer
// value_type, and InsertData per entry. A line that fails to decode (e.g. a
// truncated final line) is skipped — the rest of the batch is unaffected
// (parity with examples/toit-gateway/handler.toit's DataWriter_).
func (h *Handler) writeData(id, peer string, data []byte) error {
    if err := h.store.TouchNode(id, peer, h.now()); err != nil {
        return err
    }
    now := h.now()
    for i, raw := range bytes.Split(data, []byte("\n")) {
        line := bytes.TrimSpace(raw)
        if len(line) == 0 {
            continue
        }
        e, ok := telemetry.ParseLine(line)
        if !ok {
            continue
        }
        ts := e.TS
        if !e.HasTS {
            ts = now
        }
        seq := e.Seq
        if !e.HasSeq {
            seq = int64(i)
        }
        kind := e.Kind
        if kind == "" {
            kind = "log"
        }
        if err := h.store.InsertData(id, ts, seq, kind, e.Name, e.Value, e.Text, e.ValueType); err != nil {
            h.log("porta: data ingest insert error for %s: %v", id, err)
            // Keep ingesting the rest — one bad row shouldn't drop the batch.
            continue
        }
    }
    return nil
}
```

Note: B1's `Write` returned `access denied` for everything except `report?id=`.
The B3 change widens that to `report|data`, matching the new `AcceptWrite`.

### 4.5 `internal/portacli/mutate.go` — `device set-console`

```go
func runDeviceSetConsole(out io.Writer, st *store.Store, id, state string, now int64) error {
    var on bool
    switch state {
    case "on":
        on = true
    case "off":
        on = false
    default:
        return fmt.Errorf("set-console: state must be 'on' or 'off', got %q", state)
    }
    c := command.SetConsole(on)
    cmdID, err := st.EnqueueCommand(id, c.Verb, c.ArgsJSON, "cli", now)
    if err != nil {
        return err
    }
    fmt.Fprintf(out, "%s: enqueued set-console %s (command #%d)\n", id, state, cmdID)
    return nil
}

func newDeviceSetConsoleCmd() *cobra.Command { /* cobra wiring, ExactArgs(1) */ }
```

Wired into `newDeviceCmd()` in `inspect.go` alongside the other `device set-*`
subcommands.

### 4.6 `internal/portacli/monitor.go` — `monitor` top-level CLI

```go
// runMonitor is the testable core. since is the look-back in seconds (default
// 3600). kind filters to "log" or "metric" when non-empty. follow polls
// every pollInterval (typically 2s) until ctx is cancelled.
func runMonitor(ctx context.Context, out io.Writer, st *store.Store,
    id string, sinceS int64, kind string, follow bool,
    now func() int64, pollInterval time.Duration) error

func newMonitorCmd() *cobra.Command
```

Behavior (parity with `gateway.toit:413-435`):
1. Compute `until = now()`, `since = until - sinceS`.
2. `QueryData(id, since, until, kind)` → format each row → println.
3. If `--follow`: every `pollInterval`, requery with `since = last+1, until =
   now()`; print new rows; advance `last = until`. Ctx cancellation
   (`SIGINT` from cobra) exits cleanly.

Wired into `NewRootCmd()` in `root.go`.

## 5. Implementation details that matter

### 5.1 Value-type inference at parse time (Go-specific)

In Go, JSON `{"value":13}` decodes (with `UseNumber()`) to `json.Number("13")`;
`{"value":13.0}` decodes to `json.Number("13.0")`. To tag the value_type, we
check the canonical string for `.`/`e`/`E` (float-shaped) or rely on
`int64`-parse-succeeds-first then fall back to `float64`. Algorithm (in order):

1. raw is `bool` → bool branch.
2. raw is `json.Number`:
   - if `strings.ContainsAny(s, ".eE")` → float branch.
   - else parse int64; if success → int branch; else float64 → float branch.
3. raw is `string` → string branch.
4. raw is `nil`, array, or map → degraded (no value_type).

This matches the Toit reference (which uses `is int` / `is float` runtime
type tests on the natively-decoded numbers).

### 5.2 The NUMERIC-affinity round trip (intentionally preserved)

`InsertData(.. value=float64(13.0) ..)` is bound as REAL but stored as
INTEGER (SQLite NUMERIC affinity rule). `QueryData` reads it back as
`int64(13)` via `Scan(*any)`. `value_type` stays `"float"`, and
`FormatLine` renders with a decimal point. Tests must cover:
- write `int64(13)` + `value_type="int"` → read as `int64(13)`, format
  `"...metric  n=13"`.
- write `float64(13.0)` + `value_type="float"` → read as `int64(13)` (sic),
  format `"...metric  n=13.0"` (the decimal-point round-trip).
- write `float64(13.5)` + `value_type="float"` → read as `float64(13.5)`,
  format `"...metric  n=13.5"`.

### 5.3 Tolerance: truncated tail, non-object, non-scalar value

The handler skips, never errors, on:

- Blank lines (whitespace only).
- Lines that fail `json.Decode` into `map[string]any` (truncated tail,
  non-object root like `42`, malformed).
- Lines whose `"value"` is `null`, array, or object — the row inserts with
  `value=NULL, value_type=NULL` (graceful degradation; `monitor` renders
  `name=null`).

Failure of `InsertData` (an SQL error) is logged via `h.log` and the loop
continues — one bad row doesn't drop the batch.

### 5.4 `--follow` polling

Implemented in pure Go with `time.NewTicker`; respects `ctx.Done()`. The
boundary-row edge case from the Toit reference (rows sharing `ts` exactly at
the poll tick may be missed/repeated) is **accepted as-is**; documented in
the spec under §7.

### 5.5 Failure semantics on the WRQ path

A `data?id=` write that returns `nil` from `writeData` is reported as
success to the TFTP layer. A `data?id=` write that returns an error from
`TouchNode` (a real SQL failure) propagates as a TFTP error. The
per-line loop is best-effort; an individual `InsertData` error is logged
and the next line is attempted (parity with the Toit reference's
catch-and-skip behavior).

## 6. Testing strategy

### 6.1 Unit (new files)

- `internal/store/data_test.go` — insert/query/prune across all four
  scalar types and the log kind; the NUMERIC-affinity float round-trip;
  the `--kind` filter; the `since/until` window; prune cuts the right rows.
- `internal/telemetry/parse_test.go` — `ParseLine` per scalar type, plus
  truncated line, non-object line, null/array/object value, missing ts/seq
  defaults.
- `internal/telemetry/format_test.go` — `FormatLine` per scalar type and
  for log entries; the whole-number float decimal-point case; the degraded
  (`value_type == ""`) case rendering `null`.
- `internal/command/command_test.go` — `SetConsole(true)` / `SetConsole(false)`
  wire round-trip.

### 6.2 Integration (`internal/handler/handler_test.go`, extended)

- `TestWriteAcceptsDataAndIngestsJSONL` — POST a JSONL body with all five
  scalar lines (int, float, bool, string, log); assert five `data_log` rows
  with the expected `value_type` tags.
- `TestWriteDataTruncatedTailToleratesSkip` — append a half-written line at
  the end; assert N-1 rows ingested, no error.
- `TestWriteDataNonObjectLineSkipped` — middle line is `42`; assert the
  flanking rows ingest, no error.
- `TestWriteDataNonScalarValueDegrades` — `"value":[1,2]` line ingests
  with `value=NULL, value_type=NULL`.
- `TestWriteRejectsDataWithoutID` — `data` with no `?id=` is access-denied.
- `TestAcceptWriteRejectsUnknownBase` — neither `report` nor `data` → error
  (regression guard, keeps the existing TestWriteRejectsNonReportAndMissingID
  spirit).

### 6.3 CLI

- `internal/portacli/config_test.go` (extended) — `runDeviceSetConsole`
  enqueues `set-console` with `issued_by="cli"`; rejects unknown state.
- `internal/portacli/monitor_test.go` — `runMonitor` with a seeded store
  prints expected lines for each scalar type and for logs; `--kind` filter
  works; the `--follow` exit path on context cancel returns cleanly (use a
  short pollInterval and cancel after the initial print).

### 6.4 Acceptance gate (matches B1/B2's bar)

- `go build ./...`, `go vet ./...`, `go test -race ./...` all green.
- `examples/toit-gateway` build + host tests still green (parity reference
  must remain functional).
- Hardware checkpoint on the spare ESP32: install the `chatty` example,
  flip `device set-console on`, observe `data_log` fills and `monitor`
  prints typed lines; flip `off`, confirm row count holds.

## 7. Out of scope (explicit)

- **Live console attach.** Incompatible with deep-sleep; not present in the
  Toit reference either.
- **On-device aggregation.** Apps reduce (avg/min/max/downsample) before
  calling `TelemetryService.report`; the gateway records what it is given.
- **Automatic `data_log` retention.** `PruneData` is exposed but no daemon
  loop calls it — backlog item (deferred from the Toit reference too).
- **`unset` verb / forwarding-flag default.** Forwarding default is `off`
  (parity); operator must `set-console on` to start the stream.
- **Default-device ergonomic** (`device select <node>` + `$PORTA_DEVICE`).
  Still the agreed immediate follow-up after B3, cross-cutting every CLI
  command.
- **`--follow` boundary determinism.** Rows sharing the poll-tick `ts` may
  be missed or repeated; accepted known-minor (matches Toit reference).
- **Wider verb surface.** `set-uart-echo` (the device-side bench flag from
  M2's design) is **not** exposed via the gateway — it stays a device-side
  default-off constant per the original M2 spec.

## 8. Files added or changed

```
internal/store/data.go              NEW  (data_log methods + DataRow)
internal/store/data_test.go         NEW
internal/telemetry/                 NEW  package
  parse.go         + parse_test.go
  format.go        + format_test.go
internal/command/command.go         (+ SetConsole)
internal/command/command_test.go    (+ SetConsole tests)
internal/handler/handler.go         (AcceptWrite + writeData branch)
internal/handler/handler_test.go    (+ ingest scenarios)
internal/portacli/mutate.go         (+ runDeviceSetConsole + newDeviceSetConsoleCmd)
internal/portacli/inspect.go        (wire newDeviceSetConsoleCmd into newDeviceCmd)
internal/portacli/config_test.go    (+ set-console enqueue test)
internal/portacli/monitor.go        NEW  (runMonitor + newMonitorCmd)
internal/portacli/monitor_test.go   NEW
internal/portacli/root.go           (register newMonitorCmd)
docs/PROTOCOL.md                    (no change — already documents §2.4, §6)
```

Total: 7 new files, 6 modified files.

## 9. References

- `examples/toit-gateway/handler.toit:138-194` — `DataWriter_` (reference
  JSONL ingest with value-type inference).
- `examples/toit-gateway/store.toit:200-223` — `insert-data` / `query-data`
  / `prune-data` (reference store API).
- `examples/toit-gateway/gateway.toit:215-225, 413-435` — `monitor-line_`
  formatter and `cmd-monitor` (reference monitor CLI).
- `examples/toit-gateway/command.toit` — `Command.set-console` constructor
  + `VERB-SET-CONSOLE`.
- `examples/toit-gateway/{data_log_test,data_ingest_test,monitor_test,set_console_test}.toit`
  — reference test suites (parity benchmarks).
- `docs/specs/2026-05-24-m2-telemetry-design.md` — original (Toit-side)
  telemetry design + addendum on typed metric values.
- `docs/PROTOCOL.md` §2.4 (`set-console`), §6 (`data?id=` JSONL).
- `docs/specs/2026-05-27-porta-go-config-plane-design.md` — sibling B2
  spec; this spec follows the same shape.

## 10. Open questions

None blocking. The §7 known-minor items (follow boundary, retention loop)
are tracked as backlog and do not gate B3.
