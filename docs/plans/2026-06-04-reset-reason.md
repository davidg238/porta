# Reset Reason Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** porta ingests a neutral reset **category** from each node's `health` report, surfaces it on node detail (web/API/CLI), and emits a `data_log` event when a node reports a fault reset (watchdog/panic/brownout).

**Architecture:** The node maps its platform reset code onto a canonical neutral vocabulary and sends `health.reset` (string) + optional `health.reset_code` (int). porta stores the latest into two new `nodes` columns (mirroring the `chip`/`sdk` identity precedent), renders them on detail, and — on a *change to a fault category* — inserts one `data_log` row tagged `kind="reset"`. porta never interprets platform codes; it only owns the small "which categories are noteworthy" fault-set policy.

**Tech Stack:** Go (`internal/store`, `internal/handler`, `internal/control`, `internal/web`, `internal/apisrv`, `internal/portacli`, `devsdk/apiclient`), sqlite (mattn/go-sqlite3), cobra CLI, html/template.

**Spec:** `docs/specs/2026-06-04-reset-reason-design.md`

**Wire shape (canonical):**
```jsonc
"health": { "uptime_us": 1000000, "wakes": 7, "reset": "watchdog", "reset_code": 6 }
```
Vocabulary: `power-on`, `deep-sleep`, `software`, `external`, `watchdog`, `panic`, `brownout`, `unknown`. Fault set (porta policy): `{watchdog, panic, brownout}`.

**DB note:** Per `porta-no-legacy` (pre-1.0, no migrations), adding the two `nodes` columns is done by editing the `CREATE TABLE` DDL; an existing `porta.db` must be deleted so the schema recreates. The dev/test stores use fresh temp DBs, so tests are unaffected.

---

## File Structure

- `internal/store/store.go` — add `last_reset`/`last_reset_code` columns to the `nodes` DDL, two fields on `Node`, the columns to `nodeCols`/`scanNode`, a `nullInt` helper, and `UpdateNodeReset`.
- `internal/store/store_test.go` — `TestUpdateNodeReset`.
- `internal/control/view.go` — `RenderReset(cat string, code *int64) string` shared renderer.
- `internal/control/view_test.go` — `TestRenderReset` (create if absent).
- `internal/handler/handler.go` — parse `health.reset`/`reset_code` in `writeReport`, emit the fault event, call `UpdateNodeReset`; add the `faultReset` set + `parseResetHealth` helper.
- `internal/handler/handler_test.go` — `TestWriteReportStoresReset`, `TestWriteReportEmitsFaultEventOnce`, `TestWriteReportFaultThenNormalThenFault`.
- `internal/apisrv/nodes.go` — add `reset`/`reset_code` to the `nodeDetail` JSON.
- `internal/apisrv/nodes_test.go` — extend the detail test (if present) or add one.
- `devsdk/apiclient/client.go` — add `Reset`/`ResetCode` to `NodeDetailResp`.
- `internal/web/pages.go` — add `LastReset` to `detailVM` + populate via `RenderReset`.
- `internal/web/templates/node.html` — render the reset in the subtitle.
- `internal/portacli/inspect.go` — add a `last_reset:` line to `device show`.
- `docs/PROTOCOL.md` — document the `health.reset`/`reset_code` keys + vocabulary.

---

## Task 1: Store — columns, Node fields, `UpdateNodeReset`

**Files:**
- Modify: `internal/store/store.go` (DDL ~line 20-33, `Node` ~76-89, `nodeCols` ~155-157, `scanNode` ~159-168, `nullStr` ~122, `UpdateNodeIdentity` ~214-219)
- Test: `internal/store/store_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/store/store_test.go`:

```go
func TestUpdateNodeReset(t *testing.T) {
	st := openTmp(t)
	if err := st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000); err != nil {
		t.Fatal(err)
	}
	code := int64(6)
	if err := st.UpdateNodeReset("aabbccddeeff", "watchdog", &code); err != nil {
		t.Fatal(err)
	}
	n, err := st.GetNode("aabbccddeeff")
	if err != nil || n == nil {
		t.Fatalf("GetNode: %v / %v", n, err)
	}
	if n.LastReset != "watchdog" || !n.LastResetCode.Valid || n.LastResetCode.Int64 != 6 {
		t.Errorf("got reset=%q code=%v, want watchdog / 6", n.LastReset, n.LastResetCode)
	}
	// Empty category + nil code must not clobber a known value.
	if err := st.UpdateNodeReset("aabbccddeeff", "", nil); err != nil {
		t.Fatal(err)
	}
	n, _ = st.GetNode("aabbccddeeff")
	if n.LastReset != "watchdog" || n.LastResetCode.Int64 != 6 {
		t.Errorf("empty update clobbered reset: reset=%q code=%v", n.LastReset, n.LastResetCode)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestUpdateNodeReset -v`
Expected: FAIL — compile error (`UpdateNodeReset` / `LastReset` / `LastResetCode` undefined).

- [ ] **Step 3: Implement**

In the `nodes` `CREATE TABLE` DDL, change the `sdk TEXT` line to add the two columns:

```sql
  chip TEXT,
  sdk TEXT,
  last_reset TEXT,
  last_reset_code INTEGER
```

In the `Node` struct, after `Sdk string` add:

```go
	LastReset     string
	LastResetCode sql.NullInt64
```

Update `nodeCols` to append the two columns:

```go
const nodeCols = `id, COALESCE(name,''), COALESCE(source_addr,''), kind, first_seen, last_seen,
	COALESCE(poll_interval_s,30), COALESCE(max_offline_s,300), last_report_at,
	COALESCE(observed_state,''), COALESCE(chip,''), COALESCE(sdk,''),
	COALESCE(last_reset,''), last_reset_code`
```

Update `scanNode` to scan them (order matches `nodeCols`):

```go
	err := row.Scan(&n.ID, &n.Name, &n.SourceAddr, &n.Kind, &n.FirstSeen,
		&n.LastSeen, &n.PollIntervalS, &n.MaxOfflineS, &n.LastReportAt, &n.ObservedState,
		&n.Chip, &n.Sdk, &n.LastReset, &n.LastResetCode)
```

Add a `nullInt` helper next to `nullStr`:

```go
func nullInt(v *int64) interface{} {
	if v == nil {
		return nil
	}
	return *v
}
```

Add `UpdateNodeReset` next to `UpdateNodeIdentity`:

```go
// UpdateNodeReset records the node's last reported reset category + optional
// raw platform code. An empty category / nil code is COALESCEd so a report
// missing the field never clobbers a previously-known value.
func (s *Store) UpdateNodeReset(id, reset string, code *int64) error {
	_, err := s.db.Exec(
		`UPDATE nodes SET last_reset = COALESCE(?, last_reset),
		 last_reset_code = COALESCE(?, last_reset_code) WHERE id = ?`,
		nullStr(reset), nullInt(code), id)
	return err
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestUpdateNodeReset -v`
Expected: PASS.

- [ ] **Step 5: Run the full store package + build**

Run: `go test ./internal/store/ && go build ./...`
Expected: PASS / no output.

- [ ] **Step 6: Commit**

```bash
git add internal/store/store.go internal/store/store_test.go
git commit -m "feat(store): last_reset + last_reset_code columns and UpdateNodeReset"
```

---

## Task 2: control — `RenderReset` shared renderer

**Files:**
- Modify: `internal/control/view.go`
- Test: `internal/control/view_test.go` (create if absent)

- [ ] **Step 1: Write the failing test**

Add to `internal/control/view_test.go` (create the file with the package header if it does not exist — `package control`, import `testing`):

```go
func TestRenderReset(t *testing.T) {
	code := int64(6)
	cases := []struct {
		cat  string
		code *int64
		want string
	}{
		{"watchdog", &code, "watchdog (6)"},
		{"watchdog", nil, "watchdog"},
		{"", nil, "—"},
		{"", &code, "—"}, // no category → dash regardless of code
	}
	for _, c := range cases {
		if got := RenderReset(c.cat, c.code); got != c.want {
			t.Errorf("RenderReset(%q,%v) = %q, want %q", c.cat, c.code, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/control/ -run TestRenderReset -v`
Expected: FAIL — `RenderReset` undefined.

- [ ] **Step 3: Implement**

Add to `internal/control/view.go` (add `"fmt"` to the imports if not already present):

```go
// RenderReset formats a node's last reset for display: "category (code)" when a
// raw platform code is present, "category" when not, and "—" when no category
// has been reported. porta never interprets the category or code — it displays
// the neutral string the node sent.
func RenderReset(cat string, code *int64) string {
	if cat == "" {
		return "—"
	}
	if code != nil {
		return fmt.Sprintf("%s (%d)", cat, *code)
	}
	return cat
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/control/ -run TestRenderReset -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/control/view.go internal/control/view_test.go
git commit -m "feat(control): RenderReset helper for reset category + code"
```

---

## Task 3: Handler — ingest reset + emit fault event

**Files:**
- Modify: `internal/handler/handler.go` (`writeReport` ~144-179; add helpers near it)
- Test: `internal/handler/handler_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `internal/handler/handler_test.go`:

```go
func TestWriteReportStoresReset(t *testing.T) {
	h, st := newH(t)
	body := `{"apps":{},"config":{},"health":{"uptime_us":1,"wakes":1,"reset":"power-on","reset_code":1}}`
	if err := h.Write("report?id=dev", "1.2.3.4:5", []byte(body)); err != nil {
		t.Fatal(err)
	}
	n, _ := st.GetNode("dev")
	if n.LastReset != "power-on" || n.LastResetCode.Int64 != 1 {
		t.Fatalf("got reset=%q code=%v", n.LastReset, n.LastResetCode)
	}
	// power-on is not a fault → no data_log event.
	rows, _ := st.RecentData("dev", 10)
	for _, r := range rows {
		if r.Kind == "reset" {
			t.Errorf("unexpected reset event for non-fault category: %+v", r)
		}
	}
}

func TestWriteReportEmitsFaultEventOnce(t *testing.T) {
	h, st := newH(t)
	body := `{"apps":{},"config":{},"health":{"reset":"watchdog","reset_code":6}}`
	for i := 0; i < 3; i++ {
		if err := h.Write("report?id=dev", "1.2.3.4:5", []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	rows, _ := st.RecentData("dev", 10)
	n := 0
	for _, r := range rows {
		if r.Kind == "reset" {
			n++
			if r.Name != "watchdog" || r.Value.(int64) != 6 || r.Text != "watchdog" || r.ValueType != "int" {
				t.Errorf("bad reset row: %+v", r)
			}
		}
	}
	if n != 1 {
		t.Errorf("got %d reset events across 3 identical reports, want 1", n)
	}
}

func TestWriteReportFaultThenNormalThenFault(t *testing.T) {
	h, st := newH(t)
	fault := `{"apps":{},"config":{},"health":{"reset":"watchdog","reset_code":6}}`
	normal := `{"apps":{},"config":{},"health":{"reset":"deep-sleep","reset_code":8}}`
	for _, b := range []string{fault, normal, fault} {
		if err := h.Write("report?id=dev", "1.2.3.4:5", []byte(b)); err != nil {
			t.Fatal(err)
		}
	}
	rows, _ := st.RecentData("dev", 10)
	n := 0
	for _, r := range rows {
		if r.Kind == "reset" {
			n++
		}
	}
	if n != 2 {
		t.Errorf("got %d reset events for fault→normal→fault, want 2", n)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/handler/ -run 'TestWriteReport(StoresReset|EmitsFaultEventOnce|FaultThenNormalThenFault)' -v`
Expected: FAIL — reset is neither stored nor emitted (no `reset` event rows; `LastReset` empty).

- [ ] **Step 3: Implement**

In `internal/handler/handler.go`, add near the top of the file (after imports) the fault-set policy + parse helper:

```go
// faultReset is porta's policy set of "noteworthy" reset categories — the ones
// that trigger a data_log event. This is policy, not platform semantics: porta
// owns which neutral categories matter, never how a node derived them.
var faultReset = map[string]bool{"watchdog": true, "panic": true, "brownout": true}

// parseResetHealth extracts the neutral reset category and optional raw code
// from a report's health blob. Absent fields yield ("", nil).
func parseResetHealth(health json.RawMessage) (string, *int64) {
	var hb struct {
		Reset     string `json:"reset"`
		ResetCode *int64 `json:"reset_code"`
	}
	_ = json.Unmarshal(health, &hb) // best-effort; absent/garbled → zero value
	return hb.Reset, hb.ResetCode
}
```

In `writeReport`, after the `UpdateNodeIdentity` block and before `h.reconcileAfterReport(...)`, add:

```go
	// Reset reason: store the latest, and emit a data_log event the first time a
	// fault category appears (change-detection dedup against the stored value).
	reset, resetCode := parseResetHealth(field("health"))
	if faultReset[reset] {
		prior, _ := h.store.GetNode(id)
		if prior == nil || prior.LastReset != reset {
			var v any
			vtype := ""
			if resetCode != nil {
				v = *resetCode
				vtype = "int"
			}
			if err := h.store.InsertData(id, h.now(), 0, "reset", reset, v, reset, vtype); err != nil {
				h.log("porta: reset event insert error for %s: %v", id, err)
			}
		}
	}
	if err := h.store.UpdateNodeReset(id, reset, resetCode); err != nil {
		h.log("porta: reset update error for %s: %v", id, err)
	}
```

Note: `json` is already imported in `handler.go` (used by `writeReport`). `field` is the local closure already defined in `writeReport`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/handler/ -run 'TestWriteReport(StoresReset|EmitsFaultEventOnce|FaultThenNormalThenFault)' -v`
Expected: PASS.

- [ ] **Step 5: Run the full handler package + build**

Run: `go test ./internal/handler/ && go build ./...`
Expected: PASS / no output.

- [ ] **Step 6: Commit**

```bash
git add internal/handler/handler.go internal/handler/handler_test.go
git commit -m "feat(handler): ingest reset reason + emit data_log event on fault reset"
```

---

## Task 4: API + apiclient — surface reset on node detail

**Files:**
- Modify: `internal/apisrv/nodes.go` (`nodeDetail` struct ~54-74; `handleNodeDetail` ~94-101)
- Modify: `devsdk/apiclient/client.go` (`NodeDetailResp` ~374-389)
- Test: `internal/apisrv/nodes_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/apisrv/nodes_test.go`, mirroring the existing `TestGetNodeDetail` setup verbatim (`newTestHandler`, `h.Register(mux)`, decode the `{ok,data}` envelope):

```go
func TestGetNodeDetailIncludesReset(t *testing.T) {
	h, st := newTestHandler(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	code := int64(6)
	st.UpdateNodeReset("aabbccddeeff", "watchdog", &code)

	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest("GET", "/api/nodes/aabbccddeeff", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Reset     string `json:"reset"`
			ResetCode *int64 `json:"reset_code"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.Reset != "watchdog" || env.Data.ResetCode == nil || *env.Data.ResetCode != 6 {
		t.Errorf("reset=%q code=%v, want watchdog / 6", env.Data.Reset, env.Data.ResetCode)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/apisrv/ -run TestGetNodeDetailIncludesReset -v`
Expected: FAIL — `reset` / `reset_code` absent from the JSON.

- [ ] **Step 3: Implement**

In `internal/apisrv/nodes.go`, add to the `nodeDetail` struct (after `Sdk`):

```go
	Reset     string `json:"reset"`
	ResetCode *int64 `json:"reset_code"`
```

In `handleNodeDetail`, build the `*int64` from the store's `sql.NullInt64` and set the fields in the `writeOK(w, nodeDetail{...})` literal:

```go
	var resetCode *int64
	if n.LastResetCode.Valid {
		c := n.LastResetCode.Int64
		resetCode = &c
	}
```

Add to the struct literal (alongside `Chip: n.Chip, Sdk: n.Sdk`):

```go
		Reset: n.LastReset, ResetCode: resetCode,
```

In `devsdk/apiclient/client.go`, add to `NodeDetailResp` (after `Sdk`):

```go
	Reset         string `json:"reset"`
	ResetCode     *int64 `json:"reset_code"`
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/apisrv/ -run TestGetNodeDetailIncludesReset -v && go test ./devsdk/apiclient/`
Expected: PASS.

- [ ] **Step 5: Build**

Run: `go build ./...`
Expected: no output.

- [ ] **Step 6: Commit**

```bash
git add internal/apisrv/nodes.go internal/apisrv/nodes_test.go devsdk/apiclient/client.go
git commit -m "feat(api): surface reset/reset_code on node detail"
```

---

## Task 5: Web — render reset on the node detail page

**Files:**
- Modify: `internal/web/pages.go` (`detailVM` struct ~ Chip/Sdk fields; `detailVM()` builder ~108-146)
- Modify: `internal/web/templates/node.html` (subtitle line ~16)

- [ ] **Step 1: Implement (template render is verified by the build + a render smoke test)**

In `internal/web/pages.go`, add a field to the `detailVM` struct (after `Sdk string`):

```go
	LastReset string
```

In the `detailVM()` builder's returned struct literal (where `Chip: n.Chip, Sdk: n.Sdk` are set), add — converting the store's `sql.NullInt64` to `*int64` for `RenderReset`:

```go
	var resetCode *int64
	if n.LastResetCode.Valid {
		c := n.LastResetCode.Int64
		resetCode = &c
	}
```

and in the literal:

```go
		LastReset: control.RenderReset(n.LastReset, resetCode),
```

(`control` is already imported in `pages.go`.)

In `internal/web/templates/node.html` line 16, add a reset segment to the subtitle, after the `sdk` segment and before the `Telemetry →` link:

```html
 · reset {{if .LastReset}}{{.LastReset}}{{else}}—{{end}} ·
```

So the segment reads: `... sdk {{if .Sdk}}{{.Sdk}}{{else}}—{{end}} · reset {{if .LastReset}}{{.LastReset}}{{else}}—{{end}} · <a href="/telemetry?...">Telemetry →</a>`

- [ ] **Step 2: Verify build + existing web tests pass**

Run: `go test ./internal/web/ && go build ./...`
Expected: PASS / no output.

- [ ] **Step 3: Manual render smoke check**

Run: `grep -n "reset" internal/web/templates/node.html`
Expected: shows the new `reset {{if .LastReset}}` segment.

- [ ] **Step 4: Commit**

```bash
git add internal/web/pages.go internal/web/templates/node.html
git commit -m "feat(web): show last reset on the node detail page"
```

---

## Task 6: CLI — `device show` reset line

**Files:**
- Modify: `internal/portacli/inspect.go` (`newDeviceShowCmd` RunE ~127-149)

- [ ] **Step 1: Implement**

In `newDeviceShowCmd`'s `RunE`, after the `max_offline:` line and before the `observed:` line, add:

```go
				fmt.Fprintf(out, "last_reset:    %s\n", control.RenderReset(n.Reset, n.ResetCode))
```

(`control` is already imported in `inspect.go` — it is used by `runDeviceGet`. `n` is the `*apiclient.NodeDetailResp`, whose `Reset`/`ResetCode` were added in Task 4.)

- [ ] **Step 2: Verify build + package tests**

Run: `go test ./internal/portacli/ && go build ./...`
Expected: PASS / no output.

- [ ] **Step 3: Commit**

```bash
git add internal/portacli/inspect.go
git commit -m "feat(cli): show last_reset in device show"
```

---

## Task 7: PROTOCOL.md — document `health.reset` / `reset_code`

**Files:**
- Modify: `docs/PROTOCOL.md` (the report / observed-state section — the `health` block, near the `apps.<name>.lifecycle` table around line 241)

- [ ] **Step 1: Locate the health block doc**

Run: `grep -n "health\|uptime\|wakes" docs/PROTOCOL.md`
Expected: shows where the report `health` shape is described.

- [ ] **Step 2: Add the reset fields + vocabulary**

In the report-shape section, document the two new `health` keys. Add a paragraph + table:

```markdown
The `health` block additionally MAY carry a **reset reason**:

| Field | Type | Meaning |
|-------|------|---------|
| `health.reset` | string | Neutral reset category (vocabulary below). The node maps its own platform reset code onto this set. |
| `health.reset_code` | int (optional) | Raw platform reset code, for diagnostics only. The gateway never interprets it. |

Canonical `reset` vocabulary (the only permitted values):

| Category | Meaning |
|----------|---------|
| `power-on` | cold / power-on reset |
| `deep-sleep` | wake from deep sleep (normal duty-cycle wake) |
| `software` | software-requested reboot |
| `external` | external / reset-pin |
| `watchdog` | watchdog timeout (task or HW) |
| `panic` | software panic / exception |
| `brownout` | supply-voltage dip |
| `unknown` | unmapped / unavailable |

`reset`/`reset_code` are additive and optional: a report omitting them is valid and
the gateway keeps the last known value. The gateway surfaces the category on node
detail and emits a telemetry event on the fault categories (`watchdog`, `panic`,
`brownout`).
```

- [ ] **Step 3: Commit**

```bash
git add docs/PROTOCOL.md
git commit -m "docs(protocol): define neutral health.reset category + reset_code"
```

---

## Task 8: nodus handoff (coordination — not porta code)

**Files:**
- None in porta. This task posts a comment on nodus PR #7.

- [ ] **Step 1: Draft the handoff comment**

The comment must state: porta (protocol owner) has defined the wire shape as a neutral category, not the raw esp32 enum. nodus should emit:
- `health.reset` — neutral category string, mapping `esp32.reset-reason`:
  `1 → power-on`, `4 → panic`, `6 → watchdog`, `7 → watchdog`, `8 → deep-sleep`,
  brownout code → `brownout`, everything else → `unknown`.
- `health.reset_code` — the raw `esp32.reset-reason` int (diagnostic).
- `build-report` stays pure/host-testable: the category string + int are passed in
  like `uptime_us`/`wakes` (the supervisor reads `esp32.reset-reason`, maps, and passes
  both).
- Reference porta's `docs/PROTOCOL.md` reset vocabulary + `docs/specs/2026-06-04-reset-reason-design.md`.

- [ ] **Step 2: Post it (after the porta side is merged)**

Run:
```bash
gh pr comment 7 --repo davidg238/nodus --body "<the drafted handoff>"
```

- [ ] **Step 3: No commit** — coordination only.

---

## Final verification

- [ ] **Run the full suite + build:**

Run: `go test ./... && go build ./...`
Expected: all packages PASS, build clean.

- [ ] **Confirm no `porta.db` is committed** and note in the merge that an existing deployed `porta.db` must be deleted so the two new columns recreate (per `porta-no-legacy`).
