# porta Go Core — B3 Telemetry Plane Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Port the parked Toit gateway's telemetry plane (the `data?id=` JSONL ingest, the `set-console` toggle, the `monitor` CLI) into the Go core at parity, with no schema change.

**Architecture:** A new `internal/telemetry` package holds the pure JSONL parser and the `monitor` line formatter. `internal/store` grows `InsertData`/`QueryData`/`PruneData` against the already-present `data_log` table. `internal/handler` widens `AcceptWrite` to accept `data?id=` and `Write` dispatches to a new `writeData` branch. `internal/portacli` gains `device set-console` and a top-level `monitor` command.

**Tech Stack:** Go 1.21+, `database/sql`, `mattn/go-sqlite3` (already wired by B1), `encoding/json` (with `UseNumber()` for type-faithful number decode), `spf13/cobra` (already wired).

**Branch:** `feat/porta-go-telemetry-plane` (cut at the start; spec lives at `docs/specs/2026-05-28-porta-go-telemetry-plane-design.md`).

**Spec:** `docs/specs/2026-05-28-porta-go-telemetry-plane-design.md`

---

## File Structure

| Path | Kind | Responsibility |
|---|---|---|
| `internal/store/data.go` | N | `DataRow` type + `InsertData`/`QueryData`/`PruneData` |
| `internal/store/data_test.go` | N | data_log round-trip + NUMERIC-affinity gotcha |
| `internal/telemetry/parse.go` | N | `Entry` type + `ParseLine` (value_type inference) |
| `internal/telemetry/parse_test.go` | N | per-scalar + truncated/non-object/non-scalar tolerance |
| `internal/telemetry/format.go` | N | `FormatLine(store.DataRow) string` |
| `internal/telemetry/format_test.go` | N | per-scalar + log + degraded + whole-float-decimal |
| `internal/command/command.go` | M | `+ SetConsole` constructor |
| `internal/command/command_test.go` | M | `+ SetConsole` wire round-trip |
| `internal/handler/handler.go` | M | `AcceptWrite` accepts `data?id=`; `Write` dispatches; new `writeData` |
| `internal/handler/handler_test.go` | M | ingest, truncated-tail, non-object, non-scalar, reject-no-id |
| `internal/portacli/mutate.go` | M | `+ runDeviceSetConsole`, `+ newDeviceSetConsoleCmd` |
| `internal/portacli/inspect.go` | M | wire `newDeviceSetConsoleCmd` into `newDeviceCmd()` |
| `internal/portacli/config_test.go` | M | `+ device set-console` enqueue test |
| `internal/portacli/monitor.go` | N | `runMonitor` + `newMonitorCmd` |
| `internal/portacli/monitor_test.go` | N | range + kind filter + follow-cancel |
| `internal/portacli/root.go` | M | register `newMonitorCmd` |

Total: 7 new files, 6 modified files.

---

## Task 1: `store.InsertData` / `QueryData` / `PruneData` — the data_log methods

**Files:**
- Create: `internal/store/data.go`
- Test: `internal/store/data_test.go`

- [ ] **Step 1.1: Write the failing test**

```go
// internal/store/data_test.go
package store

import (
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(t.TempDir() + "/d.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestInsertAndQueryDataAllScalarTypes(t *testing.T) {
	st := openTestStore(t)
	dev := "aabbccddeeff"
	// Int.
	if err := st.InsertData(dev, 100, 0, "metric", "pm", int64(13), "", "int"); err != nil {
		t.Fatal(err)
	}
	// Float.
	if err := st.InsertData(dev, 101, 1, "metric", "t", float64(20.5), "", "float"); err != nil {
		t.Fatal(err)
	}
	// Bool (stored as 0/1 in value, type tag "bool").
	if err := st.InsertData(dev, 102, 2, "metric", "door", int64(1), "", "bool"); err != nil {
		t.Fatal(err)
	}
	// String (value=nil, text holds payload).
	if err := st.InsertData(dev, 103, 3, "metric", "mode", nil, "auto", "string"); err != nil {
		t.Fatal(err)
	}
	// Log (value=nil, text holds payload, value_type "").
	if err := st.InsertData(dev, 104, 4, "log", "", nil, "started blink", ""); err != nil {
		t.Fatal(err)
	}
	rows, err := st.QueryData(dev, 0, 200, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 5 {
		t.Fatalf("got %d rows, want 5", len(rows))
	}
	// int preserved (NUMERIC affinity stores integer as INTEGER storage class).
	if v, ok := rows[0].Value.(int64); !ok || v != 13 {
		t.Errorf("rows[0].Value = %v (%T), want int64(13)", rows[0].Value, rows[0].Value)
	}
	if rows[0].ValueType != "int" {
		t.Errorf("rows[0].ValueType = %q, want int", rows[0].ValueType)
	}
	// float preserved.
	if v, ok := rows[1].Value.(float64); !ok || v != 20.5 {
		t.Errorf("rows[1].Value = %v (%T), want float64(20.5)", rows[1].Value, rows[1].Value)
	}
	if rows[1].ValueType != "float" {
		t.Errorf("rows[1].ValueType = %q, want float", rows[1].ValueType)
	}
	// bool stored as 1 with tag.
	if v, ok := rows[2].Value.(int64); !ok || v != 1 {
		t.Errorf("rows[2].Value = %v (%T), want int64(1)", rows[2].Value, rows[2].Value)
	}
	if rows[2].ValueType != "bool" {
		t.Errorf("rows[2].ValueType = %q, want bool", rows[2].ValueType)
	}
	// string lives in text.
	if rows[3].Text != "auto" {
		t.Errorf("rows[3].Text = %q, want auto", rows[3].Text)
	}
	if rows[3].Value != nil {
		t.Errorf("rows[3].Value = %v, want nil", rows[3].Value)
	}
	if rows[3].ValueType != "string" {
		t.Errorf("rows[3].ValueType = %q, want string", rows[3].ValueType)
	}
	// log entry.
	if rows[4].Kind != "log" {
		t.Errorf("rows[4].Kind = %q, want log", rows[4].Kind)
	}
	if rows[4].Text != "started blink" {
		t.Errorf("rows[4].Text = %q, want started blink", rows[4].Text)
	}
	if rows[4].ValueType != "" {
		t.Errorf("rows[4].ValueType = %q, want \"\"", rows[4].ValueType)
	}
}

func TestQueryDataKindFilter(t *testing.T) {
	st := openTestStore(t)
	dev := "ffeeddccbbaa"
	st.InsertData(dev, 100, 0, "metric", "x", int64(1), "", "int")
	st.InsertData(dev, 101, 1, "log", "", nil, "hi", "")
	st.InsertData(dev, 102, 2, "metric", "y", int64(2), "", "int")
	if rows, _ := st.QueryData(dev, 0, 200, "metric"); len(rows) != 2 {
		t.Errorf("metric filter: got %d rows, want 2", len(rows))
	}
	if rows, _ := st.QueryData(dev, 0, 200, "log"); len(rows) != 1 {
		t.Errorf("log filter: got %d rows, want 1", len(rows))
	}
}

func TestQueryDataTimeWindow(t *testing.T) {
	st := openTestStore(t)
	dev := "112233445566"
	st.InsertData(dev, 100, 0, "metric", "x", int64(1), "", "int")
	st.InsertData(dev, 200, 1, "metric", "x", int64(2), "", "int")
	st.InsertData(dev, 300, 2, "metric", "x", int64(3), "", "int")
	if rows, _ := st.QueryData(dev, 150, 250, ""); len(rows) != 1 {
		t.Errorf("window 150..250: got %d rows, want 1", len(rows))
	}
	if rows, _ := st.QueryData(dev, 400, 500, ""); len(rows) != 0 {
		t.Errorf("window 400..500: got %d rows, want 0", len(rows))
	}
}

func TestPruneData(t *testing.T) {
	st := openTestStore(t)
	dev := "778899aabbcc"
	st.InsertData(dev, 100, 0, "metric", "x", int64(1), "", "int")
	st.InsertData(dev, 200, 1, "metric", "x", int64(2), "", "int")
	st.InsertData(dev, 300, 2, "metric", "x", int64(3), "", "int")
	if err := st.PruneData(200); err != nil {
		t.Fatal(err)
	}
	rows, _ := st.QueryData(dev, 0, 400, "")
	if len(rows) != 2 {
		t.Errorf("after prune: got %d rows, want 2 (ts<200 removed)", len(rows))
	}
}

// TestNumericAffinityWholeNumberFloat pins the SQLite quirk preserved end-to-
// end: a float64(13.0) bound to a NUMERIC column is stored as INTEGER (13);
// QueryData reads it back as int64(13). value_type stays "float", so the
// FormatLine renderer (Task 3) puts the decimal point back.
func TestNumericAffinityWholeNumberFloat(t *testing.T) {
	st := openTestStore(t)
	dev := "ddccbbaa9988"
	if err := st.InsertData(dev, 10, 0, "metric", "w", float64(13.0), "", "float"); err != nil {
		t.Fatal(err)
	}
	rows, _ := st.QueryData(dev, 0, 100, "")
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].ValueType != "float" {
		t.Errorf("ValueType = %q, want float", rows[0].ValueType)
	}
	// NUMERIC dropped the .0 → int64 storage class.
	if v, ok := rows[0].Value.(int64); !ok || v != 13 {
		t.Errorf("Value = %v (%T), want int64(13) (NUMERIC affinity gotcha)", rows[0].Value, rows[0].Value)
	}
}
```

- [ ] **Step 1.2: Run the test, see it fail**

Run: `go test ./internal/store/ -run "TestInsertAndQueryData|TestQueryData|TestPruneData|TestNumericAffinity"`
Expected: FAIL with `undefined: InsertData`, etc.

- [ ] **Step 1.3: Implement `internal/store/data.go`**

```go
// internal/store/data.go
package store

import "strconv"

// DataRow is one row from data_log. Value's runtime type matches the
// declared ValueType:
//   "int"    → int64
//   "float"  → float64 (with the NUMERIC-affinity caveat: a whole-number
//              float stores as INTEGER, so reads back as int64 — the
//              formatter renders by ValueType, putting the decimal back)
//   "bool"   → int64 (0 or 1)
//   "string" → Value == nil; Text holds the payload
//   ""       → log row (Value == nil; Text holds the line)
type DataRow struct {
	TS        int64
	Seq       int64
	Kind      string
	Name      string
	Value     any
	Text      string
	ValueType string
}

// InsertData appends one telemetry entry. value's runtime type drives the
// SQL binding: int64 → INTEGER, float64 → REAL, nil → NULL. Empty strings
// for name / text / valueType are bound as NULL.
func (s *Store) InsertData(deviceID string, ts, seq int64, kind, name string, value any, text, valueType string) error {
	_, err := s.db.Exec(
		`INSERT INTO data_log (device_id, ts, seq, kind, name, value, text, value_type)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		deviceID, ts, seq, kind,
		nullStr(name), value, nullStr(text), nullStr(valueType),
	)
	return err
}

// QueryData returns the device's rows with since <= ts <= until, ordered by
// (ts, seq). When kind is non-empty, restricts to that kind. value_type ==
// "" surfaces as the empty string (a log row or a degraded metric).
//
// The value column is NUMERIC; Scan into *any returns the SQLite storage
// class directly: INTEGER → int64, REAL → float64, NULL → nil. The driver
// can also return []byte for some edge cases (e.g. a numeric out of int64
// range stored textually) — normalizeNumeric handles that fallback.
func (s *Store) QueryData(deviceID string, since, until int64, kind string) ([]DataRow, error) {
	q := `SELECT ts, seq, COALESCE(kind,''), COALESCE(name,''), value, COALESCE(text,''), COALESCE(value_type,'')
		  FROM data_log WHERE device_id = ? AND ts >= ? AND ts <= ?`
	args := []any{deviceID, since, until}
	if kind != "" {
		q += ` AND kind = ?`
		args = append(args, kind)
	}
	q += ` ORDER BY ts, seq`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DataRow
	for rows.Next() {
		var r DataRow
		var v any
		if err := rows.Scan(&r.TS, &r.Seq, &r.Kind, &r.Name, &v, &r.Text, &r.ValueType); err != nil {
			return nil, err
		}
		r.Value = normalizeNumeric(v)
		out = append(out, r)
	}
	return out, rows.Err()
}

// normalizeNumeric coerces the result of Scan(*any) on a NUMERIC column.
// go-sqlite3 returns int64 / float64 / nil for the common cases; []byte is
// possible for textually-stored numerics (rare here since our binds are
// always int64/float64/nil) and gets reparsed.
func normalizeNumeric(v any) any {
	switch x := v.(type) {
	case nil, int64, float64:
		return x
	case []byte:
		s := string(x)
		if s == "" {
			return nil
		}
		for i := 0; i < len(s); i++ {
			c := s[i]
			if c == '.' || c == 'e' || c == 'E' {
				if f, err := strconv.ParseFloat(s, 64); err == nil {
					return f
				}
				return nil
			}
		}
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n
		}
		return nil
	default:
		return v
	}
}

// PruneData deletes data_log rows with ts < cutoff (epoch seconds).
func (s *Store) PruneData(cutoff int64) error {
	_, err := s.db.Exec(`DELETE FROM data_log WHERE ts < ?`, cutoff)
	return err
}
```

- [ ] **Step 1.4: Run the tests, see them pass**

Run: `go test ./internal/store/ -v`
Expected: all existing store tests still pass; the 5 new tests pass.

- [ ] **Step 1.5: Commit**

```bash
git add internal/store/data.go internal/store/data_test.go
git commit -m "$(cat <<'EOF'
feat(porta): store — InsertData / QueryData / PruneData for data_log

The data_log table + idx_data_device_ts index were already in the B1
schema; B3 starts using them. InsertData binds the JSON-decoded scalar
verbatim (int64 → INTEGER, float64 → REAL, nil → NULL); QueryData scans
through sql.RawBytes and decides int64 vs float64 from the raw storage
class so the NUMERIC-affinity quirk is preserved end-to-end (a whole-
number float comes back as int64 — value_type "float" carries the
semantic and the formatter renders the decimal). Mirrors
examples/toit-gateway/store.toit:200-223.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: `telemetry.ParseLine` — JSONL parse + value_type inference

**Files:**
- Create: `internal/telemetry/parse.go`
- Test: `internal/telemetry/parse_test.go`

- [ ] **Step 2.1: Write the failing test**

```go
// internal/telemetry/parse_test.go
package telemetry

import "testing"

func TestParseLineMetricInt(t *testing.T) {
	e, ok := ParseLine([]byte(`{"ts":100,"seq":0,"kind":"metric","name":"pm","value":13}`))
	if !ok {
		t.Fatal("ok=false, want true")
	}
	if !e.HasTS || e.TS != 100 {
		t.Errorf("TS=%d hasTS=%v, want 100 true", e.TS, e.HasTS)
	}
	if !e.HasSeq || e.Seq != 0 {
		t.Errorf("Seq=%d hasSeq=%v, want 0 true", e.Seq, e.HasSeq)
	}
	if e.Kind != "metric" || e.Name != "pm" {
		t.Errorf("Kind=%q Name=%q", e.Kind, e.Name)
	}
	v, ok := e.Value.(int64)
	if !ok || v != 13 {
		t.Errorf("Value=%v (%T), want int64(13)", e.Value, e.Value)
	}
	if e.ValueType != "int" {
		t.Errorf("ValueType=%q, want int", e.ValueType)
	}
}

func TestParseLineMetricFloat(t *testing.T) {
	e, ok := ParseLine([]byte(`{"ts":101,"kind":"metric","name":"t","value":20.5}`))
	if !ok {
		t.Fatal("ok=false")
	}
	v, ok := e.Value.(float64)
	if !ok || v != 20.5 {
		t.Errorf("Value=%v (%T), want float64(20.5)", e.Value, e.Value)
	}
	if e.ValueType != "float" {
		t.Errorf("ValueType=%q, want float", e.ValueType)
	}
}

func TestParseLineMetricWholeFloat(t *testing.T) {
	// A literal "13.0" must stay float (the dot is a syntactic signal).
	e, ok := ParseLine([]byte(`{"kind":"metric","name":"w","value":13.0}`))
	if !ok {
		t.Fatal("ok=false")
	}
	v, ok := e.Value.(float64)
	if !ok || v != 13.0 {
		t.Errorf("Value=%v (%T), want float64(13.0)", e.Value, e.Value)
	}
	if e.ValueType != "float" {
		t.Errorf("ValueType=%q, want float (literal 13.0 must stay float)", e.ValueType)
	}
}

func TestParseLineMetricBool(t *testing.T) {
	e, ok := ParseLine([]byte(`{"kind":"metric","name":"door","value":true}`))
	if !ok {
		t.Fatal("ok=false")
	}
	v, ok := e.Value.(int64)
	if !ok || v != 1 {
		t.Errorf("Value=%v (%T), want int64(1)", e.Value, e.Value)
	}
	if e.ValueType != "bool" {
		t.Errorf("ValueType=%q, want bool", e.ValueType)
	}
	// false → 0.
	e, _ = ParseLine([]byte(`{"kind":"metric","name":"door","value":false}`))
	v, _ = e.Value.(int64)
	if v != 0 {
		t.Errorf("false → %d, want 0", v)
	}
}

func TestParseLineMetricString(t *testing.T) {
	e, ok := ParseLine([]byte(`{"kind":"metric","name":"mode","value":"auto"}`))
	if !ok {
		t.Fatal("ok=false")
	}
	if e.Value != nil {
		t.Errorf("Value=%v, want nil (string lives in Text)", e.Value)
	}
	if e.Text != "auto" {
		t.Errorf("Text=%q, want auto", e.Text)
	}
	if e.ValueType != "string" {
		t.Errorf("ValueType=%q, want string", e.ValueType)
	}
}

func TestParseLineLog(t *testing.T) {
	e, ok := ParseLine([]byte(`{"kind":"log","text":"hello"}`))
	if !ok {
		t.Fatal("ok=false")
	}
	if e.Kind != "log" || e.Text != "hello" {
		t.Errorf("Kind=%q Text=%q", e.Kind, e.Text)
	}
	if e.ValueType != "" {
		t.Errorf("ValueType=%q, want \"\"", e.ValueType)
	}
}

func TestParseLineDefaultsAndKindFallback(t *testing.T) {
	// kind absent → "" at parse; caller substitutes "log".
	e, ok := ParseLine([]byte(`{"text":"hi"}`))
	if !ok {
		t.Fatal("ok=false")
	}
	if e.HasTS {
		t.Errorf("HasTS=true, want false (TS absent)")
	}
	if e.HasSeq {
		t.Errorf("HasSeq=true, want false (Seq absent)")
	}
	if e.Kind != "" {
		t.Errorf("Kind=%q, want \"\" (caller substitutes)", e.Kind)
	}
}

func TestParseLineTruncatedSkipped(t *testing.T) {
	// Truncated final line — no closing brace.
	_, ok := ParseLine([]byte(`{"kind":"metric","name":"pm","value":`))
	if ok {
		t.Error("truncated line ok=true, want false")
	}
}

func TestParseLineNonObjectSkipped(t *testing.T) {
	// JSON-valid but not an object.
	_, ok := ParseLine([]byte(`42`))
	if ok {
		t.Error("non-object ok=true, want false")
	}
	_, ok = ParseLine([]byte(`[1,2]`))
	if ok {
		t.Error("array root ok=true, want false")
	}
}

func TestParseLineNonScalarValueDegrades(t *testing.T) {
	// Object as value → row still ingests (ok=true) with Value=nil, ValueType="".
	e, ok := ParseLine([]byte(`{"kind":"metric","name":"x","value":[1,2]}`))
	if !ok {
		t.Fatal("ok=false, want true (degraded but ingestible)")
	}
	if e.Value != nil {
		t.Errorf("Value=%v, want nil (degraded)", e.Value)
	}
	if e.ValueType != "" {
		t.Errorf("ValueType=%q, want \"\" (degraded)", e.ValueType)
	}
}

func TestParseLineBlankSkipped(t *testing.T) {
	if _, ok := ParseLine([]byte("")); ok {
		t.Error("empty line ok=true, want false")
	}
	if _, ok := ParseLine([]byte("   ")); ok {
		t.Error("whitespace ok=true, want false")
	}
}
```

- [ ] **Step 2.2: Run the test, see it fail**

Run: `go test ./internal/telemetry/ -run TestParseLine`
Expected: FAIL with `package … not found` or `undefined: ParseLine`.

- [ ] **Step 2.3: Implement `internal/telemetry/parse.go`**

```go
// Package telemetry implements the porta gateway's telemetry plane:
// JSONL line parsing with type-faithful value_type inference, and the
// monitor row formatter. Pure logic — no I/O, no globals.
package telemetry

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Entry is a parsed JSONL telemetry line ready for store.InsertData. The
// HasTS / HasSeq booleans distinguish "absent" from "explicitly zero", so
// the caller can substitute the receive time / line index.
type Entry struct {
	TS        int64
	HasTS     bool
	Seq       int64
	HasSeq    bool
	Kind      string
	Name      string
	Value     any    // int64 / float64 / nil (the bound DB value)
	Text      string
	ValueType string // "int" | "float" | "bool" | "string" | ""
}

// ParseLine decodes one JSONL line into an Entry. Returns ok=false for:
//   - blank/whitespace-only lines
//   - lines that fail json.Decode (truncated tail, malformed)
//   - lines that decode to anything other than a JSON object
// ok=true for a successful decode, even when "value" was a non-scalar
// (array/object/null) — the row still ingests with Value=nil and
// ValueType="" (graceful degradation).
func ParseLine(line []byte) (Entry, bool) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return Entry{}, false
	}
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.UseNumber()
	var raw map[string]any
	if err := dec.Decode(&raw); err != nil {
		return Entry{}, false
	}
	if raw == nil {
		return Entry{}, false
	}
	e := Entry{}
	if v, ok := raw["ts"]; ok {
		if n, ok := v.(json.Number); ok {
			if i, err := n.Int64(); err == nil {
				e.TS = i
				e.HasTS = true
			}
		}
	}
	if v, ok := raw["seq"]; ok {
		if n, ok := v.(json.Number); ok {
			if i, err := n.Int64(); err == nil {
				e.Seq = i
				e.HasSeq = true
			}
		}
	}
	if v, ok := raw["kind"].(string); ok {
		e.Kind = v
	}
	if v, ok := raw["name"].(string); ok {
		e.Name = v
	}
	if v, ok := raw["text"].(string); ok {
		e.Text = v
	}
	classifyValue(&e, raw["value"])
	return e, true
}

// classifyValue inspects "value" and fills Value / ValueType / Text per the
// value_type inference rules (parity with examples/toit-gateway/handler.toit:160-184):
//   bool   → Value=int64(0|1), ValueType="bool"
//   number → int64 first, then float64; ValueType="int" or "float"
//   string → Text=raw, Value=nil, ValueType="string"
//   else (nil/array/object) → Value=nil, ValueType="" (degraded)
func classifyValue(e *Entry, raw any) {
	switch v := raw.(type) {
	case bool:
		if v {
			e.Value = int64(1)
		} else {
			e.Value = int64(0)
		}
		e.ValueType = "bool"
	case json.Number:
		s := v.String()
		if strings.ContainsAny(s, ".eE") {
			f, err := v.Float64()
			if err == nil {
				e.Value = f
				e.ValueType = "float"
			}
			return
		}
		if i, err := v.Int64(); err == nil {
			e.Value = i
			e.ValueType = "int"
			return
		}
		// Int parse failed (e.g. out of range) — fall back to float.
		if f, err := v.Float64(); err == nil {
			e.Value = f
			e.ValueType = "float"
		}
	case string:
		// A string in "value" overrides any "text" key.
		e.Text = v
		e.Value = nil
		e.ValueType = "string"
	default:
		// nil, []any, map[string]any → degraded.
		e.Value = nil
		e.ValueType = ""
	}
}
```

- [ ] **Step 2.4: Run the tests, see them pass**

Run: `go test ./internal/telemetry/ -v -run TestParseLine`
Expected: PASS — all 11 parse tests.

- [ ] **Step 2.5: Commit**

```bash
git add internal/telemetry/parse.go internal/telemetry/parse_test.go
git commit -m "$(cat <<'EOF'
feat(porta): telemetry — ParseLine for JSONL ingest

New internal/telemetry package; ParseLine decodes one JSONL line into an
Entry with value_type inference: bool→int64(0|1)+"bool", json.Number→
int64+"int" or float64+"float" (decided by canonical form), string→
Text+"string", nil/array/object→Value=nil+ValueType="" (graceful
degradation; row still ingests). Blank lines, truncated tails, and non-
object roots return ok=false so the caller skips them. Mirrors
examples/toit-gateway/handler.toit:160-184.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: `telemetry.FormatLine` — the monitor row formatter

**Files:**
- Create: `internal/telemetry/format.go`
- Test: `internal/telemetry/format_test.go`

- [ ] **Step 3.1: Write the failing test**

```go
// internal/telemetry/format_test.go
package telemetry

import (
	"testing"

	"github.com/davidg238/porta/internal/store"
)

func TestFormatLineMetricInt(t *testing.T) {
	r := store.DataRow{TS: 100, Seq: 0, Kind: "metric", Name: "n", Value: int64(7), ValueType: "int"}
	if got := FormatLine(r); got != "100  metric  n=7" {
		t.Errorf("got %q, want %q", got, "100  metric  n=7")
	}
}

func TestFormatLineMetricFloat(t *testing.T) {
	r := store.DataRow{TS: 101, Seq: 1, Kind: "metric", Name: "pm", Value: float64(13.5), ValueType: "float"}
	if got := FormatLine(r); got != "101  metric  pm=13.5" {
		t.Errorf("got %q, want %q", got, "101  metric  pm=13.5")
	}
}

func TestFormatLineMetricFloatWholeNumberAddsDecimal(t *testing.T) {
	// NUMERIC affinity stored 13.0 as INTEGER → QueryData returned int64(13);
	// value_type "float" must still render with a decimal point.
	r := store.DataRow{TS: 102, Seq: 0, Kind: "metric", Name: "pm", Value: int64(13), ValueType: "float"}
	if got := FormatLine(r); got != "102  metric  pm=13.0" {
		t.Errorf("got %q, want %q", got, "102  metric  pm=13.0")
	}
}

func TestFormatLineMetricBool(t *testing.T) {
	rt := store.DataRow{TS: 103, Kind: "metric", Name: "door", Value: int64(1), ValueType: "bool"}
	if got := FormatLine(rt); got != "103  metric  door=true" {
		t.Errorf("got %q, want %q", got, "103  metric  door=true")
	}
	rf := store.DataRow{TS: 104, Kind: "metric", Name: "door", Value: int64(0), ValueType: "bool"}
	if got := FormatLine(rf); got != "104  metric  door=false" {
		t.Errorf("got %q, want %q", got, "104  metric  door=false")
	}
}

func TestFormatLineMetricString(t *testing.T) {
	r := store.DataRow{TS: 105, Kind: "metric", Name: "mode", Text: "auto", ValueType: "string"}
	if got := FormatLine(r); got != "105  metric  mode=auto" {
		t.Errorf("got %q, want %q", got, "105  metric  mode=auto")
	}
}

func TestFormatLineLog(t *testing.T) {
	r := store.DataRow{TS: 106, Kind: "log", Text: "started blink", ValueType: ""}
	if got := FormatLine(r); got != "106  log     started blink" {
		t.Errorf("got %q, want %q", got, "106  log     started blink")
	}
}

func TestFormatLineDegradedRendersNull(t *testing.T) {
	// Metric whose ValueType is "" (e.g. value was a non-scalar at ingest) —
	// graceful: render name=null.
	r := store.DataRow{TS: 107, Kind: "metric", Name: "x", Value: nil, ValueType: ""}
	if got := FormatLine(r); got != "107  metric  x=null" {
		t.Errorf("got %q, want %q", got, "107  metric  x=null")
	}
}
```

- [ ] **Step 3.2: Run the test, see it fail**

Run: `go test ./internal/telemetry/ -run TestFormatLine`
Expected: FAIL with `undefined: FormatLine`.

- [ ] **Step 3.3: Implement `internal/telemetry/format.go`**

```go
// internal/telemetry/format.go
package telemetry

import (
	"fmt"
	"strconv"

	"github.com/davidg238/porta/internal/store"
)

// FormatLine renders one data_log row for `porta monitor`, with two
// fixed-width kind columns ("log    " / "metric "). Parity with
// examples/toit-gateway/gateway.toit:215-225.
func FormatLine(r store.DataRow) string {
	if r.Kind != "metric" {
		return fmt.Sprintf("%d  log     %s", r.TS, r.Text)
	}
	rendered := renderMetric(r)
	return fmt.Sprintf("%d  metric  %s=%s", r.TS, r.Name, rendered)
}

func renderMetric(r store.DataRow) string {
	switch r.ValueType {
	case "string":
		return r.Text
	case "bool":
		if asInt64(r.Value) != 0 {
			return "true"
		}
		return "false"
	case "float":
		// NUMERIC affinity may have stored a whole-number float as INTEGER,
		// so r.Value can be int64 13 with ValueType "float" — coerce to
		// float64 and render with a guaranteed decimal point.
		f := asFloat64(r.Value)
		// strconv.FormatFloat with -1 precision drops trailing zeros, so
		// 13.0 → "13"; reinstate the ".0" tail when the rendered form has
		// no decimal point and no exponent.
		s := strconv.FormatFloat(f, 'f', -1, 64)
		if !containsAny(s, ".eE") {
			s += ".0"
		}
		return s
	case "int":
		return strconv.FormatInt(asInt64(r.Value), 10)
	default:
		// Degraded — value was non-scalar at ingest, or unknown type tag.
		return "null"
	}
}

// asInt64 coerces an any (int64 or float64 from sql scan) to int64.
func asInt64(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case float64:
		return int64(n)
	default:
		return 0
	}
}

// asFloat64 coerces an any (int64 or float64) to float64.
func asFloat64(v any) float64 {
	switch n := v.(type) {
	case int64:
		return float64(n)
	case float64:
		return n
	default:
		return 0
	}
}

func containsAny(s, chars string) bool {
	for i := 0; i < len(s); i++ {
		for j := 0; j < len(chars); j++ {
			if s[i] == chars[j] {
				return true
			}
		}
	}
	return false
}
```

- [ ] **Step 3.4: Run the tests, see them pass**

Run: `go test ./internal/telemetry/ -v`
Expected: PASS — all 7 format tests plus the 11 parse tests from Task 2.

- [ ] **Step 3.5: Commit**

```bash
git add internal/telemetry/format.go internal/telemetry/format_test.go
git commit -m "$(cat <<'EOF'
feat(porta): telemetry — FormatLine for the monitor CLI row formatter

Renders a store.DataRow as one terminal line, dispatching on Kind and
ValueType:
  log    : "<ts>  log     <text>"
  metric : "<ts>  metric  <name>=<rendered>"
where rendered ∈ {"int"→digits, "float"→digits with guaranteed decimal
(handles the NUMERIC-affinity round-trip where a whole-number float
came back as int64), "bool"→true/false, "string"→text, ""→null}.
Mirrors examples/toit-gateway/gateway.toit:215-225.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: `command.SetConsole` constructor

**Files:**
- Modify: `internal/command/command.go` (append a constructor)
- Modify: `internal/command/command_test.go` (append a test)

- [ ] **Step 4.1: Write the failing test**

Append to `internal/command/command_test.go`:

```go
func TestSetConsole(t *testing.T) {
	on := SetConsole(true)
	if on.Verb != "set-console" {
		t.Errorf("Verb=%q, want set-console", on.Verb)
	}
	if on.ArgsJSON != `{"on":true}` {
		t.Errorf("ArgsJSON=%s, want {\"on\":true}", on.ArgsJSON)
	}
	off := SetConsole(false)
	if off.ArgsJSON != `{"on":false}` {
		t.Errorf("ArgsJSON=%s, want {\"on\":false}", off.ArgsJSON)
	}
	// Wire round-trip: verb + args spliced in flat form.
	wire := EncodeWire(on.Verb, on.ArgsJSON)
	verb, args, err := Decode(wire)
	if err != nil {
		t.Fatal(err)
	}
	if verb != "set-console" {
		t.Errorf("decoded verb=%q, want set-console", verb)
	}
	if v, ok := args["on"].(bool); !ok || !v {
		t.Errorf("decoded on=%v (%T), want bool true", args["on"], args["on"])
	}
}
```

- [ ] **Step 4.2: Run the test, see it fail**

Run: `go test ./internal/command/ -run TestSetConsole`
Expected: FAIL with `undefined: SetConsole`.

- [ ] **Step 4.3: Implement `SetConsole`**

Append to `internal/command/command.go` (after `SetPollInterval`):

```go
// SetConsole builds the telemetry-forwarding toggle command.
func SetConsole(on bool) Command {
	if on {
		return Command{Verb: "set-console", ArgsJSON: `{"on":true}`}
	}
	return Command{Verb: "set-console", ArgsJSON: `{"on":false}`}
}
```

- [ ] **Step 4.4: Run all command tests**

Run: `go test ./internal/command/ -v`
Expected: PASS for the new test; all existing tests still pass.

- [ ] **Step 4.5: Commit**

```bash
git add internal/command/command.go internal/command/command_test.go
git commit -m "$(cat <<'EOF'
feat(porta): command — SetConsole constructor for the forwarding toggle

Builds {"verb":"set-console","on":<bool>} per PROTOCOL.md §2.4. The node
persists the flag; default is off if absent at read time. Mirrors
examples/toit-gateway/command.toit's Command.set-console.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: Handler `data?id=` WRQ branch + integration tests

**Files:**
- Modify: `internal/handler/handler.go` (widen `AcceptWrite`; dispatch in `Write`; new `writeData`)
- Modify: `internal/handler/handler_test.go` (ingest scenarios)

- [ ] **Step 5.1: Write the failing integration tests**

Append to `internal/handler/handler_test.go`:

```go
func TestWriteAcceptsDataAndIngestsJSONL(t *testing.T) {
	h, st := newH(t)
	st.EnsureNode("aabbccddeeff", 1000)
	body := []byte(
		`{"ts":100,"seq":0,"kind":"metric","name":"pm","value":13}` + "\n" +
			`{"ts":101,"seq":1,"kind":"metric","name":"t","value":20.5}` + "\n" +
			`{"ts":102,"seq":2,"kind":"metric","name":"door","value":true}` + "\n" +
			`{"ts":103,"seq":3,"kind":"metric","name":"mode","value":"auto"}` + "\n" +
			`{"ts":104,"seq":4,"kind":"log","text":"started blink"}` + "\n")
	if err := h.AcceptWrite("data?id=aabbccddeeff", "p:1"); err != nil {
		t.Fatalf("AcceptWrite: %v", err)
	}
	if err := h.Write("data?id=aabbccddeeff", "p:1", body); err != nil {
		t.Fatalf("Write: %v", err)
	}
	rows, _ := st.QueryData("aabbccddeeff", 0, 200, "")
	if len(rows) != 5 {
		t.Fatalf("got %d rows, want 5", len(rows))
	}
	// Spot-check the type tags.
	if rows[0].ValueType != "int" {
		t.Errorf("rows[0].ValueType=%q, want int", rows[0].ValueType)
	}
	if rows[1].ValueType != "float" {
		t.Errorf("rows[1].ValueType=%q, want float", rows[1].ValueType)
	}
	if rows[2].ValueType != "bool" {
		t.Errorf("rows[2].ValueType=%q, want bool", rows[2].ValueType)
	}
	if rows[3].ValueType != "string" {
		t.Errorf("rows[3].ValueType=%q, want string", rows[3].ValueType)
	}
	if rows[3].Text != "auto" {
		t.Errorf("rows[3].Text=%q, want auto", rows[3].Text)
	}
	if rows[4].Kind != "log" || rows[4].Text != "started blink" {
		t.Errorf("rows[4]=%+v", rows[4])
	}
}

func TestWriteDataTruncatedTailToleratesSkip(t *testing.T) {
	h, st := newH(t)
	st.EnsureNode("ffeeddccbbaa", 1000)
	body := []byte(
		`{"ts":100,"kind":"log","text":"a"}` + "\n" +
			`{"ts":101,"kind":"log","text":"b"}` + "\n" +
			`{"ts":102,"kind":"met` /* truncated */)
	if err := h.Write("data?id=ffeeddccbbaa", "p:1", body); err != nil {
		t.Fatalf("Write: %v", err)
	}
	rows, _ := st.QueryData("ffeeddccbbaa", 0, 200, "")
	if len(rows) != 2 {
		t.Errorf("got %d rows, want 2 (truncated tail must be skipped)", len(rows))
	}
}

func TestWriteDataNonObjectLineSkipped(t *testing.T) {
	h, st := newH(t)
	st.EnsureNode("112233445566", 1000)
	body := []byte(
		`{"ts":100,"kind":"log","text":"a"}` + "\n" +
			`42` + "\n" +
			`{"ts":101,"kind":"log","text":"b"}` + "\n")
	if err := h.Write("data?id=112233445566", "p:1", body); err != nil {
		t.Fatalf("Write: %v", err)
	}
	rows, _ := st.QueryData("112233445566", 0, 200, "")
	if len(rows) != 2 {
		t.Errorf("got %d rows, want 2 (non-object line skipped)", len(rows))
	}
}

func TestWriteDataNonScalarValueDegrades(t *testing.T) {
	h, st := newH(t)
	st.EnsureNode("aaaa11112222", 1000)
	body := []byte(`{"ts":300,"kind":"metric","name":"x","value":[1,2]}` + "\n")
	if err := h.Write("data?id=aaaa11112222", "p:1", body); err != nil {
		t.Fatalf("Write: %v", err)
	}
	rows, _ := st.QueryData("aaaa11112222", 0, 400, "")
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].Value != nil {
		t.Errorf("Value=%v, want nil (degraded)", rows[0].Value)
	}
	if rows[0].ValueType != "" {
		t.Errorf("ValueType=%q, want \"\" (degraded)", rows[0].ValueType)
	}
}

func TestAcceptWriteRejectsDataWithoutID(t *testing.T) {
	h, _ := newH(t)
	if err := h.AcceptWrite("data", "p:1"); err == nil {
		t.Error("AcceptWrite(data) without id must error")
	}
	if err := h.AcceptWrite("data?id=", "p:1"); err == nil {
		t.Error("AcceptWrite(data?id=) (empty id) must error")
	}
}
```

Also adjust the existing `TestWriteRejectsNonReportAndMissingID` (in the
same file) — it currently asserts that anything other than `report` is
rejected. After this task, `data` is allowed; update it to assert that
`bogus` (an unknown base) is rejected:

```go
// Replace the body of TestWriteRejectsNonReportAndMissingID with:
func TestWriteRejectsNonReportAndMissingID(t *testing.T) {
	h, _ := newH(t)
	if err := h.AcceptWrite("bogus?id=dev", "p:1"); err == nil {
		t.Error("unknown base must be rejected")
	}
	if err := h.AcceptWrite("report", "p:1"); err == nil {
		t.Error("report without id must be rejected")
	}
}
```

- [ ] **Step 5.2: Run the tests, see them fail**

Run: `go test ./internal/handler/ -run "TestWriteAcceptsData|TestWriteDataTruncated|TestWriteDataNonObject|TestWriteDataNonScalar|TestAcceptWriteRejectsDataWithoutID"`
Expected: FAIL — `AcceptWrite("data?id=…")` currently returns "access denied".

- [ ] **Step 5.3: Modify `Handler` to accept and dispatch `data?id=`**

In `internal/handler/handler.go`:

1. Add `"github.com/davidg238/porta/internal/telemetry"` to the import block.

2. Replace `AcceptWrite` with:

```go
// AcceptWrite gates WRQs: report?id= and data?id= are accepted. Everything
// else (missing id, unknown base) → TFTP ERROR.
func (h *Handler) AcceptWrite(resource, peer string) error {
	base, params := parseResource(resource)
	if base != "report" && base != "data" {
		return fmt.Errorf("access denied: %s", base)
	}
	if params["id"] == "" {
		return fmt.Errorf("access denied: %s missing id", base)
	}
	return nil
}
```

3. Replace `Write` with the dispatching version:

```go
// Write ingests a completed WRQ body: report → observed_state + reconcile;
// data → JSONL telemetry ingest. Anything else is rejected.
func (h *Handler) Write(resource, peer string, data []byte) error {
	base, params := parseResource(resource)
	id := params["id"]
	if id == "" {
		return fmt.Errorf("access denied")
	}
	switch base {
	case "report":
		return h.writeReport(id, peer, data)
	case "data":
		return h.writeData(id, peer, data)
	default:
		return fmt.Errorf("access denied: %s", base)
	}
}

// writeReport is the previous Write body, refactored out.
func (h *Handler) writeReport(id, peer string, data []byte) error {
	if err := h.store.TouchNode(id, peer, h.now()); err != nil {
		return err
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return fmt.Errorf("report: bad json: %w", err)
	}
	field := func(k string) json.RawMessage {
		if v, ok := obj[k]; ok {
			return v
		}
		return json.RawMessage("{}")
	}
	observed := fmt.Sprintf(`{"apps":%s,"config":%s}`, field("apps"), field("config"))
	health := string(field("health"))
	if err := h.store.InsertReport(id, observed, health, h.now()); err != nil {
		return err
	}
	h.reconcileAfterReport(id, field("config"))
	return nil
}

// writeData ingests a JSONL telemetry body. Best-effort per line: blank
// lines, truncated tails, and non-object lines are skipped (no error). A
// non-scalar "value" inserts a row with Value=nil, ValueType=NULL
// (graceful degradation). A real SQL failure on TouchNode propagates;
// per-row InsertData failures are logged and the loop continues.
// Parity with examples/toit-gateway/handler.toit's DataWriter_.
func (h *Handler) writeData(id, peer string, data []byte) error {
	if err := h.store.TouchNode(id, peer, h.now()); err != nil {
		return err
	}
	now := h.now()
	for i, raw := range bytes.Split(data, []byte("\n")) {
		e, ok := telemetry.ParseLine(raw)
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
			h.log("porta: data ingest insert error for %s seq=%d: %v", id, seq, err)
			continue
		}
	}
	return nil
}
```

- [ ] **Step 5.4: Run the handler test suite**

Run: `go test ./internal/handler/ -v`
Expected: every test passes — all B1+B2 tests plus the 5 new ingest tests
plus the updated rejection test.

- [ ] **Step 5.5: Run the full Go test suite**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: green across every package.

- [ ] **Step 5.6: Commit**

```bash
git add internal/handler/handler.go internal/handler/handler_test.go
git commit -m "$(cat <<'EOF'
feat(porta): handler — data?id= JSONL ingest branch in Write

AcceptWrite widens to {report,data}; Write dispatches on base. The new
writeData loops over the body's lines, parses each via telemetry.ParseLine,
substitutes defaults (ts→receive time, seq→line index, kind→"log") and
InsertData per entry. Blank lines, truncated tails, non-object lines, and
non-scalar values all degrade gracefully without aborting the batch
(parity with examples/toit-gateway/handler.toit's DataWriter_).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: `porta device set-console` CLI

**Files:**
- Modify: `internal/portacli/mutate.go` (append `runDeviceSetConsole` + `newDeviceSetConsoleCmd`)
- Modify: `internal/portacli/inspect.go` (wire `newDeviceSetConsoleCmd` into `newDeviceCmd()`)
- Modify: `internal/portacli/config_test.go` (append test)

- [ ] **Step 6.1: Write the failing test**

Append to `internal/portacli/config_test.go`:

```go
func TestRunDeviceSetConsoleOn(t *testing.T) {
	st, _ := store.Open(t.TempDir() + "/sc.db")
	defer st.Close()
	st.EnsureNode("dev", 1000)
	var out bytes.Buffer
	if err := runDeviceSetConsole(&out, st, "dev", "on", 2000); err != nil {
		t.Fatal(err)
	}
	c, _ := st.NextUndelivered("dev")
	if c == nil || c.Verb != "set-console" {
		t.Fatalf("expected set-console command, got %+v", c)
	}
	if c.Args != `{"on":true}` {
		t.Errorf("Args=%s, want {\"on\":true}", c.Args)
	}
	if c.IssuedBy != "cli" {
		t.Errorf("IssuedBy=%q, want cli", c.IssuedBy)
	}
	if !strings.Contains(out.String(), "enqueued set-console on") {
		t.Errorf("stdout=%q, want enqueue message", out.String())
	}
}

func TestRunDeviceSetConsoleOff(t *testing.T) {
	st, _ := store.Open(t.TempDir() + "/sc.db")
	defer st.Close()
	st.EnsureNode("dev", 1000)
	var out bytes.Buffer
	if err := runDeviceSetConsole(&out, st, "dev", "off", 2000); err != nil {
		t.Fatal(err)
	}
	c, _ := st.NextUndelivered("dev")
	if c == nil || c.Args != `{"on":false}` {
		t.Errorf("Args=%v, want {\"on\":false}", c)
	}
}

func TestRunDeviceSetConsoleRejectsBadState(t *testing.T) {
	st, _ := store.Open(t.TempDir() + "/sc.db")
	defer st.Close()
	st.EnsureNode("dev", 1000)
	var out bytes.Buffer
	if err := runDeviceSetConsole(&out, st, "dev", "maybe", 2000); err == nil {
		t.Error("expected error for state=maybe")
	}
}
```

- [ ] **Step 6.2: Run the test, see it fail**

Run: `go test ./internal/portacli/ -run TestRunDeviceSetConsole`
Expected: FAIL with `undefined: runDeviceSetConsole`.

- [ ] **Step 6.3: Implement `runDeviceSetConsole` + `newDeviceSetConsoleCmd`**

Append to `internal/portacli/mutate.go` (after `newDeviceSetCmd`):

```go
// runDeviceSetConsole is the testable core of `porta device set-console`:
// it validates the state token, enqueues a set-console command tagged
// issued_by="cli", and prints a confirmation line.
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

func newDeviceSetConsoleCmd() *cobra.Command {
	var device string
	cmd := &cobra.Command{
		Use:   "set-console <on|off>",
		Short: "Toggle a node's console/telemetry forwarding",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			id, err := resolveNodeID(st, device)
			if err != nil {
				return err
			}
			if err := st.EnsureNode(id, nowSec()); err != nil {
				return err
			}
			return runDeviceSetConsole(cmd.OutOrStdout(), st, id, args[0], nowSec())
		},
	}
	deviceFlag(cmd, &device)
	return cmd
}
```

In `internal/portacli/inspect.go`, update `newDeviceCmd` to include the new
subcommand:

```go
func newDeviceCmd() *cobra.Command {
	parent := &cobra.Command{Use: "device", Short: "Per-node operations"}
	parent.AddCommand(
		newDeviceShowCmd(),
		newDeviceGetCmd(),
		newDeviceSetCmd(),
		newDeviceSetConsoleCmd(),
		newDeviceSetPollIntervalCmd(),
		newDeviceSetMaxOfflineCmd(),
		newDeviceNameCmd(),
	)
	return parent
}
```

- [ ] **Step 6.4: Run the tests, see them pass**

Run: `go test ./internal/portacli/ -run TestRunDeviceSetConsole -v`
Expected: PASS for the 3 new tests; full portacli suite stays green.

- [ ] **Step 6.5: Confirm cobra wiring**

Run: `go build ./cmd/porta && ./porta device set-console --help`
Expected: usage `set-console <on|off>` with the `-d/--device` flag.

- [ ] **Step 6.6: Commit**

```bash
git add internal/portacli/mutate.go internal/portacli/inspect.go internal/portacli/config_test.go
git commit -m "$(cat <<'EOF'
feat(porta): cli — device set-console <on|off>

Enqueues a set-console command tagged issued_by="cli" so the next wake
flips the node's telemetry-forwarding flag. Rejects any state other than
"on"/"off". Wired as a sub-command of `device` alongside set / set-poll-
interval / set-max-offline / name. Mirrors
examples/toit-gateway/gateway.toit:274-284.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: `porta monitor` CLI (range + --follow)

**Files:**
- Create: `internal/portacli/monitor.go`
- Create: `internal/portacli/monitor_test.go`
- Modify: `internal/portacli/root.go` (register `newMonitorCmd`)

- [ ] **Step 7.1: Write the failing tests**

```go
// internal/portacli/monitor_test.go
package portacli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/davidg238/porta/internal/store"
)

func seededStore(t *testing.T, dev string) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/m.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	st.InsertData(dev, 100, 0, "metric", "pm", int64(13), "", "int")
	st.InsertData(dev, 101, 1, "metric", "t", float64(20.5), "", "float")
	st.InsertData(dev, 102, 2, "metric", "door", int64(1), "", "bool")
	st.InsertData(dev, 103, 3, "metric", "mode", nil, "auto", "string")
	st.InsertData(dev, 104, 4, "log", "", nil, "started blink", "")
	return st
}

func TestRunMonitorRangePrintsAllScalars(t *testing.T) {
	dev := "aabbccddeeff"
	st := seededStore(t, dev)
	var out bytes.Buffer
	now := func() int64 { return 200 }
	if err := runMonitor(context.Background(), &out, st, dev, 200, "", false, now, 10*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 5 {
		t.Fatalf("got %d lines, want 5: %q", len(lines), out.String())
	}
	wants := []string{
		"100  metric  pm=13",
		"101  metric  t=20.5",
		"102  metric  door=true",
		"103  metric  mode=auto",
		"104  log     started blink",
	}
	for i, w := range wants {
		if lines[i] != w {
			t.Errorf("line %d = %q, want %q", i, lines[i], w)
		}
	}
}

func TestRunMonitorKindFilter(t *testing.T) {
	dev := "ffeeddccbbaa"
	st := seededStore(t, dev)
	var out bytes.Buffer
	now := func() int64 { return 200 }
	if err := runMonitor(context.Background(), &out, st, dev, 200, "log", false, now, 10*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], "started blink") {
		t.Errorf("kind=log filter: lines=%v", lines)
	}
}

func TestRunMonitorFollowExitsOnCancel(t *testing.T) {
	dev := "112233445566"
	st := seededStore(t, dev)
	var out bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	// Cancel after a few poll intervals — the loop must return promptly.
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()
	now := func() int64 { return 200 }
	done := make(chan error, 1)
	go func() {
		done <- runMonitor(ctx, &out, st, dev, 200, "", true, now, 10*time.Millisecond)
	}()
	select {
	case err := <-done:
		if err != nil && err != context.Canceled {
			t.Fatalf("runMonitor returned %v, want nil or context.Canceled", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("runMonitor --follow did not exit after cancel")
	}
}
```

- [ ] **Step 7.2: Run the tests, see them fail**

Run: `go test ./internal/portacli/ -run TestRunMonitor`
Expected: FAIL with `undefined: runMonitor`.

- [ ] **Step 7.3: Implement `internal/portacli/monitor.go`**

```go
// internal/portacli/monitor.go
package portacli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/davidg238/porta/internal/command"
	"github.com/davidg238/porta/internal/store"
	"github.com/davidg238/porta/internal/telemetry"
	"github.com/spf13/cobra"
)

// runMonitor is the testable core of `porta monitor`. It prints the
// data_log rows for (id, sinceS look-back, kind filter), formatted via
// telemetry.FormatLine. If follow=true, it polls every pollInterval until
// ctx is cancelled, advancing the watermark by (last+1) to dedup. The
// boundary-row edge case (rows sharing the poll-tick ts) is accepted as-is
// (see spec §7).
func runMonitor(ctx context.Context, out io.Writer, st *store.Store,
	id string, sinceS int64, kind string, follow bool,
	now func() int64, pollInterval time.Duration,
) error {
	until := now()
	since := until - sinceS
	rows, err := st.QueryData(id, since, until, kind)
	if err != nil {
		return err
	}
	for _, r := range rows {
		fmt.Fprintln(out, telemetry.FormatLine(r))
	}
	if !follow {
		return nil
	}
	last := until
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			err := ctx.Err()
			if err == context.Canceled {
				return nil
			}
			return err
		case <-ticker.C:
			t := now()
			rows, err := st.QueryData(id, last+1, t, kind)
			if err != nil {
				return err
			}
			for _, r := range rows {
				fmt.Fprintln(out, telemetry.FormatLine(r))
			}
			last = t
		}
	}
}

func newMonitorCmd() *cobra.Command {
	var device, since, kind string
	var follow bool
	cmd := &cobra.Command{
		Use:   "monitor",
		Short: "Print a node's telemetry; --follow tails new rows as wakes deliver them",
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			id, err := resolveNodeID(st, device)
			if err != nil {
				return err
			}
			sinceS := int64(3600)
			if since != "" {
				s, err := command.ParseDurationSeconds(since)
				if err != nil {
					return err
				}
				sinceS = s
			}
			return runMonitor(cmd.Context(), cmd.OutOrStdout(), st, id, sinceS, kind, follow, nowSec, 2*time.Second)
		},
	}
	deviceFlag(cmd, &device)
	cmd.Flags().StringVar(&since, "since", "", "look-back window, e.g. 30m, 1h (default 1h)")
	cmd.Flags().StringVar(&kind, "kind", "", "filter to 'log' or 'metric'")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "poll the store and tail new rows")
	return cmd
}
```

Then in `internal/portacli/root.go`, register the new command:

```go
// In NewRootCmd, add newMonitorCmd() to the AddCommand list.
root.AddCommand(
	newServeCmd(),
	newScanCmd(),
	newPingCmd(),
	newDeviceCmd(),
	newContainerCmd(),
	newLogCmd(),
	newMonitorCmd(),
)
```

- [ ] **Step 7.4: Run the tests, see them pass**

Run: `go test ./internal/portacli/ -run TestRunMonitor -v -race`
Expected: PASS — range, kind filter, and follow-cancel.

- [ ] **Step 7.5: Confirm cobra wiring**

Run: `go build ./cmd/porta && ./porta monitor --help`
Expected: usage `monitor` with `-d/--device`, `--since`, `--kind`,
`-f/--follow` flags.

- [ ] **Step 7.6: Run the full Go test suite + parked-Toit regression**

Run: `go build ./... && go vet ./... && go test -race ./...`
Expected: every package green.

Run: `cd examples/toit-gateway && ./run-host-tests.sh && cd -`
Expected: the parked Toit reference still builds + passes its host suites
(parity invariant from the spec's §6.4).

- [ ] **Step 7.7: Commit**

```bash
git add internal/portacli/monitor.go internal/portacli/monitor_test.go internal/portacli/root.go
git commit -m "$(cat <<'EOF'
feat(porta): cli — monitor (range + --follow telemetry tail)

Top-level `porta monitor -d <node> [--since <dur>] [--kind log|metric]
[-f|--follow]`: prints data_log rows formatted by telemetry.FormatLine
over the look-back window; --follow polls every 2s, advancing a (last+1)
watermark to dedup. Ctx cancellation (SIGINT from cobra) exits cleanly.
The boundary-row edge case (rows sharing the poll-tick ts) is accepted
as-is per spec §7. Mirrors examples/toit-gateway/gateway.toit:413-435.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Self-Review

**Spec coverage check:**

- §2 wire contracts (set-console, data?id= JSONL) → Task 4 implements
  set-console; Task 5 implements data?id= ingest; both already documented
  in PROTOCOL.md (no doc change needed).
- §3 architecture (internal/telemetry, store/data.go) → Tasks 1-3.
- §4.1 store methods → Task 1.
- §4.2 telemetry package → Tasks 2 + 3.
- §4.3 SetConsole → Task 4.
- §4.4 handler data?id= → Task 5.
- §4.5 device set-console CLI → Task 6.
- §4.6 monitor CLI → Task 7.
- §5.1 value_type inference at parse time → Task 2 (covered by tests).
- §5.2 NUMERIC-affinity round trip → Tasks 1 (insert/query) + 3 (format).
- §5.3 tolerance (truncated, non-object, non-scalar) → Tasks 2 + 5.
- §5.4 --follow polling + ctx cancel → Task 7.
- §5.5 best-effort per-line failure → Task 5's writeData.
- §6 testing strategy → covered task-by-task; the acceptance gate
  (`go test -race ./...` + parked Toit regression) is Task 7's Step 7.6.

**Placeholder scan:** every step contains the exact code/command/expected
output the implementer needs; no "TBD" / "handle edge cases" / "similar to
Task N" placeholders.

**Type consistency:**
- `Entry` (telemetry pkg) fields used in Task 5 match Task 2's definition.
- `DataRow` fields used in Task 3 (`FormatLine`) match Task 1's struct.
- `runMonitor(...)` signature in Task 7's test matches Task 7's impl
  (`ctx, out, st, id, sinceS, kind, follow, now, pollInterval`).
- `runDeviceSetConsole(...)` signature in Task 6's test matches the impl
  (`out, st, id, state, now`).
- `command.SetConsole(bool) Command` shape consistent across Tasks 4 + 6.

No gaps found.
