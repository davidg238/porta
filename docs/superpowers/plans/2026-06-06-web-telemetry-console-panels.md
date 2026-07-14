# Web telemetry console panels + node filter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show a node's prints and logs as raw console panels on its detail page, and add an all/node filter to the metrics telemetry page.

**Architecture:** A new store read `RecentByKinds` backs two lazy-loaded htmx panels (Prints / Logs) on the node page, rendered with the existing `telemetry.FormatLine` so they match the CLI `porta monitor`. The panels are self-contained (new `node_console.go` + `node_console.html`, two delegating cases in `handleNodeSub`, two placeholder sections in `node.html`) so telemetry stays cleanly excisable — the core node view-model is untouched. The metrics page gains a node `<select>` driving the existing `?node=` filter.

**Tech Stack:** Go, sqlite (mattn/go-sqlite3), stdlib `net/http` + `html/template`, htmx (polling). Test with `go test ./internal/store/ ./internal/web/`.

**Spec:** `docs/design/2026-06-06-web-telemetry-console-panels-design.md`. Built on branch `feat/set-forward` (needs the `level` column + `kind:"print"` from the set-forward work).

---

## File Structure

- `internal/store/data.go` — add `RecentByKinds` (node + kind-set filtered recent rows, newest-first, carries `Level`).
- `internal/web/node_console.go` — **new**: `consoleVM` + `renderNodeConsole` (the Prints/Logs panel handler). Telemetry-tagged, owned by the optional feature.
- `internal/web/templates/node_console.html` — **new**: `node-prints` / `node-logs` template defines.
- `internal/web/pages.go` — two delegating cases in `handleNodeSub`.
- `internal/web/templates/node.html` — two placeholder `<section>`s in the `node` template.
- `internal/web/templates/style.css` — `.console` rule.
- `internal/web/telemetry.go` — `nodeOpt` + `telemVM.Nodes` + build options.
- `internal/web/templates/telemetry.html` — node `<select>`.
- Tests: `internal/store/data_test.go`, `internal/web/web_test.go`.

Build order: store first (Task 1), then the node-page panels that consume it (Task 2), then the independent telemetry filter (Task 3).

---

## Task 1: `store.RecentByKinds`

**Files:**
- Modify: `internal/store/data.go` (add `RecentByKinds` near `RecentMetrics`)
- Test: `internal/store/data_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/store/data_test.go` (the package has an `openTestStore(t) *Store` helper; `InsertData` signature is `(deviceID, ts, seq int64, kind, name string, value any, text, valueType, level string)`):

```go
func TestRecentByKinds(t *testing.T) {
	st := openTestStore(t)
	// node A: a metric, a log (with level), a print, a panic
	_ = st.InsertData("aaaaaaaaaaaa", 100, 0, "metric", "pm", int64(7), "", "int", "")
	_ = st.InsertData("aaaaaaaaaaaa", 101, 0, "log", "", nil, "stall", "", "warn")
	_ = st.InsertData("aaaaaaaaaaaa", 102, 0, "print", "", nil, "hello", "", "")
	_ = st.InsertData("aaaaaaaaaaaa", 103, 0, "panic", "", nil, "blob", "", "")
	// node B: a log that must NOT appear in node A's results
	_ = st.InsertData("bbbbbbbbbbbb", 104, 0, "log", "", nil, "other", "", "error")

	// logs + panic for node A, newest first
	rows, err := st.RecentByKinds("aaaaaaaaaaaa", []string{"log", "panic"}, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows (log+panic), got %d: %+v", len(rows), rows)
	}
	if rows[0].Kind != "panic" || rows[1].Kind != "log" {
		t.Fatalf("want newest-first panic then log, got %q,%q", rows[0].Kind, rows[1].Kind)
	}
	if rows[1].Level != "warn" {
		t.Fatalf("want log level warn, got %q", rows[1].Level)
	}

	// prints only
	prints, _ := st.RecentByKinds("aaaaaaaaaaaa", []string{"print"}, 50)
	if len(prints) != 1 || prints[0].Text != "hello" {
		t.Fatalf("want 1 print 'hello', got %+v", prints)
	}

	// limit honored
	lim, _ := st.RecentByKinds("aaaaaaaaaaaa", []string{"log", "panic", "print"}, 1)
	if len(lim) != 1 {
		t.Fatalf("limit=1 want 1 row, got %d", len(lim))
	}

	// empty kinds → no rows, no error (never emit IN ())
	none, err := st.RecentByKinds("aaaaaaaaaaaa", nil, 50)
	if err != nil || len(none) != 0 {
		t.Fatalf("empty kinds want (0,nil), got (%d,%v)", len(none), err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestRecentByKinds`
Expected: FAIL — `RecentByKinds` undefined.

- [ ] **Step 3: Implement**

Add to `internal/store/data.go` (after `RecentMetrics`). Note `strings` is not yet imported in this file — the only existing import is `strconv`; add `strings`:

```go
// RecentByKinds returns the device's newest <= limit rows whose kind is in
// kinds, newest first. Backs the per-node console panels (prints / logs).
// Empty kinds returns no rows (never emits an `IN ()`).
func (s *Store) RecentByKinds(deviceID string, kinds []string, limit int) ([]DataRow, error) {
	if len(kinds) == 0 {
		return nil, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(kinds)), ",")
	q := `SELECT ts, seq, COALESCE(kind,''), COALESCE(name,''), value, COALESCE(text,''), COALESCE(value_type,''), COALESCE(level,'')
		  FROM data_log WHERE device_id = ? AND kind IN (` + ph + `)
		  ORDER BY ts DESC, seq DESC LIMIT ?`
	args := make([]any, 0, len(kinds)+2)
	args = append(args, deviceID)
	for _, k := range kinds {
		args = append(args, k)
	}
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DataRow
	for rows.Next() {
		var r DataRow
		var v any
		if err := rows.Scan(&r.TS, &r.Seq, &r.Kind, &r.Name, &v, &r.Text, &r.ValueType, &r.Level); err != nil {
			return nil, err
		}
		r.Value = normalizeNumeric(v)
		out = append(out, r)
	}
	return out, rows.Err()
}
```

Update the import block at the top of `internal/store/data.go` from `import "strconv"` to:

```go
import (
	"strconv"
	"strings"
)
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/store/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/data.go internal/store/data_test.go
git commit -m "feat(store): RecentByKinds for per-node console panels"
```

---

## Task 2: Node-page Prints / Logs console panels

**Files:**
- Create: `internal/web/node_console.go`
- Create: `internal/web/templates/node_console.html`
- Modify: `internal/web/pages.go` (`handleNodeSub` switch, ~lines 71-98)
- Modify: `internal/web/templates/node.html` (the `node` template, ~lines 2-11)
- Modify: `internal/web/templates/style.css` (add `.console`)
- Test: `internal/web/web_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `internal/web/web_test.go` (harness: `testStore(t)`, `serve(t, st)` → `*httptest.Server`, `readBody(t, resp)`, `mustGet(t, url)`; `InsertData` is 9-arg incl. trailing `level`):

```go
func TestNodeConsolePanels(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	_ = st.InsertData("aabbccddeeff", 1001, 0, "print", "", nil, "hello world", "", "")
	_ = st.InsertData("aabbccddeeff", 1002, 0, "log", "", nil, "pump stalled", "", "warn")
	_ = st.InsertData("aabbccddeeff", 1003, 0, "panic", "", nil, "traceblob", "", "")
	srv := serve(t, st)

	prints := readBody(t, mustGet(t, srv.URL+"/n/aabbccddeeff/prints"))
	if !strings.Contains(prints, `id="prints"`) || !strings.Contains(prints, "hello world") {
		t.Errorf("prints panel missing content: %s", prints)
	}
	if strings.Contains(prints, "pump stalled") {
		t.Errorf("prints panel must not contain log lines: %s", prints)
	}

	logs := readBody(t, mustGet(t, srv.URL+"/n/aabbccddeeff/logs"))
	if !strings.Contains(logs, `id="logs"`) || !strings.Contains(logs, "[warn] pump stalled") {
		t.Errorf("logs panel missing leveled log: %s", logs)
	}
	if !strings.Contains(logs, "traceblob") {
		t.Errorf("logs panel should include panic rows: %s", logs)
	}
	if strings.Contains(logs, "hello world") {
		t.Errorf("logs panel must not contain prints: %s", logs)
	}
}

func TestNodeConsoleEmptyHint(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	srv := serve(t, st)
	prints := readBody(t, mustGet(t, srv.URL+"/n/aabbccddeeff/prints"))
	if !strings.Contains(prints, "no prints") {
		t.Errorf("want empty hint, got: %s", prints)
	}
}

func TestNodeConsoleEscapesText(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	_ = st.InsertData("aabbccddeeff", 1001, 0, "print", "", nil, "<script>x</script>", "", "")
	srv := serve(t, st)
	prints := readBody(t, mustGet(t, srv.URL+"/n/aabbccddeeff/prints"))
	if strings.Contains(prints, "<script>x</script>") {
		t.Errorf("console text must be HTML-escaped: %s", prints)
	}
}

func TestNodePageEmbedsConsolePlaceholders(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	srv := serve(t, st)
	body := readBody(t, mustGet(t, srv.URL+"/n/aabbccddeeff"))
	if !strings.Contains(body, `hx-get="/n/aabbccddeeff/prints"`) ||
		!strings.Contains(body, `hx-get="/n/aabbccddeeff/logs"`) {
		t.Errorf("node page missing console placeholders: %s", body)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/web/ -run 'TestNodeConsole|TestNodePageEmbeds'`
Expected: FAIL — routes `/n/{id}/prints` and `/n/{id}/logs` 404 (default case), and the node page has no placeholders.

- [ ] **Step 3: Create `internal/web/node_console.go`**

```go
// Copyright (c) 2026 Ekorau LLC

// node_console.go is part of porta's OPTIONAL telemetry surface: the per-node
// Prints/Logs console panels on the node detail page. It is self-contained so
// telemetry can be excised in one bounded change (see telemetry.go's recipe):
// delete this file + node_console.html, the two cases in pages.go's
// handleNodeSub, and the telemetry:node-console block in node.html.
package web

import (
	"net/http"

	"github.com/davidg238/porta/internal/store"
	"github.com/davidg238/porta/internal/telemetry"
)

// consoleVM backs the node-prints / node-logs templates. Lines are pre-formatted
// console rows (via telemetry.FormatLine), in chronological order (oldest→newest)
// so the newest line sits at the bottom, like a terminal tail.
type consoleVM struct {
	ID    string
	Title string
	Lines []string
	Empty string
}

// renderNodeConsole renders one console panel (def is "node-prints"/"node-logs")
// for the node, showing the newest 50 rows of the given kinds.
func (h *Handler) renderNodeConsole(w http.ResponseWriter, n *store.Node, def, title, empty string, kinds []string) {
	rows, err := h.st.RecentByKinds(n.ID, kinds, 50)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	lines := make([]string, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- { // newest-first → chronological
		lines = append(lines, telemetry.FormatLine(rows[i]))
	}
	h.render(w, def, consoleVM{ID: n.ID, Title: title, Lines: lines, Empty: empty})
}
```

- [ ] **Step 4: Create `internal/web/templates/node_console.html`**

```html
<!-- Copyright (c) 2026 Ekorau LLC -->
<!-- OPTIONAL telemetry surface: per-node Prints/Logs console panels. -->
{{define "node-prints"}}<section id="prints" hx-get="/n/{{.ID}}/prints" hx-trigger="every 3s" hx-swap="outerHTML">
<h2>{{.Title}}</h2>
{{if .Lines}}<pre class="console">{{range .Lines}}{{.}}
{{end}}</pre>{{else}}<p class="subtitle">{{.Empty}}</p>{{end}}
</section>{{end}}

{{define "node-logs"}}<section id="logs" hx-get="/n/{{.ID}}/logs" hx-trigger="every 3s" hx-swap="outerHTML">
<h2>{{.Title}}</h2>
{{if .Lines}}<pre class="console">{{range .Lines}}{{.}}
{{end}}</pre>{{else}}<p class="subtitle">{{.Empty}}</p>{{end}}
</section>{{end}}
```

- [ ] **Step 5: Add the two delegating cases in `internal/web/pages.go`**

In `handleNodeSub`'s `switch sub {` (after the `case "containers":` block, before `case "max-offline", "rename":`):

```go
		// telemetry (optional): per-node console panels — see node_console.go
		case "prints":
			h.renderNodeConsole(w, n, "node-prints", "Prints",
				"no prints — forwarding may be off (set-forward --print on)", []string{"print"})
		case "logs":
			h.renderNodeConsole(w, n, "node-logs", "Logs",
				"no logs — forwarding may be off (set-forward --log on)", []string{"log", "panic"})
```

- [ ] **Step 6: Add the placeholder sections in `internal/web/templates/node.html`**

In the `{{define "node"}}` block, after `{{template "node-containers" .}}` and before `{{template "foot" .}}`:

```html
{{/* telemetry:node-console begin (optional; see node_console.go) */}}
<section id="prints" hx-get="/n/{{.ID}}/prints" hx-trigger="load, every 3s" hx-swap="outerHTML">
  <h2>Prints</h2><p class="subtitle">loading…</p></section>
<section id="logs" hx-get="/n/{{.ID}}/logs" hx-trigger="load, every 3s" hx-swap="outerHTML">
  <h2>Logs</h2><p class="subtitle">loading…</p></section>
{{/* telemetry:node-console end */}}
```

- [ ] **Step 7: Add the `.console` style in `internal/web/templates/style.css`**

Append:

```css
.console { font-family: ui-monospace, Menlo, Consolas, monospace; font-size: 12px;
  white-space: pre; overflow: auto; max-height: 16em; background: #111; color: #ddd;
  padding: 8px; border-radius: 4px; margin: 0; }
```

- [ ] **Step 8: Run tests**

Run: `go test ./internal/web/...`
Expected: PASS (the new console tests + all existing web tests). Then `go build ./...`.

- [ ] **Step 9: Commit**

```bash
git add internal/web/node_console.go internal/web/templates/node_console.html internal/web/pages.go internal/web/templates/node.html internal/web/templates/style.css internal/web/web_test.go
git commit -m "feat(web): per-node Prints/Logs console panels (optional telemetry surface)"
```

---

## Task 3: Telemetry-page all/node filter

**Files:**
- Modify: `internal/web/telemetry.go` (`telemVM` struct + `telemVM(...)` builder)
- Modify: `internal/web/templates/telemetry.html` (add `<select>`)
- Test: `internal/web/web_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/web/web_test.go`:

```go
func TestTelemetryNodeFilterSelect(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	_ = st.SetNodeName("aabbccddeeff", "fwkb")
	st.TouchNode("ccddeeff0011", "192.168.1.10", 1000)
	_ = st.SetNodeName("ccddeeff0011", "vin")
	srv := serve(t, st)

	// unfiltered: All nodes selected, both options present
	all := readBody(t, mustGet(t, srv.URL+"/telemetry"))
	if !strings.Contains(all, `name="node"`) ||
		!strings.Contains(all, ">All nodes<") ||
		!strings.Contains(all, ">fwkb<") || !strings.Contains(all, ">vin<") {
		t.Errorf("telemetry select missing options: %s", all)
	}

	// filtered: the chosen node's option is marked selected
	one := readBody(t, mustGet(t, srv.URL+"/telemetry?node=aabbccddeeff"))
	if !strings.Contains(one, `value="aabbccddeeff" selected`) {
		t.Errorf("selected option not marked: %s", one)
	}
}
```

(If `SetNodeName` is not the store method used elsewhere to set a friendly name, use whatever the other web tests use; confirm via `grep -n "func (s \*Store) SetNodeName" internal/store/*.go`.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/web/ -run TestTelemetryNodeFilterSelect`
Expected: FAIL — no `name="node"` select in the telemetry page.

- [ ] **Step 3: Implement the VM in `internal/web/telemetry.go`**

Add the option type and field. Place `nodeOpt` near `telemVM`:

```go
type nodeOpt struct {
	ID, Name string
	Selected bool
}
```

Add `Nodes []nodeOpt` to `telemVM`:

```go
type telemVM struct {
	Title  string
	Node   string
	NodeID string
	Nodes  []nodeOpt
	Rows   []telemRowVM
}
```

In `telemVM(nodeID string, now int64)`, after building `names`/`vm`, populate `vm.Nodes` (preserve `ListNodes` order; "All nodes" handled by the template's static option):

```go
	nodes, err := h.st.ListNodes()
	if err != nil {
		return telemVM{}, err
	}
	for _, n := range nodes {
		vm.Nodes = append(vm.Nodes, nodeOpt{ID: n.ID, Name: n.Name, Selected: n.ID == nodeID})
	}
```

(`nodeNames()` already calls `ListNodes()`; this is a second small call — acceptable, and keeps `nodeNames` reusable. Do not refactor `nodeNames`.)

- [ ] **Step 4: Add the `<select>` in `internal/web/templates/telemetry.html`**

In the `{{define "telemetry"}}` block, between the `<h1>` and the `<table>`:

```html
<form>
<select name="node" hx-get="/partials/telemetry" hx-target="#telem" hx-swap="outerHTML" hx-trigger="change">
  <option value=""{{if not .NodeID}} selected{{end}}>All nodes</option>
  {{range .Nodes}}<option value="{{.ID}}"{{if .Selected}} selected{{end}}>{{.Name}}</option>{{end}}
</select>
</form>
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/web/...`
Expected: PASS. Then `go build ./...`.

- [ ] **Step 6: Commit**

```bash
git add internal/web/telemetry.go internal/web/templates/telemetry.html internal/web/web_test.go
git commit -m "feat(web): all/node filter select on the telemetry page"
```

---

## Final verification

- [ ] **Full suite + build + vet**

Run: `go test ./... && go build ./... && go vet ./...`
Expected: ALL PASS, clean.

- [ ] **Manual smoke (optional, with a local server)**

```bash
go build -o /tmp/porta ./cmd/porta
/tmp/porta serve --db /tmp/smoke.db &   # then open http://localhost:6970/n/<id> and /telemetry
```
Expected: node page shows Prints + Logs panels (empty hints with no data); telemetry page shows the node dropdown.

- [ ] **Finish the branch** — this stacks on the set-forward work; merge the whole `feat/set-forward` branch via superpowers:finishing-a-development-branch (`--no-ff` per porta convention), then redeploy to the gw (fresh DB picks up the `level` column).

---

## Self-Review notes

- **Spec coverage:** RecentByKinds (T1) · node Prints/Logs panels w/ FormatLine + lazy-load + excisable + escape + empty hint (T2) · telemetry node `<select>` (T3) · updated excision recipe (the `// telemetry (optional)` tags + `telemetry:node-console` markers + new-file comments are placed in T2 so the recipe in the spec is realizable). All spec sections covered.
- **Type consistency:** `RecentByKinds(deviceID string, kinds []string, limit int) ([]DataRow, error)` defined T1, called T2 with `[]string{"print"}` / `[]string{"log","panic"}`. `consoleVM{ID,Title,Lines,Empty}` defined and rendered with the same fields in `node_console.html`. `renderNodeConsole(w, n, def, title, empty string, kinds []string)` signature matches the T2 Step-5 cases. `telemVM.Nodes []nodeOpt{ID,Name,Selected}` matches the `telemetry.html` range.
- **DB note:** no schema change here; the `level` column came from the set-forward work on this branch.
- **Excisability:** core `detailVM` and the base `node` template gain no telemetry data fields — panels are lazy-loaded placeholders referencing only `.ID`.
