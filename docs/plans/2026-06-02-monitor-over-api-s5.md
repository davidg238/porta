# S5 — `porta monitor` over the control-plane API — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Re-point `porta monitor` from the local store to the HTTP control-plane API (new `GET /api/nodes/{sel}/telemetry`), with an exact `id`-cursor `--follow` that removes today's timestamp-watermark boundary edge case.

**Architecture:** A new windowed read endpoint on the existing `apisrv` listener (selector resolved server-side, `{ok,data,error}` envelope). The CLI polls it: a time-window read seeds the tail, then `--follow` advances an `id` cursor (`data_log.id`, an AUTOINCREMENT rowid alias — strictly monotonic, never reused). `apiclient` stays store-free with a wire DTO; `telemetry.FormatLine` stays client-side so output is byte-for-byte identical to today.

**Tech Stack:** Go 1.22 (`net/http` method patterns), SQLite (`go-sqlite3`), cobra, `httptest`.

**Spec:** `docs/specs/2026-06-02-monitor-over-api-s5-design.md`

---

## File map

- `internal/store/data.go` — add `ID` to `DataRow`; `QueryDataLimited` selects `id`; new `QueryDataAfter`. (Task 1)
- `internal/store/data_after_test.go` — new store tests. (Task 1)
- `internal/apisrv/telemetry.go` — new `handleTelemetry`, `telemetryRow` DTO, `parseOptInt`. (Task 2)
- `internal/apisrv/apisrv.go` — register the route. (Task 2)
- `internal/apisrv/telemetry_test.go` — new handler tests. (Task 2)
- `internal/apiclient/client.go` — `DataRow`, `QueryTelemetryWindow`/`QueryTelemetryAfter`, `typedValue`. (Task 3)
- `internal/apiclient/telemetry_test.go` — new client tests. (Task 3)
- `internal/portacli/monitor.go` — rewrite `runMonitor` over the client; `toStoreRow`; drop `openStore`. (Task 4)
- `internal/portacli/monitor_test.go` — rewrite for the new signature. (Task 4)
- `internal/portacli/client.go` — update the stale "reads stay db-backed" comment. (Task 4)

---

## Task 1: Store — `id` cursor support

**Files:**
- Modify: `internal/store/data.go` (DataRow struct ~16-24; QueryDataLimited ~64-97)
- Test: `internal/store/data_after_test.go` (create)

- [ ] **Step 1: Write the failing tests**

Create `internal/store/data_after_test.go`:

```go
package store

import "testing"

func seedAfter(t *testing.T) *Store {
	t.Helper()
	st, err := Open(t.TempDir() + "/a.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	// ids 1..4 in insertion order (AUTOINCREMENT).
	st.InsertData("dev", 100, 0, "metric", "pm", int64(13), "", "int")
	st.InsertData("dev", 101, 1, "metric", "t", float64(20.5), "", "float")
	st.InsertData("dev", 102, 2, "log", "", nil, "hello", "")
	st.InsertData("dev", 103, 3, "metric", "pm", int64(14), "", "int")
	st.InsertData("other", 104, 0, "metric", "pm", int64(99), "", "int")
	return st
}

func TestQueryDataLimitedPopulatesID(t *testing.T) {
	st := seedAfter(t)
	rows, err := st.QueryDataLimited("dev", 0, 0, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Fatalf("got %d rows, want 4", len(rows))
	}
	for i, r := range rows {
		if r.ID != int64(i+1) {
			t.Errorf("rows[%d].ID = %d, want %d", i, r.ID, i+1)
		}
	}
}

func TestQueryDataAfterFiltersByID(t *testing.T) {
	st := seedAfter(t)
	rows, err := st.QueryDataAfter("dev", 2, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].ID != 3 || rows[1].ID != 4 {
		t.Fatalf("after=2 got %+v, want ids 3,4", rows)
	}
}

func TestQueryDataAfterKindAndLimit(t *testing.T) {
	st := seedAfter(t)
	rows, err := st.QueryDataAfter("dev", 0, "metric", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].ID != 1 || rows[1].ID != 2 {
		t.Fatalf("kind=metric limit=2 got %+v, want ids 1,2", rows)
	}
}

func TestQueryDataAfterScopedByDevice(t *testing.T) {
	st := seedAfter(t)
	rows, err := st.QueryDataAfter("dev", 0, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Fatalf("got %d rows, want 4 (other device excluded)", len(rows))
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/store/ -run 'QueryDataAfter|PopulatesID' -v`
Expected: FAIL — `r.ID` undefined and `QueryDataAfter` undefined (compile error).

- [ ] **Step 3: Add the `ID` field to `DataRow`**

In `internal/store/data.go`, add `ID` as the first field of `DataRow`:

```go
type DataRow struct {
	ID        int64
	TS        int64
	Seq       int64
	Kind      string
	Name      string
	Value     any
	Text      string
	ValueType string
}
```

- [ ] **Step 4: Make `QueryDataLimited` select `id`**

In `internal/store/data.go`, change the `QueryDataLimited` SELECT to include `id` as the first column. Replace the query-string opening line:

```go
	q := `SELECT id, ts, seq, COALESCE(kind,''), COALESCE(name,''), value, COALESCE(text,''), COALESCE(value_type,'')
		  FROM data_log WHERE device_id = ? AND ts >= ?`
```

and update the scan inside the `for rows.Next()` loop to read `id` first (the remaining columns keep their existing order):

```go
		var r DataRow
		var v any
		if err := rows.Scan(&r.ID, &r.TS, &r.Seq, &r.Kind, &r.Name, &v, &r.Text, &r.ValueType); err != nil {
			return nil, err
		}
		r.Value = normalizeNumeric(v)
		out = append(out, r)
```

- [ ] **Step 5: Add `QueryDataAfter`**

In `internal/store/data.go`, after `QueryDataLimited`, add:

```go
// QueryDataAfter returns the device's rows with id > after, ordered by id
// (the data_log AUTOINCREMENT primary key — strictly monotonic, never reused),
// so `porta monitor --follow` tails exactly the rows inserted since the last
// poll with no timestamp-tie boundary case. kind restricts to that kind when
// non-empty; limit caps the row count in SQL when > 0.
func (s *Store) QueryDataAfter(deviceID string, after int64, kind string, limit int) ([]DataRow, error) {
	q := `SELECT id, ts, seq, COALESCE(kind,''), COALESCE(name,''), value, COALESCE(text,''), COALESCE(value_type,'')
		  FROM data_log WHERE device_id = ? AND id > ?`
	args := []any{deviceID, after}
	if kind != "" {
		q += ` AND kind = ?`
		args = append(args, kind)
	}
	q += ` ORDER BY id`
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DataRow
	for rows.Next() {
		var r DataRow
		var v any
		if err := rows.Scan(&r.ID, &r.TS, &r.Seq, &r.Kind, &r.Name, &v, &r.Text, &r.ValueType); err != nil {
			return nil, err
		}
		r.Value = normalizeNumeric(v)
		out = append(out, r)
	}
	return out, rows.Err()
}
```

- [ ] **Step 6: Run the store tests to verify they pass**

Run: `go test ./internal/store/ -v`
Expected: PASS (new tests + existing ones — `QueryDataLimited` consumers ignore the new `ID` field).

- [ ] **Step 7: Commit**

```bash
git add internal/store/data.go internal/store/data_after_test.go
git commit -m "feat(store): id cursor for telemetry tail (QueryDataAfter + DataRow.ID)"
```

---

## Task 2: apisrv — `GET /api/nodes/{sel}/telemetry`

**Files:**
- Create: `internal/apisrv/telemetry.go`
- Modify: `internal/apisrv/apisrv.go:53` (register route)
- Test: `internal/apisrv/telemetry_test.go` (create)

- [ ] **Step 1: Write the failing tests**

Create `internal/apisrv/telemetry_test.go`:

```go
package apisrv

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/davidg238/porta/internal/store"
)

func telemetryHarness(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	st.InsertData("aabbccddeeff", 100, 0, "metric", "pm", int64(13), "", "int")
	st.InsertData("aabbccddeeff", 101, 1, "metric", "t", float64(20.5), "", "float")
	st.InsertData("aabbccddeeff", 102, 2, "log", "", nil, "hello", "")
	mux := http.NewServeMux()
	New(st).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, st
}

// getRows GETs the telemetry endpoint and returns the decoded rows.
func getRows(t *testing.T, srv *httptest.Server, query string) []map[string]any {
	t.Helper()
	resp, err := http.Get(srv.URL + "/api/nodes/aabbccddeeff/telemetry?" + query)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d for query %q", resp.StatusCode, query)
	}
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Rows []map[string]any `json:"rows"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if !env.OK {
		t.Fatalf("ok=false for query %q", query)
	}
	return env.Data.Rows
}

func TestTelemetryWindowMode(t *testing.T) {
	srv, _ := telemetryHarness(t)
	rows := getRows(t, srv, "since=0")
	if len(rows) != 3 {
		t.Fatalf("window since=0 got %d rows, want 3", len(rows))
	}
	if rows[0]["id"].(float64) != 1 || rows[0]["value_type"] != "int" {
		t.Fatalf("row0 = %+v", rows[0])
	}
}

func TestTelemetryCursorModeTakesPrecedence(t *testing.T) {
	srv, _ := telemetryHarness(t)
	// after=1 should skip id=1 even though since=0 would include it.
	rows := getRows(t, srv, "after=1&since=0")
	if len(rows) != 2 || rows[0]["id"].(float64) != 2 {
		t.Fatalf("after=1 got %+v, want ids 2,3", rows)
	}
}

func TestTelemetryKindFilter(t *testing.T) {
	srv, _ := telemetryHarness(t)
	rows := getRows(t, srv, "since=0&kind=log")
	if len(rows) != 1 || rows[0]["text"] != "hello" {
		t.Fatalf("kind=log got %+v", rows)
	}
}

func TestTelemetryBadParam400(t *testing.T) {
	srv, _ := telemetryHarness(t)
	resp, err := http.Get(srv.URL + "/api/nodes/aabbccddeeff/telemetry?since=notanumber")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status %d, want 400", resp.StatusCode)
	}
}

func TestTelemetryUnknownSelector404(t *testing.T) {
	srv, _ := telemetryHarness(t)
	resp, err := http.Get(srv.URL + "/api/nodes/nosuchnode/telemetry?since=0")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("status %d, want 404", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/apisrv/ -run Telemetry -v`
Expected: FAIL — route not registered (404 for the window test) / `handleTelemetry` undefined.

- [ ] **Step 3: Create the handler**

Create `internal/apisrv/telemetry.go`:

```go
package apisrv

import (
	"net/http"
	"strconv"

	"github.com/davidg238/porta/internal/store"
)

// telemetryRow is one row of GET /api/nodes/{sel}/telemetry. It mirrors
// store.DataRow on the wire (so apiclient need not import store). value is the
// typed scalar (number for int/float/bool, null for string & log rows whose
// payload is in text); value_type drives client-side rendering.
type telemetryRow struct {
	ID        int64  `json:"id"`
	TS        int64  `json:"ts"`
	Seq       int64  `json:"seq"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Value     any    `json:"value"`
	Text      string `json:"text"`
	ValueType string `json:"value_type"`
}

// parseOptInt parses an optional integer query param: "" → (def, true); a valid
// integer → (n, true); anything else → (0, false).
func parseOptInt(s string, def int64) (int64, bool) {
	if s == "" {
		return def, true
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// handleTelemetry returns a node's data_log rows. With ?after=<id> it tails by
// the monotonic id cursor (ordered by id); otherwise it returns the ts window
// [since, until] (until<=0 = unbounded). kind filters log|metric; limit caps
// the rows in SQL. The selector is resolved server-side (read-only, no EnsureNode).
func (h *Handler) handleTelemetry(w http.ResponseWriter, r *http.Request) {
	id, ok := h.resolveSel(w, r.PathValue("sel"))
	if !ok {
		return
	}
	q := r.URL.Query()
	kind := q.Get("kind")
	limit64, ok := parseOptInt(q.Get("limit"), 0)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid limit")
		return
	}
	limit := int(limit64)

	var rows []store.DataRow
	var err error
	if q.Has("after") {
		after, okA := parseOptInt(q.Get("after"), 0)
		if !okA {
			writeErr(w, http.StatusBadRequest, "invalid after")
			return
		}
		rows, err = h.st.QueryDataAfter(id, after, kind, limit)
	} else {
		since, okS := parseOptInt(q.Get("since"), 0)
		if !okS {
			writeErr(w, http.StatusBadRequest, "invalid since")
			return
		}
		until, okU := parseOptInt(q.Get("until"), 0)
		if !okU {
			writeErr(w, http.StatusBadRequest, "invalid until")
			return
		}
		rows, err = h.st.QueryDataLimited(id, since, until, kind, limit)
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	out := make([]telemetryRow, 0, len(rows))
	for _, dr := range rows {
		out = append(out, telemetryRow{
			ID: dr.ID, TS: dr.TS, Seq: dr.Seq, Kind: dr.Kind,
			Name: dr.Name, Value: dr.Value, Text: dr.Text, ValueType: dr.ValueType,
		})
	}
	writeOK(w, map[string]any{"rows": out})
}
```

- [ ] **Step 4: Register the route**

In `internal/apisrv/apisrv.go`, in `Register`, add after the `commands` GET line (`apisrv.go:53`):

```go
	mux.HandleFunc("GET /api/nodes/{sel}/telemetry", recoverer(h.handleTelemetry))
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/apisrv/ -v`
Expected: PASS (new Telemetry tests + existing apisrv tests).

- [ ] **Step 6: Commit**

```bash
git add internal/apisrv/telemetry.go internal/apisrv/apisrv.go internal/apisrv/telemetry_test.go
git commit -m "feat(apisrv): GET /api/nodes/{sel}/telemetry (window + id cursor)"
```

---

## Task 3: apiclient — telemetry read methods

**Files:**
- Modify: `internal/apiclient/client.go` (add DataRow + methods near the existing reads)
- Test: `internal/apiclient/telemetry_test.go` (create)

- [ ] **Step 1: Write the failing tests**

Create `internal/apiclient/telemetry_test.go`:

```go
package apiclient

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// telemetryStub serves a fixed {ok,data:{rows}} envelope and records the last
// query string so the test can assert the params the client sent.
func telemetryStub(t *testing.T, body string) (*Client, *string) {
	t.Helper()
	var lastQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL), &lastQuery
}

func TestQueryTelemetryWindowDecodesTypes(t *testing.T) {
	body := `{"ok":true,"data":{"rows":[
		{"id":1,"ts":100,"seq":0,"kind":"metric","name":"pm","value":13,"text":"","value_type":"int"},
		{"id":2,"ts":101,"seq":1,"kind":"metric","name":"t","value":20.5,"text":"","value_type":"float"},
		{"id":3,"ts":102,"seq":2,"kind":"metric","name":"door","value":1,"text":"","value_type":"bool"},
		{"id":4,"ts":103,"seq":3,"kind":"metric","name":"mode","value":null,"text":"auto","value_type":"string"},
		{"id":5,"ts":104,"seq":4,"kind":"log","name":"","value":null,"text":"started","value_type":""}
	]},"error":""}`
	c, lastQuery := telemetryStub(t, body)
	rows, err := c.QueryTelemetryWindow("dev", 50, 200, "metric", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 5 {
		t.Fatalf("got %d rows, want 5", len(rows))
	}
	if v, ok := rows[0].Value.(int64); !ok || v != 13 {
		t.Errorf("row0 value = %#v, want int64 13", rows[0].Value)
	}
	if v, ok := rows[1].Value.(float64); !ok || v != 20.5 {
		t.Errorf("row1 value = %#v, want float64 20.5", rows[1].Value)
	}
	if v, ok := rows[2].Value.(int64); !ok || v != 1 {
		t.Errorf("row2 (bool) value = %#v, want int64 1", rows[2].Value)
	}
	if rows[3].Value != nil || rows[3].Text != "auto" {
		t.Errorf("row3 (string) = %#v / %q, want nil / auto", rows[3].Value, rows[3].Text)
	}
	if rows[4].Value != nil || rows[4].ID != 5 {
		t.Errorf("row4 (log) = %#v / id %d", rows[4].Value, rows[4].ID)
	}
	if got := *lastQuery; got != "kind=metric&limit=10&since=50&until=200" {
		t.Errorf("window query = %q", got)
	}
}

func TestQueryTelemetryAfterQuery(t *testing.T) {
	c, lastQuery := telemetryStub(t, `{"ok":true,"data":{"rows":[]},"error":""}`)
	if _, err := c.QueryTelemetryAfter("dev", 7, "", 0); err != nil {
		t.Fatal(err)
	}
	if got := *lastQuery; got != "after=7" {
		t.Errorf("after query = %q, want after=7", got)
	}
}

func TestQueryTelemetryServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte(`{"ok":false,"data":null,"error":"unknown node"}`))
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL)
	if _, err := c.QueryTelemetryWindow("nope", 0, 0, "", 0); err == nil || err.Error() != "unknown node" {
		t.Fatalf("err = %v, want \"unknown node\"", err)
	}
}
```

Note the expected query strings: `url.Values.Encode()` sorts keys alphabetically, so window → `kind&limit&since&until`, and `until`/`kind`/`limit` are omitted when zero/empty.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/apiclient/ -run Telemetry -v`
Expected: FAIL — `QueryTelemetryWindow`/`DataRow` undefined (compile error).

- [ ] **Step 3: Add the wire type, decode helper, and methods**

In `internal/apiclient/client.go`, append:

```go
// DataRow is one telemetry row returned by the telemetry reads. Value is the
// typed scalar reconstructed from value_type: int64 for int/bool, float64 for
// float, nil for string & log rows (their payload is in Text).
type DataRow struct {
	ID        int64
	TS        int64
	Seq       int64
	Kind      string
	Name      string
	Value     any
	Text      string
	ValueType string
}

// wireRow is the on-the-wire shape; Value stays raw so typedValue can coerce it
// by value_type without losing int64 precision through a float.
type wireRow struct {
	ID        int64           `json:"id"`
	TS        int64           `json:"ts"`
	Seq       int64           `json:"seq"`
	Kind      string          `json:"kind"`
	Name      string          `json:"name"`
	Value     json.RawMessage `json:"value"`
	Text      string          `json:"text"`
	ValueType string          `json:"value_type"`
}

// typedValue coerces a raw JSON value to the Go type FormatLine expects for the
// given value_type: int64 for int/bool, float64 for float, nil otherwise (a
// JSON null, a string row, or an unknown tag — the payload then lives in Text).
func typedValue(valueType string, raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	switch valueType {
	case "float":
		var f float64
		if json.Unmarshal(raw, &f) == nil {
			return f
		}
	case "int", "bool":
		var i int64
		if json.Unmarshal(raw, &i) == nil {
			return i
		}
		var f float64
		if json.Unmarshal(raw, &f) == nil {
			return f
		}
	}
	return nil
}

// QueryTelemetryWindow reads the ts window [since, until] (until<=0 = unbounded)
// for sel, optionally filtered by kind and capped by limit. Used for monitor's
// initial look-back.
func (c *Client) QueryTelemetryWindow(sel string, since, until int64, kind string, limit int) ([]DataRow, error) {
	q := url.Values{}
	q.Set("since", strconv.FormatInt(since, 10))
	if until > 0 {
		q.Set("until", strconv.FormatInt(until, 10))
	}
	if kind != "" {
		q.Set("kind", kind)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	return c.getTelemetry(sel, q)
}

// QueryTelemetryAfter tails rows with id > after (ordered by id) for sel. Used
// for monitor --follow polls: exact dedup, no timestamp boundary case.
func (c *Client) QueryTelemetryAfter(sel string, after int64, kind string, limit int) ([]DataRow, error) {
	q := url.Values{}
	q.Set("after", strconv.FormatInt(after, 10))
	if kind != "" {
		q.Set("kind", kind)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	return c.getTelemetry(sel, q)
}

// getTelemetry GETs /api/nodes/{sel}/telemetry with q and decodes the rows.
func (c *Client) getTelemetry(sel string, q url.Values) ([]DataRow, error) {
	u := c.baseURL + "/api/nodes/" + url.PathEscape(sel) + "/telemetry"
	if enc := q.Encode(); enc != "" {
		u += "?" + enc
	}
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	data, err := c.do(req)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Rows []wireRow `json:"rows"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	out := make([]DataRow, 0, len(resp.Rows))
	for _, w := range resp.Rows {
		out = append(out, DataRow{
			ID: w.ID, TS: w.TS, Seq: w.Seq, Kind: w.Kind, Name: w.Name,
			Value: typedValue(w.ValueType, w.Value), Text: w.Text, ValueType: w.ValueType,
		})
	}
	return out, nil
}
```

`strconv` and `net/url` are already imported in `client.go`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/apiclient/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/apiclient/client.go internal/apiclient/telemetry_test.go
git commit -m "feat(apiclient): QueryTelemetryWindow/After telemetry reads"
```

---

## Task 4: CLI — `monitor` over the API

**Files:**
- Modify: `internal/portacli/monitor.go` (rewrite `runMonitor` + `newMonitorCmd`)
- Modify: `internal/portacli/client.go:11-12` (comment)
- Test: `internal/portacli/monitor_test.go` (rewrite)

- [ ] **Step 1: Rewrite the tests for the new signature**

Replace the entire contents of `internal/portacli/monitor_test.go`:

```go
// internal/portacli/monitor_test.go
package portacli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/davidg238/porta/internal/apiclient"
)

// fakeReader is an in-memory telemetryReader. Window returns `window`; each
// After call pops the next batch from `after` and records the cursor it saw.
type fakeReader struct {
	window     []apiclient.DataRow
	after      [][]apiclient.DataRow
	afterCalls []int64
}

func (f *fakeReader) QueryTelemetryWindow(sel string, since, until int64, kind string, limit int) ([]apiclient.DataRow, error) {
	return f.window, nil
}

func (f *fakeReader) QueryTelemetryAfter(sel string, after int64, kind string, limit int) ([]apiclient.DataRow, error) {
	f.afterCalls = append(f.afterCalls, after)
	if len(f.after) == 0 {
		return nil, nil
	}
	batch := f.after[0]
	f.after = f.after[1:]
	return batch, nil
}

func dr(id, ts int64, name string, value any, vtype string) apiclient.DataRow {
	return apiclient.DataRow{ID: id, TS: ts, Seq: id, Kind: "metric", Name: name, Value: value, ValueType: vtype}
}

func TestRunMonitorWindowPrintsAllScalars(t *testing.T) {
	f := &fakeReader{window: []apiclient.DataRow{
		dr(1, 100, "pm", int64(13), "int"),
		dr(2, 101, "t", float64(20.5), "float"),
		dr(3, 102, "door", int64(1), "bool"),
		{ID: 4, TS: 103, Kind: "metric", Name: "mode", Value: nil, Text: "auto", ValueType: "string"},
		{ID: 5, TS: 104, Kind: "log", Text: "started blink"},
	}}
	var out bytes.Buffer
	now := func() int64 { return 200 }
	if err := runMonitor(context.Background(), &out, f, "dev", 200, "", false, now, 10*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"100  metric  pm=13",
		"101  metric  t=20.5",
		"102  metric  door=true",
		"103  metric  mode=auto",
		"104  log     started blink",
	}
	got := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d: %q", len(got), len(want), out.String())
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRunMonitorFollowDedupsByID(t *testing.T) {
	f := &fakeReader{
		window: []apiclient.DataRow{dr(1, 100, "pm", int64(13), "int"), dr(2, 101, "pm", int64(14), "int")},
		after:  [][]apiclient.DataRow{{dr(3, 102, "pm", int64(15), "int")}},
	}
	var out bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	now := func() int64 { return 200 }
	done := make(chan error, 1)
	go func() {
		done <- runMonitor(ctx, &out, f, "dev", 200, "", true, now, 5*time.Millisecond)
	}()
	// Give the loop time to poll at least twice, then cancel.
	time.Sleep(40 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runMonitor returned %v, want nil", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("runMonitor --follow did not exit after cancel")
	}
	// id=3 printed exactly once (no re-print across polls).
	if n := strings.Count(out.String(), "pm=15"); n != 1 {
		t.Fatalf("pm=15 printed %d times, want 1\n%s", n, out.String())
	}
	// First After poll uses the window's max id (2), not 0.
	if len(f.afterCalls) == 0 || f.afterCalls[0] != 2 {
		t.Fatalf("first after cursor = %v, want 2", f.afterCalls)
	}
}

func TestRunMonitorKindFilterPassedThrough(t *testing.T) {
	f := &fakeReader{window: []apiclient.DataRow{{ID: 1, TS: 100, Kind: "log", Text: "hi"}}}
	var out bytes.Buffer
	now := func() int64 { return 200 }
	if err := runMonitor(context.Background(), &out, f, "dev", 200, "log", false, now, 10*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "hi") {
		t.Errorf("out = %q", out.String())
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/portacli/ -run RunMonitor -v`
Expected: FAIL — `runMonitor`'s signature still takes `*store.Store` (compile error: `*fakeReader` is not `*store.Store`).

- [ ] **Step 3: Rewrite `monitor.go`**

Replace the entire contents of `internal/portacli/monitor.go`:

```go
// internal/portacli/monitor.go
package portacli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/davidg238/porta/internal/apiclient"
	"github.com/davidg238/porta/internal/command"
	"github.com/davidg238/porta/internal/store"
	"github.com/davidg238/porta/internal/telemetry"
	"github.com/spf13/cobra"
)

// telemetryReader is the slice of apiclient.Client that monitor needs, so the
// follow loop can be tested with an in-memory fake.
type telemetryReader interface {
	QueryTelemetryWindow(sel string, since, until int64, kind string, limit int) ([]apiclient.DataRow, error)
	QueryTelemetryAfter(sel string, after int64, kind string, limit int) ([]apiclient.DataRow, error)
}

// toStoreRow adapts an apiclient.DataRow to the store.DataRow telemetry.FormatLine
// expects, so monitor's output is byte-for-byte identical to the db-backed past.
func toStoreRow(r apiclient.DataRow) store.DataRow {
	return store.DataRow{
		TS: r.TS, Seq: r.Seq, Kind: r.Kind, Name: r.Name,
		Value: r.Value, Text: r.Text, ValueType: r.ValueType,
	}
}

// runMonitor is the testable core of `porta monitor`. It prints the node's
// telemetry over the API: first the ts window [now-sinceS, now], then — if
// follow — it polls every pollInterval for rows with id past the highest id
// seen, advancing an exact id cursor (no timestamp-tie boundary case). It
// returns nil on ctx cancellation (Ctrl-C).
func runMonitor(ctx context.Context, out io.Writer, c telemetryReader,
	sel string, sinceS int64, kind string, follow bool,
	now func() int64, pollInterval time.Duration,
) error {
	until := now()
	rows, err := c.QueryTelemetryWindow(sel, until-sinceS, until, kind, 0)
	if err != nil {
		return err
	}
	var cursor int64
	for _, r := range rows {
		fmt.Fprintln(out, telemetry.FormatLine(toStoreRow(r)))
		if r.ID > cursor {
			cursor = r.ID
		}
	}
	if !follow {
		return nil
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			if ctx.Err() == context.Canceled {
				return nil
			}
			return ctx.Err()
		case <-ticker.C:
			rows, err := c.QueryTelemetryAfter(sel, cursor, kind, 0)
			if err != nil {
				return err
			}
			for _, r := range rows {
				fmt.Fprintln(out, telemetry.FormatLine(toStoreRow(r)))
				if r.ID > cursor {
					cursor = r.ID
				}
			}
		}
	}
}

func newMonitorCmd() *cobra.Command {
	var device, since, kind string
	var follow bool
	cmd := &cobra.Command{
		Use:   "monitor",
		Short: "Print a node's telemetry over the API; --follow tails new rows",
		RunE: func(cmd *cobra.Command, _ []string) error {
			sinceS := int64(3600)
			if since != "" {
				s, err := command.ParseDurationSeconds(since)
				if err != nil {
					return err
				}
				sinceS = s
			}
			c := apiclient.New(serverURL())
			return runMonitor(cmd.Context(), cmd.OutOrStdout(), c, device, sinceS, kind, follow, nowSec, 2*time.Second)
		},
	}
	deviceFlag(cmd, &device)
	cmd.Flags().StringVar(&since, "since", "", "look-back window, e.g. 30m, 1h (default 1h)")
	cmd.Flags().StringVar(&kind, "kind", "", "filter to 'log' or 'metric'")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "poll the server and tail new rows")
	return cmd
}
```

Note: the selector is now sent **raw** to the server (no `resolveNodeID`); the server resolves it and returns a clean 404 error for an unknown node. `monitor` no longer opens the store.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/portacli/ -run RunMonitor -v`
Expected: PASS.

- [ ] **Step 5: Add a cobra e2e test against a real server**

Append to `internal/portacli/monitor_test.go`:

```go
func TestMonitorCmdE2EOverAPI(t *testing.T) {
	st, err := storeOpenForTest(t)
	if err != nil {
		t.Fatal(err)
	}
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	st.InsertData("aabbccddeeff", 100, 0, "metric", "pm", int64(42), "", "int")
	srv := apiServerForTest(t, st)

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"--server", srv.URL, "monitor", "-d", "aabbccddeeff", "--since", "1h"})
	if err := root.Execute(); err != nil {
		t.Fatalf("monitor cmd: %v", err)
	}
	if !strings.Contains(out.String(), "metric  pm=42") {
		t.Fatalf("out = %q", out.String())
	}
}
```

This reuses two existing test helpers. Confirm their names in `internal/portacli/` (mutate_test.go / run_test.go set up `store.Open(t.TempDir()+...)` and an `httptest` server over `apisrv.New(st).Register(mux)`). If helpers named `storeOpenForTest`/`apiServerForTest` do not already exist, inline them here:

```go
func storeOpenForTest(t *testing.T) (*store.Store, error) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/e2e.db")
	if err == nil {
		t.Cleanup(func() { st.Close() })
	}
	return st, err
}

func apiServerForTest(t *testing.T, st *store.Store) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	apisrv.New(st).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}
```

…and add the imports `net/http`, `net/http/httptest`, `github.com/davidg238/porta/internal/apisrv`, `github.com/davidg238/porta/internal/store` to the test file.

- [ ] **Step 6: Update the stale comment in `client.go`**

In `internal/portacli/client.go`, the `serverURL` doc comment says "Only the 8 mutating commands consume it; reads stay db-backed." Update it:

```go
// serverURL resolves the porta server base URL: --server, then $PORTA_SERVER,
// then http://localhost:6970 (matches serve's default --http-port). The
// mutating commands and `monitor` consume it; the remaining reads stay db-backed.
func serverURL() string {
```

- [ ] **Step 7: Run the full suite to verify it passes**

Run: `go test ./... `
Expected: PASS across all packages.

- [ ] **Step 8: Commit**

```bash
git add internal/portacli/monitor.go internal/portacli/monitor_test.go internal/portacli/client.go
git commit -m "feat(portacli): porta monitor reads over the control-plane API"
```

---

## Self-review notes (for the executor)

- **Spec coverage:** §2 cursor → Task 1 (`QueryDataAfter`) + Task 4 (cursor loop). §3 endpoint/params/envelope → Task 2. §4 store → Task 1. §5 handler → Task 2. §6 apiclient → Task 3. §7 CLI → Task 4. §8 errors → Tasks 2 (400/404) + 3 (transport/server-error). §9 testing → tests in every task.
- **`--db` retirement:** `monitor` simply stops calling `openStore()`; the `--db` persistent flag stays (other reads + `serve` use it). Nothing to remove.
- **Format parity:** `telemetry.FormatLine` is unchanged; `toStoreRow` feeds it. The Task 4 window test reuses the exact expected lines from the old `monitor_test.go`.
- **Out of scope:** no SSE; MCP #10/#11 untouched; full `device get` detail read stays db-backed.
