# Web telemetry: per-node console panels + telemetry node filter — Design

**Date:** 2026-06-06
**Branch:** `feat/set-forward` (depends on the `level` column + `kind:"print"` from the set-forward work; ships in the same build/deploy).
**Status:** APPROVED (design) — pending spec review.

## 1. Goal

Make a node's **prints and logs** visible in the browser, and let the **telemetry (metrics) page** be filtered by node. Today the only way to see prints/logs is the CLI `porta monitor -d <node>`; the web `/telemetry` page is metrics-only and has no on-page node selector.

Two deliverables:
1. **Per-node console panels** — a **Prints** panel and a **Logs** panel on the node detail page (`/n/{id}`), each a raw monospace console block rendered with the *same* `telemetry.FormatLine` the CLI uses.
2. **Telemetry-page node filter** — an **All nodes / <node>** `<select>` on `/telemetry` driving the existing `?node=` filter.

## 2. Non-goals / constraints

- **Telemetry stays optional and excisable.** This feature extends the bounded excision recipe in `internal/web/telemetry.go`; it must not entangle telemetry into the core node page. Concretely: the core node view-model (`detailVM`) and the core `node` template gain **no** telemetry data fields — the panels are self-contained and lazy-loaded (§4).
- **No schema change.** The `level` column and `kind:"print"` already landed in the set-forward work.
- **Read-only.** The web console is read-only (commands go via CLI/nodus); these panels only display.
- **Metrics page stays metrics-only.** The node filter is the *only* change to `/telemetry`; it does not gain prints/logs.
- YAGNI: no full-history scrollback, no search, no level-filter dropdown, no SSE — newest-N polled refresh, mirroring the existing node sections.

## 3. Data layer

One new store read in `internal/store/data.go` (mirrors the existing telemetry-specific `RecentMetrics`):

```go
// RecentByKinds returns the device's newest <= limit rows whose kind is in
// kinds, newest first. Used by the per-node console panels (prints / logs).
func (s *Store) RecentByKinds(deviceID string, kinds []string, limit int) ([]DataRow, error)
```

- SQL: `SELECT ts, seq, COALESCE(kind,''), COALESCE(name,''), value, COALESCE(text,''), COALESCE(value_type,''), COALESCE(level,'') FROM data_log WHERE device_id = ? AND kind IN (<n placeholders>) ORDER BY ts DESC, seq DESC LIMIT ?`.
- Placeholders for `kinds` are built dynamically; args are `deviceID, kinds…, limit`.
- Returns `DataRow` (carries `.Level`). Empty `kinds` ⇒ return no rows (guard; never emit `IN ()`).
- This is the only store addition; `RecentMetrics` is unchanged and still backs the metrics page.

## 4. Per-node console panels (node page)

### 4.1 Excisability approach — lazy-loaded, zero core coupling

The panels are **lazy-loaded** so the core node page owns nothing telemetry-specific:

- `node.html` gains two **literal placeholder `<section>`s** (referencing only `.ID`, which the core VM already has), wrapped in excision markers:

```html
<!-- telemetry:node-console begin (optional; see node_console.go) -->
<section id="prints" hx-get="/n/{{.ID}}/prints" hx-trigger="load, every 3s" hx-swap="outerHTML">
  <h2>Prints</h2><p class="subtitle">loading…</p></section>
<section id="logs" hx-get="/n/{{.ID}}/logs" hx-trigger="load, every 3s" hx-swap="outerHTML">
  <h2>Logs</h2><p class="subtitle">loading…</p></section>
<!-- telemetry:node-console end -->
```

  On page load, htmx immediately (`load`) GETs each route and replaces the placeholder with the rendered panel; it then polls every 3s. The core `detailVM` / `node` template need **no** prints/logs fields.

- New file `internal/web/node_console.go` owns the panel view-model + render helpers.
- New file `internal/web/templates/node_console.html` owns the `node-prints` / `node-logs` template defines.
- `handleNodeSub` (in `pages.go`) gains two delegating cases, tagged for excision:

```go
		// telemetry (optional): per-node console panels — see node_console.go
		case "prints":
			h.renderNodeConsole(w, n, "node-prints", "Prints",
				"no prints — forwarding may be off (set-forward --print on)", []string{"print"})
		case "logs":
			h.renderNodeConsole(w, n, "node-logs", "Logs",
				"no logs — forwarding may be off (set-forward --log on)", []string{"log", "panic"})
```

### 4.2 Handler + view-model (`node_console.go`)

```go
type consoleVM struct {
	ID    string   // node id, for the poll URL referenced by the define
	Title string   // "Prints" / "Logs"
	Lines []string // pre-formatted console lines, chronological (oldest→newest)
	Empty string   // hint shown when Lines is empty
}

func (h *Handler) renderNodeConsole(w http.ResponseWriter, n *store.Node, def, title, empty string, kinds []string) {
	rows, err := h.st.RecentByKinds(n.ID, kinds, 50)
	if err != nil { http.Error(w, err.Error(), http.StatusInternalServerError); return }
	// rows are newest-first; reverse to chronological (newest at the bottom, like a tail).
	lines := make([]string, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- {
		lines = append(lines, telemetry.FormatLine(rows[i]))
	}
	h.render(w, def, consoleVM{ID: n.ID, Title: title, Lines: lines, Empty: empty})
}
```

- The empty-hint strings are passed by the caller (the two `handleNodeSub` cases): Prints → "no prints — forwarding may be off (set-forward --print on)"; Logs → "no logs — forwarding may be off (set-forward --log on)".
- 50 rows, reversed to chronological so the newest line sits at the bottom (terminal tail convention).
- Rows render via `telemetry.FormatLine` — identical to `porta monitor`, so a log row shows `<ts>  log     [warn] text`, a panic shows `<ts>  panic   <blob>`, a print shows `<ts>  print   text`.

### 4.3 Template (`node_console.html`)

Each define emits the *same* wrapper element (`id`, `hx-get`, `hx-trigger`, `hx-swap`) the placeholder used, so the 3s poll continues after the swap:

```html
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

- Each define hardcodes its own `id` and `hx-get` route (`prints` / `logs`) to exactly match its placeholder in `node.html`, so the 3s poll continues after the swap. (Two near-identical defines is acceptable and clearer than a shared parametric one given the literal ids.)
- `{{.}}` auto-escapes the formatted line (defends against hostile log/print text from a node — XSS safety, consistent with prior review findings). The base64 panic blob renders inert.
- `.console` CSS: monospace, small, scrollable (`max-height`, `overflow:auto`, `white-space:pre`) — added to `style.css`.

## 5. Telemetry-page node filter

In `internal/web/telemetry.go`, extend `telemVM` with the node option list:

```go
type nodeOpt struct{ ID, Name string; Selected bool }
// telemVM gains:  Nodes []nodeOpt
```

`telemVM(nodeID, now)` already calls `nodeNames()`; build `Nodes` from `ListNodes()` (one option per node, `Selected` when `n.ID == nodeID`), preserving list order. Prepend an **"All nodes"** option (empty `ID`, `Selected` when `nodeID == ""`).

In `telemetry.html`, add the selector above the table (inside the `telemetry` define):

```html
<form>
<select name="node" hx-get="/partials/telemetry" hx-target="#telem" hx-swap="outerHTML" hx-trigger="change">
  <option value=""{{if not .NodeID}} selected{{end}}>All nodes</option>
  {{range .Nodes}}<option value="{{.ID}}"{{if .Selected}} selected{{end}}>{{.Name}}</option>{{end}}
</select>
</form>
```

- On `change`, htmx GETs `/partials/telemetry?node=<id>` and swaps the `#telem` tbody (which already rebuilds its own `?node=` poll URL from `.NodeID`). The metrics then poll for the selected node.
- `handleTelemetryPartial` already reads `?node=`; the partial response (`telem-rows`) carries `.NodeID` so polling stays scoped. No new route.
- The `<select>` value is preserved across the 5s metric poll because the select lives outside `#telem` (the polled region), so it is not replaced.
- The page `<h1>` still shows `· <node>` only on full-page load (`?node=` in the URL); selecting via the dropdown updates the table but not the H1. Acceptable (the selected `<option>` reflects the choice). Linking from a node page (`/telemetry?node=<id>`) still full-loads with the H1 and the dropdown pre-selected.

## 6. Excision recipe (updated)

To remove telemetry entirely, in addition to today's steps (delete `telemetry.go` + `telemetry.html`, the `/telemetry` + `/partials/telemetry` routes, the nav link, the node "Telemetry →" link):

- Delete `internal/web/node_console.go` and `internal/web/templates/node_console.html`.
- Remove the two `case "prints"` / `case "logs"` lines from `handleNodeSub` (tagged with the `// telemetry (optional)` comment).
- Remove the `telemetry:node-console begin…end` block from `node.html`.
- Remove the `.console` rule from `style.css` (cosmetic).
- (`store.RecentByKinds` may be left or deleted — like `RecentMetrics`, it is dead-but-harmless once the web surface is gone.)

## 7. Testing

**Store (`internal/store/data_test.go`):**
- `RecentByKinds` returns only rows of the requested kinds, node-scoped, newest-first, honoring `limit`, with `Level` populated. Insert a mix of `metric`/`log`/`print`/`panic` rows for two nodes; assert `RecentByKinds("node", []string{"log","panic"}, 50)` returns the log+panic rows for that node only (no metrics, no print, no other node) and that a `log` row's `Level` round-trips.
- Empty `kinds` returns no rows and no error (no `IN ()`).

**Web (`internal/web/web_test.go`):**
- `GET /n/{id}/prints` renders a `<section id="prints">` containing a seeded print line and not a seeded log line.
- `GET /n/{id}/logs` renders a `<section id="logs">` containing a seeded `log` row shown as `[warn]` (level) and a seeded `panic` row, and not the print line.
- Empty case: a node with no prints renders the empty hint in `/n/{id}/prints`.
- The node page (`GET /n/{id}`) contains both placeholder sections (`hx-get="/n/{id}/prints"` and `…/logs`).
- `GET /telemetry` renders the node `<select>` with an "All nodes" option plus one option per node; `GET /telemetry?node=<id>` marks that node's option `selected`.
- Console lines are HTML-escaped: a print row with text `<script>` renders escaped, not raw.

## 8. Files touched

| File | Change |
|------|--------|
| `internal/store/data.go` | + `RecentByKinds` |
| `internal/store/data_test.go` | + `RecentByKinds` tests |
| `internal/web/node_console.go` | **new** — panel handler + `consoleVM` + helpers |
| `internal/web/templates/node_console.html` | **new** — `node-prints` / `node-logs` defines |
| `internal/web/pages.go` | + two delegating cases in `handleNodeSub` |
| `internal/web/templates/node.html` | + the `telemetry:node-console` placeholder block |
| `internal/web/telemetry.go` | + `nodeOpt`, `telemVM.Nodes`, build options |
| `internal/web/templates/telemetry.html` | + node `<select>` |
| `internal/web/templates/style.css` | + `.console` rule |
| `internal/web/web_test.go` | + panel + filter tests |

No protocol, schema, CLI, or core-VM changes.
