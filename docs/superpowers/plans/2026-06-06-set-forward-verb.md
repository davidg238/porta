# set-forward verb + enriched telemetry entries (porta side) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace porta's `set-console` toggle with the ratified per-stream `set-forward` policy verb, and enrich telemetry ingest/display so log lines carry a `level` and prints are a distinct `kind:"print"`.

**Architecture:** Hard cutover — `set-console` is deleted everywhere (command/control/apisrv/CLI), not translated. A new declarative `set-forward` verb carries three nested per-stream policy objects (`print`/`log`/`telemetry`) on the wire. The telemetry plane gains a persisted `level` column on `data_log` (render-only is impossible because `monitor` formats from the stored `DataRow`, not the live JSONL) and recognises `kind:"print"`. All changes are additive at the storage layer except the `set-console` removal.

**Tech Stack:** Go 1.x, sqlite (mattn/go-sqlite3), cobra CLI, stdlib `net/http` + `encoding/json`. Test with `go test ./...`.

**Ratification source:** `~/workspaceToit/nodus/docs/porta-coordination-set-forward.md` §6 (committed nodus@5953a97). Decisions: hard cut · stream-as-`kind` (`kind:"print"` + optional `level`) · persist `level` · CLI requires all three stream flags · defer `every_s` CLI (wire field reserved).

**Wire shape (node-facing, flattened by `EncodeWire`):**
```json
{"verb":"set-forward","print":{"on":false},"log":{"on":true,"level":"warn"},"telemetry":{"on":true}}
```
**Enriched telemetry entries (node → gateway, JSONL):**
```json
{"kind":"print","text":"raw print output"}
{"kind":"log","level":"warn","text":"pump stalled"}
{"kind":"metric","name":"pm2_5","value":12}
{"kind":"panic","text":"<base64 trace blob>"}
```

---

## File Structure

- `internal/store/store.go` — add `level TEXT` to the `data_log` CREATE.
- `internal/store/data.go` — `DataRow.Level`; thread `level` through `InsertData` + the 4 SELECT/Scan sites (`QueryDataLimited`, `QueryDataAfter`, `RecentData`, `RecentMetrics`).
- `internal/telemetry/parse.go` — `Entry.Level`; read `raw["level"]`. (`kind:"print"` already parses cleanly via the no-`value` path.)
- `internal/handler/handler.go` — pass `e.Level` into `InsertData`.
- `internal/telemetry/format.go` — render `log [level]` and `print`.
- `internal/apisrv/telemetry.go` — `telemetryRow.Level` + copy it through.
- `devsdk/apiclient/client.go` — `DataRow.Level` + `wireRow.level` + copy it through.
- `internal/portacli/monitor.go` — carry `Level` in `toStoreRow`.
- `internal/command/command.go` — add `StreamPolicy`/`ForwardPolicy`/`SetForward`; delete `SetConsole`.
- `internal/control/control.go` — add `SetForward`; delete `SetConsole`.
- `internal/apisrv/commands.go` — add `case "set-forward"`; delete `case "set-console"`.
- `internal/portacli/mutate.go` + `inspect.go` — add `device set-forward` (all-3-flags); delete `set-console` command + core.
- `internal/portacli/*_test.go` — replace `set-console` references.
- `docs/PROTOCOL.md` — §2 verb table, §2.4, §6, §7.

Build order is data-layer-first so each task compiles against committed predecessors.

---

## Task 1: Persist `level` on `data_log` (store)

**Files:**
- Modify: `internal/store/store.go:59-69` (data_log CREATE)
- Modify: `internal/store/data.go` (`DataRow`, `InsertData`, `QueryDataLimited`, `QueryDataAfter`, `RecentData`, `RecentMetrics`)
- Test: `internal/store/data_test.go` (add a test; create if absent)

- [ ] **Step 1: Write the failing test**

Add to `internal/store/data_test.go` (match the existing test's store-open helper; if the file doesn't exist, mirror the open pattern from another `internal/store/*_test.go`):

```go
func TestInsertDataPersistsLevel(t *testing.T) {
	st := openTestStore(t) // reuse the package's existing helper
	if err := st.InsertData("aabbccddeeff", 100, 0, "log", "", nil, "pump stalled", "", "warn"); err != nil {
		t.Fatal(err)
	}
	rows, err := st.QueryDataLimited("aabbccddeeff", 0, 0, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	if rows[0].Level != "warn" {
		t.Fatalf("want level=warn, got %q", rows[0].Level)
	}
}
```

If the package has no `openTestStore` helper, use the same construction the other store tests use (e.g. `store.Open(filepath.Join(t.TempDir(), "t.db"))`) inline.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestInsertDataPersistsLevel`
Expected: FAIL — `InsertData` takes 8 args not 9 / `rows[0].Level` undefined.

- [ ] **Step 3: Implement**

In `internal/store/store.go`, add the column to the `data_log` CREATE (after `value_type TEXT`):

```sql
CREATE TABLE IF NOT EXISTS data_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  device_id TEXT,
  ts INTEGER,
  seq INTEGER,
  kind TEXT,
  name TEXT,
  value NUMERIC,
  text TEXT,
  value_type TEXT,
  level TEXT
);
```

In `internal/store/data.go`, add the field to `DataRow` (after `ValueType`):

```go
	ValueType string
	Level     string // log stream only ("trace".."fatal"); "" otherwise
```

Update `InsertData` to take and bind `level`:

```go
func (s *Store) InsertData(deviceID string, ts, seq int64, kind, name string, value any, text, valueType, level string) error {
	_, err := s.db.Exec(
		`INSERT INTO data_log (device_id, ts, seq, kind, name, value, text, value_type, level)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		deviceID, ts, seq, kind,
		nullStr(name), value, nullStr(text), nullStr(valueType), nullStr(level),
	)
	return err
}
```

In all four readers, add `COALESCE(level,'')` to the SELECT column list (as the last column) and `&r.Level` as the last Scan target:

- `QueryDataLimited` (line ~68 SELECT, ~93 Scan)
- `QueryDataAfter` (line ~108 SELECT, ~129 Scan)
- `RecentData` (line ~172 SELECT, ~182 Scan)
- `RecentMetrics` (line ~202 SELECT, ~220 Scan)

Example for `QueryDataLimited`:

```go
	q := `SELECT id, ts, seq, COALESCE(kind,''), COALESCE(name,''), value, COALESCE(text,''), COALESCE(value_type,''), COALESCE(level,'')
		  FROM data_log WHERE device_id = ? AND ts >= ?`
```
```go
		if err := rows.Scan(&r.ID, &r.TS, &r.Seq, &r.Kind, &r.Name, &v, &r.Text, &r.ValueType, &r.Level); err != nil {
```

`RecentData` and `RecentMetrics` don't select `id`, so add `COALESCE(level,'')` as the last column and `&r.Level` as the last Scan target there too.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/store/...`
Expected: PASS. (The whole package must compile — `InsertData` callers elsewhere break until Task 3; that's fine for *this* package's tests.)

- [ ] **Step 5: Commit**

```bash
git add internal/store/
git commit -m "feat(store): persist optional log level on data_log"
```

---

## Task 2: Parse `level` and `kind:"print"` (telemetry parse)

**Files:**
- Modify: `internal/telemetry/parse.go` (`Entry`, `ParseLine`)
- Test: `internal/telemetry/parse_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/telemetry/parse_test.go`:

```go
func TestParseLineLevelAndPrint(t *testing.T) {
	e, ok := telemetry.ParseLine([]byte(`{"kind":"log","level":"warn","text":"pump stalled"}`))
	if !ok || e.Kind != "log" || e.Level != "warn" || e.Text != "pump stalled" {
		t.Fatalf("log entry: ok=%v %+v", ok, e)
	}
	p, ok := telemetry.ParseLine([]byte(`{"kind":"print","text":"raw"}`))
	if !ok || p.Kind != "print" || p.Text != "raw" || p.Level != "" {
		t.Fatalf("print entry: ok=%v %+v", ok, p)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/telemetry/ -run TestParseLineLevelAndPrint`
Expected: FAIL — `e.Level` undefined.

- [ ] **Step 3: Implement**

In `internal/telemetry/parse.go`, add to `Entry` (after `ValueType`):

```go
	ValueType string // "int" | "float" | "bool" | "string" | ""
	Level     string // log stream only; "" when absent
```

In `ParseLine`, after the `text` extraction block, add:

```go
	if v, ok := raw["level"].(string); ok {
		e.Level = v
	}
```

(`kind:"print"` needs no special case — it has no `value`, so `classifyValue` leaves `Value=nil`/`ValueType=""`, exactly like a log line.)

- [ ] **Step 4: Run tests**

Run: `go test ./internal/telemetry/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/telemetry/parse.go internal/telemetry/parse_test.go
git commit -m "feat(telemetry): parse optional level field on entries"
```

---

## Task 3: Ingest level into the store (handler)

**Files:**
- Modify: `internal/handler/handler.go:218` (reset InsertData) and `:265` (telemetry InsertData)
- Test: existing `internal/handler/*_test.go` must still pass (compilation is the gate)

- [ ] **Step 1: Update the two InsertData call sites**

`InsertData` now takes a trailing `level` arg. The reset path (line ~218) has no level — pass `""`:

```go
			if err := h.store.InsertData(id, h.now(), 0, "reset", reset, v, reset, vtype, ""); err != nil {
```

The telemetry path (line ~265) passes the parsed level:

```go
		if err := h.store.InsertData(id, ts, seq, kind, e.Name, e.Value, e.Text, e.ValueType, e.Level); err != nil {
```

- [ ] **Step 2: Build the whole module**

Run: `go build ./...`
Expected: success — all `InsertData` callers now match the 9-arg signature.

- [ ] **Step 3: Run handler tests**

Run: `go test ./internal/handler/...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/handler/handler.go
git commit -m "feat(handler): pass parsed level through telemetry ingest"
```

---

## Task 4: Render `print` and `log [level]` (telemetry format)

**Files:**
- Modify: `internal/telemetry/format.go:14-22` (`FormatLine`)
- Test: `internal/telemetry/format_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/telemetry/format_test.go`:

```go
func TestFormatLinePrintAndLevel(t *testing.T) {
	cases := []struct {
		row  store.DataRow
		want string
	}{
		{store.DataRow{TS: 5, Kind: "print", Text: "raw"}, "5  print   raw"},
		{store.DataRow{TS: 5, Kind: "log", Level: "warn", Text: "stall"}, "5  log     [warn] stall"},
		{store.DataRow{TS: 5, Kind: "log", Text: "plain"}, "5  log     plain"},
	}
	for _, c := range cases {
		if got := telemetry.FormatLine(c.row); got != c.want {
			t.Errorf("FormatLine(%+v) = %q, want %q", c.row, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/telemetry/ -run TestFormatLinePrintAndLevel`
Expected: FAIL — `print` renders under the `log` branch; no level bracket.

- [ ] **Step 3: Implement**

Replace `FormatLine` in `internal/telemetry/format.go`:

```go
// FormatLine renders one data_log row for `porta monitor`. metric rows render
// "<name>=<value>"; print rows render under a "print" column; log rows render
// under "log" with an optional "[level]" prefix on the text. Old rows (no
// level, kind "" → treated as log) render exactly as before.
func FormatLine(r store.DataRow) string {
	switch r.Kind {
	case "metric":
		return fmt.Sprintf("%d  metric  %s=%s", r.TS, r.Name, renderMetric(r))
	case "print":
		return fmt.Sprintf("%d  print   %s", r.TS, r.Text)
	default: // "log", "panic", "reset", "" — text-bearing
		col := r.Kind
		if col == "" {
			col = "log"
		}
		text := r.Text
		if r.Level != "" {
			text = "[" + r.Level + "] " + text
		}
		return fmt.Sprintf("%d  %-7s %s", r.TS, col, text)
	}
}
```

Note: `%-7s` reproduces the existing `"log     "` (log + padding to width 7 + the trailing space in the format string) so `kind:"log"` with no level is byte-identical to today. Verify against the test's expected strings; adjust padding if the test fails on whitespace.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/telemetry/`
Expected: PASS. If a pre-existing format test for plain `log`/`panic` rows now fails on spacing, reconcile the expected string — the goal is byte-identical output for the old `log`/`panic` cases.

- [ ] **Step 5: Commit**

```bash
git add internal/telemetry/format.go internal/telemetry/format_test.go
git commit -m "feat(telemetry): render print kind and log [level] in monitor"
```

---

## Task 5: Carry `level` over the API (apisrv + apiclient + monitor)

**Files:**
- Modify: `internal/apisrv/telemetry.go:16-25` (`telemetryRow`) + `:85-91` (row build)
- Modify: `devsdk/apiclient/client.go:213-235` (`DataRow`, `wireRow`) + `:318-321` (build)
- Modify: `internal/portacli/monitor.go:33-39` (`toStoreRow`)
- Test: `internal/apisrv/telemetry_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/apisrv/telemetry_test.go` (mirror the existing telemetry test's setup that inserts a row and GETs it; insert a log row with a level and assert the JSON carries it):

```go
func TestTelemetryRowCarriesLevel(t *testing.T) {
	srv, st := newTestServer(t) // reuse the package's existing harness
	_ = st.InsertData("aabbccddeeff", 10, 0, "log", "", nil, "stall", "", "warn")
	resp, err := http.Get(srv.URL + "/api/nodes/aabbccddeeff/telemetry?since=0")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"level":"warn"`) {
		t.Fatalf("response missing level: %s", body)
	}
}
```

Use the same server/store construction the other tests in this file use (`newTestServer` is illustrative — match the real helper name).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/apisrv/ -run TestTelemetryRowCarriesLevel`
Expected: FAIL — `level` not in the JSON.

- [ ] **Step 3: Implement**

`internal/apisrv/telemetry.go` — add to `telemetryRow`:

```go
	ValueType string `json:"value_type"`
	Level     string `json:"level"`
```

and to the row build loop:

```go
		out = append(out, telemetryRow{
			ID: dr.ID, TS: dr.TS, Seq: dr.Seq, Kind: dr.Kind,
			Name: dr.Name, Value: dr.Value, Text: dr.Text, ValueType: dr.ValueType,
			Level: dr.Level,
		})
```

`devsdk/apiclient/client.go` — add `Level string` to `DataRow` (after `ValueType`), add `Level string \`json:"level"\`` to `wireRow`, and copy it in `getTelemetry`'s build:

```go
		out = append(out, DataRow{
			ID: w.ID, TS: w.TS, Seq: w.Seq, Kind: w.Kind, Name: w.Name,
			Value: typedValue(w.ValueType, w.Value), Text: w.Text, ValueType: w.ValueType,
			Level: w.Level,
		})
```

`internal/portacli/monitor.go` — carry `Level` in `toStoreRow`:

```go
	return store.DataRow{
		TS: r.TS, Seq: r.Seq, Kind: r.Kind, Name: r.Name,
		Value: r.Value, Text: r.Text, ValueType: r.ValueType, Level: r.Level,
	}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/apisrv/... ./devsdk/... ./internal/portacli/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/apisrv/telemetry.go devsdk/apiclient/client.go internal/portacli/monitor.go internal/apisrv/telemetry_test.go
git commit -m "feat(api): carry log level through telemetry reads end to end"
```

---

## Task 6: `command.SetForward` + types; delete `SetConsole`

**Files:**
- Modify: `internal/command/command.go:90-96` (delete `SetConsole`; add types + `SetForward`)
- Test: `internal/command/command_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/command/command_test.go`:

```go
func TestSetForward(t *testing.T) {
	p := command.ForwardPolicy{
		Print:     &command.StreamPolicy{On: false},
		Log:       &command.StreamPolicy{On: true, Level: "warn"},
		Telemetry: &command.StreamPolicy{On: true},
	}
	c, err := command.SetForward(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Verb != "set-forward" {
		t.Fatalf("verb = %q", c.Verb)
	}
	if c.ArgsJSON != `{"print":{"on":false},"log":{"on":true,"level":"warn"},"telemetry":{"on":true}}` {
		t.Fatalf("args = %s", c.ArgsJSON)
	}
	// invalid level rejected
	if _, err := command.SetForward(command.ForwardPolicy{Log: &command.StreamPolicy{On: true, Level: "loud"}}); err == nil {
		t.Fatal("expected error for invalid level")
	}
	// flattened wire form
	wire := command.EncodeWire(c.Verb, c.ArgsJSON)
	if !strings.Contains(string(wire), `"verb":"set-forward"`) || !strings.Contains(string(wire), `"telemetry":{"on":true}`) {
		t.Fatalf("wire = %s", wire)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/command/ -run TestSetForward`
Expected: FAIL — `ForwardPolicy`/`SetForward` undefined.

- [ ] **Step 3: Implement**

In `internal/command/command.go`, delete `SetConsole` (lines 90-96) and add:

```go
// StreamPolicy is one northbound stream's forwarding policy. On is always
// emitted (explicit on/off). Level applies to the log stream only. EveryS is
// the reserved always-on per-stream cadence (no CLI surface yet — omitted when 0).
type StreamPolicy struct {
	On     bool   `json:"on"`
	Level  string `json:"level,omitempty"`
	EveryS int64  `json:"every_s,omitempty"`
}

// ForwardPolicy is the complete per-stream forwarding policy carried by
// set-forward. Absent streams are omitted from the wire; the node resolves an
// omitted stream to its default (off) — set-forward is absolute, not a patch.
type ForwardPolicy struct {
	Print     *StreamPolicy `json:"print,omitempty"`
	Log       *StreamPolicy `json:"log,omitempty"`
	Telemetry *StreamPolicy `json:"telemetry,omitempty"`
}

func validLogLevel(l string) bool {
	switch l {
	case "trace", "debug", "info", "warn", "error", "fatal":
		return true
	}
	return false
}

// SetForward builds a set-forward command from a complete forwarding policy.
// The optional log level is validated against the 6-term vocab; nested policy
// objects are spliced verbatim by EncodeWire so they reach the node intact.
func SetForward(p ForwardPolicy) (Command, error) {
	if p.Log != nil && p.Log.Level != "" && !validLogLevel(p.Log.Level) {
		return Command{}, fmt.Errorf("invalid log level %q (expected trace|debug|info|warn|error|fatal)", p.Log.Level)
	}
	b, err := json.Marshal(p)
	if err != nil {
		return Command{}, err
	}
	return Command{Verb: "set-forward", ArgsJSON: string(b)}, nil
}
```

Note: `json.Marshal(ForwardPolicy{...})` emits fields in struct order (print, log, telemetry); within `StreamPolicy`, `on` then `level` (when set) — matching the test's expected literal.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/command/`
Expected: PASS. (Any other package referencing `command.SetConsole` still breaks — fixed in Task 7.)

- [ ] **Step 5: Commit**

```bash
git add internal/command/command.go internal/command/command_test.go
git commit -m "feat(command): add set-forward verb + ForwardPolicy, drop set-console"
```

---

## Task 7: `control.SetForward` + apisrv dispatch; delete `set-console`

**Files:**
- Modify: `internal/control/control.go:35-39` (delete `SetConsole`; add `SetForward`)
- Modify: `internal/apisrv/commands.go:74-84` (delete `case "set-console"`; add `case "set-forward"`)
- Test: `internal/apisrv/commands_test.go` (or the file holding dispatch tests)

- [ ] **Step 1: Write the failing test**

Add to the apisrv dispatch test file (mirror its existing per-verb test that POSTs a command and inspects the queued row):

```go
func TestDispatchSetForward(t *testing.T) {
	srv, st := newTestServer(t) // reuse existing harness
	body := `{"verb":"set-forward","args":{"print":{"on":false},"log":{"on":true,"level":"warn"},"telemetry":{"on":true}}}`
	resp, err := http.Post(srv.URL+"/api/nodes/aabbccddeeff/commands", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	cmd := lastQueuedCommand(t, st, "aabbccddeeff") // reuse the harness's queue inspector
	if cmd.Verb != "set-forward" {
		t.Fatalf("verb = %q", cmd.Verb)
	}
	// invalid level → 400
	bad := `{"verb":"set-forward","args":{"log":{"on":true,"level":"loud"}}}`
	r2, _ := http.Post(srv.URL+"/api/nodes/aabbccddeeff/commands", "application/json", strings.NewReader(bad))
	defer r2.Body.Close()
	if r2.StatusCode != 400 {
		t.Fatalf("invalid level: want 400, got %d", r2.StatusCode)
	}
}
```

Match the real helper names used by the other dispatch tests in that file (`newTestServer`/`lastQueuedCommand` are illustrative).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/apisrv/ -run TestDispatchSetForward`
Expected: FAIL — unknown verb `set-forward` → 400 on the valid case.

- [ ] **Step 3: Implement**

`internal/control/control.go` — delete `SetConsole` (lines 35-39) and add:

```go
// SetForward enqueues a set-forward command carrying the node's complete
// per-stream forwarding policy.
func SetForward(st *store.Store, id string, p command.ForwardPolicy, issuedBy string, now int64) (int64, error) {
	c, err := command.SetForward(p)
	if err != nil {
		return 0, err
	}
	return st.EnqueueCommand(id, c.Verb, c.ArgsJSON, issuedBy, now)
}
```

`internal/apisrv/commands.go` — delete the `case "set-console":` block (lines 74-84) and add:

```go
	case "set-forward":
		var p command.ForwardPolicy
		if err := decodeArgs(req.Args, &p); err != nil {
			return 0, err
		}
		return control.SetForward(h.st, id, p, "api", now)
```

(`decodeArgs` uses `UseNumber`, which only affects decoding into `interface{}`; the typed `ForwardPolicy`/`StreamPolicy` fields decode normally, including `EveryS int64`. Level validation happens in `command.SetForward`.)

- [ ] **Step 4: Run tests**

Run: `go test ./internal/apisrv/... ./internal/control/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/control/control.go internal/apisrv/commands.go internal/apisrv/commands_test.go
git commit -m "feat(api): dispatch set-forward via control, drop set-console"
```

---

## Task 8: CLI `device set-forward` (all 3 flags); delete `set-console`

**Files:**
- Modify: `internal/portacli/mutate.go:32-41` (delete `runDeviceSetConsole`; add `runDeviceSetForward` + helpers) and `:258-271` (delete `newDeviceSetConsoleCmd`; add `newDeviceSetForwardCmd`)
- Modify: `internal/portacli/inspect.go:113` (swap `newDeviceSetConsoleCmd()` → `newDeviceSetForwardCmd()` in `AddCommand`)
- Modify tests: `internal/portacli/mutate_test.go:87-90`, `inspect_test.go:85,143-147`, `e2e_test.go:66`
- Test: `internal/portacli/mutate_test.go`

- [ ] **Step 1: Write the failing test**

Replace the `set-console` assertion in `internal/portacli/mutate_test.go` (around line 87-90) with a `set-forward` test. Mirror the existing test's fake-client/server harness:

```go
func TestRunDeviceSetForward(t *testing.T) {
	// reuse the existing test harness that captures the enqueued command
	out := &bytes.Buffer{}
	c := newFakeClient(t) // illustrative — match the real helper
	if err := runDeviceSetForward(out, c, "aabbccddeeff", false, true, true, "warn"); err != nil {
		t.Fatal(err)
	}
	cmd := c.lastCommand()
	if cmd == nil || cmd.Verb != "set-forward" {
		t.Fatalf("verb = %v", cmd)
	}
	if !strings.Contains(out.String(), "enqueued set-forward") ||
		!strings.Contains(out.String(), "print:off") ||
		!strings.Contains(out.String(), "log:on[warn]") ||
		!strings.Contains(out.String(), "telemetry:on") {
		t.Fatalf("output = %q", out.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/portacli/ -run TestRunDeviceSetForward`
Expected: FAIL — `runDeviceSetForward` undefined.

- [ ] **Step 3: Implement**

In `internal/portacli/mutate.go`, delete `runDeviceSetConsole` and add:

```go
func onOffStr(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func parseOnOff(s string) (bool, error) {
	switch s {
	case "on":
		return true, nil
	case "off":
		return false, nil
	}
	return false, fmt.Errorf("expected on or off, got %q", s)
}

// runDeviceSetForward enqueues a set-forward command carrying the complete
// per-stream policy. set-forward is absolute, so the CLI requires all three
// stream states explicitly (no silent off). The log level defaults to warn
// on the node when omitted.
func runDeviceSetForward(out io.Writer, c *apiclient.Client, sel string, print, log, telemetry bool, logLevel string) error {
	logPolicy := map[string]any{"on": log}
	if logLevel != "" {
		logPolicy["level"] = logLevel
	}
	args := map[string]any{
		"print":     map[string]any{"on": print},
		"log":       logPolicy,
		"telemetry": map[string]any{"on": telemetry},
	}
	cmdID, nodeID, err := c.Command(sel, "set-forward", args)
	if err != nil {
		return err
	}
	lvl := logLevel
	if lvl == "" {
		lvl = "warn"
	}
	fmt.Fprintf(out, "%s: enqueued set-forward (command #%d)\n  → print:%s  log:%s[%s]  telemetry:%s\n",
		nodeID, cmdID, onOffStr(print), onOffStr(log), lvl, onOffStr(telemetry))
	return nil
}
```

Replace `newDeviceSetConsoleCmd` with:

```go
func newDeviceSetForwardCmd() *cobra.Command {
	var device, printS, logS, telemetryS, logLevel string
	cmd := &cobra.Command{
		Use:   "set-forward",
		Short: "Set a node's per-stream forwarding policy (absolute — all streams required)",
		Long: "Set the complete per-stream forwarding policy. set-forward is absolute: " +
			"every stream you do not enable is turned OFF, so --print, --log and --telemetry " +
			"are all required.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			print, err := parseOnOff(printS)
			if err != nil {
				return fmt.Errorf("--print: %w", err)
			}
			log, err := parseOnOff(logS)
			if err != nil {
				return fmt.Errorf("--log: %w", err)
			}
			telemetry, err := parseOnOff(telemetryS)
			if err != nil {
				return fmt.Errorf("--telemetry: %w", err)
			}
			c := apiclient.New(serverURL())
			return runDeviceSetForward(cmd.OutOrStdout(), c, device, print, log, telemetry, logLevel)
		},
	}
	deviceFlag(cmd, &device)
	cmd.Flags().StringVar(&printS, "print", "", "forward print stream (on|off)")
	cmd.Flags().StringVar(&logS, "log", "", "forward log stream (on|off)")
	cmd.Flags().StringVar(&telemetryS, "telemetry", "", "forward telemetry/metric stream (on|off)")
	cmd.Flags().StringVar(&logLevel, "log-level", "", "minimum log level (trace|debug|info|warn|error|fatal; node default warn)")
	_ = cmd.MarkFlagRequired("print")
	_ = cmd.MarkFlagRequired("log")
	_ = cmd.MarkFlagRequired("telemetry")
	return cmd
}
```

In `internal/portacli/inspect.go:113`, swap the registration:

```go
		newDeviceSetForwardCmd(),
```

- [ ] **Step 4: Update the remaining test references**

- `e2e_test.go:66` — change `{"device", "set-console", "on", "-d", "aabbccddeeff", "--server", url}` to `{"device", "set-forward", "--print", "off", "--log", "on", "--telemetry", "on", "-d", "aabbccddeeff", "--server", url}` and update any assertion on the output/queued verb to `set-forward`.
- `inspect_test.go:85` — change `EnqueueCommand("aabbccddeeff", "set-console", `{"state":"on"}`, ...)` to `EnqueueCommand("aabbccddeeff", "set-forward", `{"telemetry":{"on":true}}`, ...)`.
- `inspect_test.go:143-147` — update the expected timeline line from `set-console` to `set-forward`.

- [ ] **Step 5: Run tests**

Run: `go test ./internal/portacli/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/portacli/
git commit -m "feat(cli): device set-forward (all stream flags required), drop set-console"
```

---

## Task 9: PROTOCOL.md — ratify the wire

**Files:**
- Modify: `docs/PROTOCOL.md` (§2 verb table line 81; §2.4 lines 149-160; §6 lines 410-448; §7 lines 457-471)

- [ ] **Step 1: Verb table (line 81)**

Replace:
```
| `set-console` | `VERB-SET-CONSOLE` |
```
with:
```
| `set-forward` | `VERB-SET-FORWARD` |
```

- [ ] **Step 2: §2.4 — replace the whole `set-console` subsection (lines 149-160)**

```markdown
### 2.4 `set-forward` — per-stream forwarding policy

A single **declarative, absolute** command carrying the node's complete northbound
forwarding policy. Each stream is an optional nested object; an omitted stream
resolves to its default (off) on the node — the command is the whole policy, not a patch.

| Key | Type | Required | Meaning |
|-----|------|----------|---------|
| `verb` | string | yes | `"set-forward"` |
| `print` | object | no | `{"on": bool, "every_s"?: int}` |
| `log` | object | no | `{"on": bool, "level"?: string, "every_s"?: int}` |
| `telemetry` | object | no | `{"on": bool, "every_s"?: int}` |

- `level` (log only) ∈ `trace|debug|info|warn|error|fatal`. Absent ⇒ node keeps `warn`.
- `every_s` (optional, all streams): the always-on per-stream forward interval.
  Ignored by deep-sleep nodes (cadence there is `set-poll-interval`). Absent ⇒ node
  coalesces with its report window. (Reserved; porta carries it but exposes no CLI flag yet.)

```json
{"verb": "set-forward", "print": {"on": false}, "log": {"on": true, "level": "warn"}, "telemetry": {"on": true}}
```

The node persists the resolved policy in its flash config (so it survives reboot).
FATAL-level logs and panics are delivered regardless of the gates.
```

- [ ] **Step 3: §6 — enrich the entry table + examples (lines 410-448)**

In the intro line, change "When console/telemetry forwarding is enabled (§2.4)" to
"When forwarding is enabled for a stream (§2.4)".

Change the `kind` row to note `print`:
```
| `kind` | string | no | `"log"` | Entry kind: `"print"`, `"log"`, `"metric"`, `"panic"`, or `"reset"`. |
```
Add a `level` row after `kind`:
```
| `level` | string | no | `null` | Log-stream severity (`trace`..`fatal`). Present on `"log"` entries only. |
```
Replace the examples block:
```json
{"kind": "print", "text": "raw print output"}
{"kind": "log", "level": "warn", "text": "pump stalled"}
{"kind": "metric", "name": "pm2_5", "value": 12}
{"kind": "panic", "text": "<base64 trace blob>"}
```
After the examples, add a sentence: "FATAL-level logs and `panic` entries are part of the
must-deliver subset — the node ships them even when the corresponding gate is off."

- [ ] **Step 4: §7 Conformance (lines 457-471)**

Replace the verb bullet:
```
- Honour the seven verbs (`run`, `stop`, `set-poll-interval`, `set-forward`,
  `set`, `set-power-mode`, `reboot`) with the arg schemas and defaults in §2,
```
Add (or fold into the telemetry bullet) a line:
```
- (If it forwards telemetry) forward print/log/telemetry per the resolved
  `set-forward` policy, tagging log entries with `level`, and deliver FATAL logs +
  `kind:"panic"` entries even when the relevant gate is off.
```

- [ ] **Step 5: Commit**

```bash
git add docs/PROTOCOL.md
git commit -m "docs(protocol): ratify set-forward verb + enriched telemetry entries"
```

---

## Final verification

- [ ] **Run the full suite**

Run: `go test ./...`
Expected: ALL PASS.

- [ ] **Grep for stragglers**

Run: `grep -rn "set-console\|SetConsole" --include=*.go .`
Expected: NO matches (hard cutover — none should remain).

- [ ] **Build the binary**

Run: `go build ./cmd/porta`
Expected: success.

- [ ] **Finish the branch** via superpowers:finishing-a-development-branch (merge `--no-ff` to master per porta convention).

---

## Self-Review notes

- **Spec coverage:** verb (T6/T7/T8) · enriched entries `kind:"print"`+`level` (T2/T4) · persist level (T1/T3/T5) · hard cut of set-console (T6/T7/T8 + final grep) · PROTOCOL.md §2/§2.4/§6/§7 (T9). All five ratified decisions covered.
- **Type consistency:** `InsertData(..., valueType, level string)` 9-arg signature is set in T1 and matched by every caller in T3; `DataRow.Level`/`Entry.Level`/`telemetryRow.Level`/`apiclient.DataRow.Level` all named `Level`; `ForwardPolicy`/`StreamPolicy` defined in T6 and reused verbatim in T7.
- **Deferred (not in scope):** `every_s` CLI surface (wire field reserved only); nodus device-side logic (separate repo).
- **DB note:** the `level` column is added to the CREATE only (`CREATE TABLE IF NOT EXISTS` won't alter an existing `data_log`). On the gw soak, the DB must be recreated to gain the column — consistent with porta's no-legacy/recreate pattern ([[porta-no-legacy]]). All readers `COALESCE(level,'')`, so a fresh DB and old code interoperate; only the new column needs the recreate.
