# Command Lifecycle Badges + Operator-UI Reorg — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show each queued command's delivery lifecycle on the node page, separate gateway-local actions into a banner edit toggle, and move telemetry to a self-contained global `/telemetry` page.

**Architecture:** Display-layer change in the Go gateway only — no wire protocol, DB schema, or node-firmware change. A command's lifecycle state is *derived at render time* from data already in `command_queue` + the node's cached `observed_state`. A pure `control.LifecycleOf` computes the state; the web layer renders badges. Telemetry's web surface is isolated in its own file/template/routes so it can be excised cleanly later.

**Tech Stack:** Go, `database/sql` + `mattn/go-sqlite3`, `html/template`, htmx (polling), `net/http`. Tests use `testing` + `httptest` with in-memory sqlite (`store.Open(":memory:")`).

**Spec:** `docs/specs/2026-05-31-command-lifecycle-ui-design.md`

**Conventions to follow (already in the codebase):**
- Polled partials re-emit their own wrapper element (`id` + `hx-get` + `hx-trigger="every Ns"` + `hx-swap="outerHTML"`) so an outerHTML swap is idempotent. See `nodes-rows` / `log-rows` in `internal/web/templates/`.
- `render(w, name, data)` executes a named template into a buffer; only writes on success.
- Tests: `testStore(t)` opens in-memory sqlite; `serve(t, st)` mounts `New(st).Register(mux)` on an `httptest.Server`; `readBody` / `mustGet` helpers exist in `internal/web/web_test.go`.

---

## File Structure

| File | Responsibility | Task |
|---|---|---|
| `internal/config/project.go` | add exported `DecodeSetArgs` (single set command → app/key/value) | 1 |
| `internal/control/lifecycle.go` (new) | pure `LifecycleOf` + `Lifecycle` type | 2 |
| `internal/store/store.go` | add `RecentCommandsForDevice` | 3 |
| `internal/web/pages.go` | node view model: `Recent` replaces `Pending` | 4 |
| `internal/web/partials.go` | `confirm()` renders `node-recent` | 4 |
| `internal/web/templates/node.html` | `node-recent` replaces `node-pending`; forms retarget `#recent`; gateway-settings `<details>`; telemetry link; telemetry section removed | 4,6,7 |
| `internal/web/assets/style.css` | badge classes | 4 |
| `internal/store/data.go` | add `LoggedData` + `RecentMetrics` | 5 |
| `internal/web/telemetry.go` (new) | global telemetry page handler + VMs (self-contained for excise) | 6 |
| `internal/web/templates/telemetry.html` (new) | telemetry page + `telem-rows` partial | 6 |
| `internal/web/templates/base.html` | nav: add Telemetry link | 6 |
| `internal/web/web.go` | register `/telemetry`, `/partials/telemetry` | 6 |

---

## Task 1: Export `config.DecodeSetArgs`

`LifecycleOf` (Task 2) needs to pull `(app, key, value)` out of a single stored `set` command to check convergence. The package already has an unexported `decodeSetArgs`; expose it.

**Files:**
- Modify: `internal/config/project.go` (append after `decodeSetArgs`, ~line 82)
- Test: `internal/config/project_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/config/project_test.go`:

```go
func TestDecodeSetArgs(t *testing.T) {
	app, key, val, ok := DecodeSetArgs(`{"app":"vin","key":"gain","value":5}`)
	if !ok || app != "vin" || key != "gain" {
		t.Fatalf("got app=%q key=%q ok=%v", app, key, ok)
	}
	if n, isNum := val.(json.Number); !isNum || n.String() != "5" {
		t.Fatalf("value = %#v, want json.Number 5", val)
	}
	if _, _, _, ok := DecodeSetArgs(`not json`); ok {
		t.Errorf("malformed args should return ok=false")
	}
}
```

(`encoding/json` is already imported in `project_test.go` — `json.Number` resolves without changes.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestDecodeSetArgs -v`
Expected: FAIL — `undefined: DecodeSetArgs`

- [ ] **Step 3: Add the exported wrapper**

Append to `internal/config/project.go`:

```go
// DecodeSetArgs exposes a single set command's (app, key, value) to callers
// outside this package — e.g. command-lifecycle convergence checks. Numeric
// values come out as json.Number (UseNumber), matching ConfigFromObserved so
// EqualScalars compares them faithfully.
func DecodeSetArgs(argsJSON string) (app, key string, value any, ok bool) {
	return decodeSetArgs(argsJSON)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -run TestDecodeSetArgs -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config/project.go internal/config/project_test.go
git commit -m "feat(porta): export config.DecodeSetArgs for lifecycle convergence"
```

---

## Task 2: `control.LifecycleOf` + `Lifecycle` type

Pure function deriving a command's delivery state from gateway-side data only. States: `queued → delivered → converged` (`set` only) and `expired`.

**Files:**
- Create: `internal/control/lifecycle.go`
- Test: `internal/control/lifecycle_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/control/lifecycle_test.go`:

```go
package control

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/davidg238/porta/internal/store"
)

func delivered(ts int64) sql.NullInt64 { return sql.NullInt64{Int64: ts, Valid: true} }

// observed builds the app→key→value map the way ConfigFromObserved does
// (json.Number values via UseNumber), so EqualScalars matches DecodeSetArgs.
func observed(t *testing.T, blob string) map[string]map[string]any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(blob))
	dec.UseNumber()
	var obj struct {
		Config map[string]map[string]any `json:"config"`
	}
	if err := dec.Decode(&obj); err != nil {
		t.Fatal(err)
	}
	return obj.Config
}

func TestLifecycleOf(t *testing.T) {
	const maxOffline = 300
	now := int64(10_000)
	setArgs := `{"app":"vin","key":"gain","value":5}`

	tests := []struct {
		name string
		cmd  store.Command
		obs  map[string]map[string]any
		want Lifecycle
	}{
		{
			name: "queued: undelivered, fresh",
			cmd:  store.Command{Verb: "set", Args: setArgs, IssuedAt: now - 10},
			want: LifecycleQueued,
		},
		{
			name: "expired: undelivered past max_offline",
			cmd:  store.Command{Verb: "set", Args: setArgs, IssuedAt: now - maxOffline - 1},
			want: LifecycleExpired,
		},
		{
			name: "delivered: set, observed does not match yet",
			cmd:  store.Command{Verb: "set", Args: setArgs, IssuedAt: now - 100, DeliveredAt: delivered(now - 50)},
			obs:  observed(t, `{"config":{"vin":{"gain":4}}}`),
			want: LifecycleDelivered,
		},
		{
			name: "converged: set, observed matches desired",
			cmd:  store.Command{Verb: "set", Args: setArgs, IssuedAt: now - 100, DeliveredAt: delivered(now - 50)},
			obs:  observed(t, `{"config":{"vin":{"gain":5}}}`),
			want: LifecycleConverged,
		},
		{
			name: "delivered terminal: non-set verb never converges",
			cmd:  store.Command{Verb: "set-console", Args: `{"on":true}`, IssuedAt: now - 100, DeliveredAt: delivered(now - 50)},
			obs:  observed(t, `{"config":{"vin":{"gain":5}}}`),
			want: LifecycleDelivered,
		},
		{
			name: "expiry boundary: exactly max_offline is expired",
			cmd:  store.Command{Verb: "set", Args: setArgs, IssuedAt: now - maxOffline},
			want: LifecycleExpired,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LifecycleOf(tt.cmd, tt.obs, maxOffline, now); got != tt.want {
				t.Errorf("LifecycleOf = %q, want %q", got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/control/ -run TestLifecycleOf -v`
Expected: FAIL — `undefined: Lifecycle` / `undefined: LifecycleOf`

- [ ] **Step 3: Write the implementation**

Create `internal/control/lifecycle.go`:

```go
package control

import (
	"github.com/davidg238/porta/internal/config"
	"github.com/davidg238/porta/internal/store"
)

// Lifecycle is a command's derived delivery state. It is computed at render
// time from gateway-side data only (the command row + the node's cached
// observed config) — there is no node ACK/NACK, so there is no "failed".
type Lifecycle string

const (
	LifecycleQueued    Lifecycle = "queued"    // undelivered, within the expiry window
	LifecycleDelivered Lifecycle = "delivered" // node pulled it; terminal for non-set verbs
	LifecycleConverged Lifecycle = "converged" // set only: observed config now matches desired
	LifecycleExpired   Lifecycle = "expired"   // undelivered for >= max_offline_s
)

// LifecycleOf derives the state of one command.
//   - observed is the node's observed config (app→key→value) from
//     ConfigFromObserved — json.Number values, comparable via config.EqualScalars.
//   - maxOfflineS is the node's offline threshold (the expiry window).
//   - now is epoch seconds.
//
// Only "set" can reach Converged; every other verb stops at Delivered (no
// observed-state to reconcile against). A command undelivered for at least
// maxOfflineS is Expired — note the node pulls one command per check-in,
// oldest-first, so a command queued deep behind others can read Expired while
// legitimately waiting its turn (accepted; see the spec).
func LifecycleOf(c store.Command, observed map[string]map[string]any, maxOfflineS, now int64) Lifecycle {
	if !c.DeliveredAt.Valid {
		if now-c.IssuedAt >= maxOfflineS {
			return LifecycleExpired
		}
		return LifecycleQueued
	}
	if c.Verb == "set" {
		if app, key, val, ok := config.DecodeSetArgs(c.Args); ok {
			if m := observed[app]; m != nil {
				if o, present := m[key]; present && config.EqualScalars(val, o) {
					return LifecycleConverged
				}
			}
		}
	}
	return LifecycleDelivered
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/control/ -run TestLifecycleOf -v`
Expected: PASS (all 6 subtests)

- [ ] **Step 5: Commit**

```bash
git add internal/control/lifecycle.go internal/control/lifecycle_test.go
git commit -m "feat(porta): control.LifecycleOf derives command delivery state"
```

---

## Task 3: `store.RecentCommandsForDevice`

Per-device "last N commands" read (delivered or not), newest first — backs the node page's Recent commands section.

**Files:**
- Modify: `internal/store/store.go` (add after `CommandLog`, ~line 296)
- Test: `internal/store/store_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/store/store_test.go`:

```go
func TestRecentCommandsForDevice(t *testing.T) {
	st := openTmp(t)
	for i := 0; i < 3; i++ {
		if _, err := st.EnqueueCommand("dev1", "set", `{"app":"a","key":"k","value":1}`, "cli", int64(100+i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.EnqueueCommand("dev2", "stop", `{"name":"x"}`, "cli", 200); err != nil {
		t.Fatal(err)
	}

	got, err := st.RecentCommandsForDevice("dev1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (limit)", len(got))
	}
	if got[0].ID <= got[1].ID {
		t.Errorf("not newest-first: %d then %d", got[0].ID, got[1].ID)
	}
	for _, c := range got {
		if c.Verb != "set" {
			t.Errorf("leaked another device's command: %+v", c)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestRecentCommandsForDevice -v`
Expected: FAIL — `st.RecentCommandsForDevice undefined`

- [ ] **Step 3: Write the implementation**

Add to `internal/store/store.go` after `CommandLog`:

```go
// RecentCommandsForDevice returns the newest <= limit commands for one device
// (delivered or not), newest first. Backs the node page's Recent commands view.
func (s *Store) RecentCommandsForDevice(deviceID string, limit int) ([]Command, error) {
	rows, err := s.db.Query(`SELECT `+cmdCols+`
		FROM command_queue WHERE device_id = ? ORDER BY id DESC LIMIT ?`, deviceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Command
	for rows.Next() {
		c, err := scanCommand(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestRecentCommandsForDevice -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/store.go internal/store/store_test.go
git commit -m "feat(porta): store.RecentCommandsForDevice (per-device, newest-first)"
```

---

## Task 4: Node page — "Recent commands" replaces "Pending commands"

Swap the undelivered-only "Pending commands" section for a "Recent commands" timeline (last 10, with lifecycle badges). All action forms retarget `#pending` → `#recent`.

**Files:**
- Modify: `internal/web/pages.go` (detailVM struct + builder + handleNodeSub case)
- Modify: `internal/web/partials.go` (`confirm()`)
- Modify: `internal/web/templates/node.html` (template + form targets)
- Modify: `internal/web/assets/style.css` (badge classes)
- Modify: `internal/web/web_test.go` (section assertion)

- [ ] **Step 1: Write the failing test**

Add to `internal/web/web_test.go`:

```go
func TestNodeRecentCommandsBadges(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	// A delivered set whose observed config matches → converged.
	id, err := control.Set(st, "aabbccddeeff", "demo", "gain", int64(2), "cli", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.MarkDelivered(id, 1001); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertReport("aabbccddeeff",
		`{"config":{"demo":{"gain":2}},"apps":{"demo":{"crc":99,"runlevel":3}}}`, "", 1002); err != nil {
		t.Fatal(err)
	}
	srv := serve(t, st)

	body := readBody(t, mustGet(t, srv.URL+"/n/aabbccddeeff"))
	for _, want := range []string{"Recent commands", "badge-converged", `id="recent"`} {
		if !strings.Contains(body, want) {
			t.Errorf("recent section missing %q: %s", want, body)
		}
	}
	// The polled partial endpoint serves the same section.
	p := readBody(t, mustGet(t, srv.URL+"/n/aabbccddeeff/recent"))
	if !strings.Contains(p, `id="recent"`) || !strings.Contains(p, "badge-") {
		t.Errorf("recent partial missing wrapper/badge: %s", p)
	}
}
```

Also update the existing `TestNodeDetailRendersSections` wanted list (line ~85): change `"Pending"` to `"Recent"`. Leave `"Telemetry"` and `"pm25"` for now (telemetry is removed in Task 6).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/web/ -run 'TestNodeRecentCommandsBadges|TestNodeDetailRendersSections' -v`
Expected: FAIL — body lacks "Recent commands" / `id="recent"`

- [ ] **Step 3a: Update the view model in `internal/web/pages.go`**

In the `detailVM` struct, replace the `Pending` field:

```go
	Pending  []store.Command
```

with:

```go
	Recent   []recentRowVM
```

Add the row type just above the `detailVM` struct:

```go
// recentRowVM is one row in the node page's Recent commands timeline.
type recentRowVM struct {
	ID    int64
	Verb  string
	Args  string
	State string // queued | delivered | converged | expired
}
```

In the `detailVM` builder, replace:

```go
	pending, _ := h.st.UndeliveredCommands(n.ID)
```

with:

```go
	recentCmds, _ := h.st.RecentCommandsForDevice(n.ID, 10)
	obsConfig := control.ConfigFromObserved(n.ObservedState)
	recent := make([]recentRowVM, 0, len(recentCmds))
	for _, c := range recentCmds {
		recent = append(recent, recentRowVM{
			ID:    c.ID,
			Verb:  c.Verb,
			Args:  c.Args,
			State: string(control.LifecycleOf(c, obsConfig, n.MaxOfflineS, now)),
		})
	}
```

And in the returned struct literal, replace `Pending:  pending,` with `Recent:   recent,`.

- [ ] **Step 3b: Update `handleNodeSub` in `internal/web/pages.go`**

Replace the partial case:

```go
	case "pending":
		h.render(w, "node-pending", vm)
```

with:

```go
	case "recent":
		h.render(w, "node-recent", vm)
```

- [ ] **Step 3c: Update `confirm()` in `internal/web/partials.go`**

In `confirm()`, change the template name from `"node-pending"` to `"node-recent"`:

```go
	if err := h.tmpl.ExecuteTemplate(&buf, "node-recent", vm); err != nil {
```

- [ ] **Step 3d: Update `internal/web/templates/node.html`**

In the `"node"` define, replace `{{template "node-pending" .}}` with `{{template "node-recent" .}}`.

Replace the entire `node-pending` define block:

```html
{{define "node-pending"}}<section id="pending" hx-get="/n/{{.ID}}/pending" hx-trigger="every 2s" hx-swap="outerHTML">
  <h2>Pending commands</h2>
  {{if .Pending}}<table><tbody>{{range .Pending}}<tr><td>#{{.ID}}</td><td>{{.Verb}}</td><td><code>{{.Args}}</code></td></tr>{{end}}</tbody></table>{{else}}<p class="subtitle">none undelivered</p>{{end}}
</section>{{end}}
```

with:

```html
{{define "node-recent"}}<section id="recent" hx-get="/n/{{.ID}}/recent" hx-trigger="every 2s" hx-swap="outerHTML">
  <h2>Recent commands</h2>
  {{if .Recent}}<table><tbody>{{range .Recent}}<tr><td>#{{.ID}}</td><td>{{.Verb}}</td><td><code>{{.Args}}</code></td><td><span class="badge badge-{{.State}}">{{.State}}</span></td></tr>{{end}}</tbody></table>{{else}}<p class="subtitle">no commands yet</p>{{end}}
</section>{{end}}
```

Retarget every action form that currently has `hx-target="#pending"` to `hx-target="#recent"`. These are: `set`, `console`, `poll-interval` (in `node-actions`) and `install`, `uninstall` (in `node-containers`) — 5 forms total. (`max-offline` and `rename` target `#hdr`; leave them.)

- [ ] **Step 3e: Add badge CSS to `internal/web/assets/style.css`**

Append:

```css
.badge { font-size:.7rem; padding:.05rem .4rem; border-radius:3px; color:#fff; }
.badge-queued { background:var(--amber); color:#000; }
.badge-delivered { background:#06c; }
.badge-converged { background:var(--green); }
.badge-expired { background:var(--red); }
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/web/ -v`
Expected: PASS (new badge test + updated sections test + all existing web tests, since `confirm()` now renders `node-recent` and the set/console/install tests still see `id="recent"` and "queued").

- [ ] **Step 5: Commit**

```bash
git add internal/web/pages.go internal/web/partials.go internal/web/templates/node.html internal/web/assets/style.css internal/web/web_test.go
git commit -m "feat(porta): node page Recent commands timeline with lifecycle badges"
```

---

## Task 5: `store.RecentMetrics` (metrics-only, optional device filter)

Backs the global telemetry page. Filters `kind='metric'` (which is what removes the blank `log` rows) and tags each row with its device id.

**Files:**
- Modify: `internal/store/data.go` (add `LoggedData` + `RecentMetrics`)
- Test: `internal/store/data_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/store/data_test.go`:

```go
func TestRecentMetricsFiltersAndOrders(t *testing.T) {
	st := openTestStore(t)
	// Two metric rows + one log row, two devices.
	st.InsertData("devA", 100, 1, "metric", "pm25", int64(7), "", "int")
	st.InsertData("devA", 100, 0, "log", "", nil, "vin: pm25=7", "")
	st.InsertData("devB", 200, 1, "metric", "temp", int64(21), "", "int")

	all, err := st.RecentMetrics("", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("len = %d, want 2 (log row excluded)", len(all))
	}
	if all[0].TS != 200 || all[0].DeviceID != "devB" {
		t.Errorf("not newest-first or device id missing: %+v", all[0])
	}

	just, err := st.RecentMetrics("devA", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(just) != 1 || just[0].DeviceID != "devA" || just[0].Name != "pm25" {
		t.Errorf("device filter wrong: %+v", just)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestRecentMetricsFiltersAndOrders -v`
Expected: FAIL — `st.RecentMetrics undefined`

- [ ] **Step 3: Write the implementation**

Add to `internal/store/data.go`:

```go
// LoggedData is a data_log row tagged with its device id, for the global
// telemetry view (DataRow alone carries no device id).
type LoggedData struct {
	DataRow
	DeviceID string
}

// RecentMetrics returns the newest <= limit metric rows (kind='metric'),
// newest first. When deviceID != "" it restricts to that device. The
// kind='metric' filter excludes the per-report log rows (empty name/value),
// so the telemetry table shows no blank lines.
func (s *Store) RecentMetrics(deviceID string, limit int) ([]LoggedData, error) {
	q := `SELECT device_id, ts, seq, COALESCE(kind,''), COALESCE(name,''), value, COALESCE(text,''), COALESCE(value_type,'')
		  FROM data_log WHERE kind = 'metric'`
	args := []any{}
	if deviceID != "" {
		q += ` AND device_id = ?`
		args = append(args, deviceID)
	}
	q += ` ORDER BY ts DESC, seq DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LoggedData
	for rows.Next() {
		var r LoggedData
		var v any
		if err := rows.Scan(&r.DeviceID, &r.TS, &r.Seq, &r.Kind, &r.Name, &v, &r.Text, &r.ValueType); err != nil {
			return nil, err
		}
		r.Value = normalizeNumeric(v)
		out = append(out, r)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestRecentMetricsFiltersAndOrders -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/data.go internal/store/data_test.go
git commit -m "feat(porta): store.RecentMetrics (metrics-only, optional device filter)"
```

---

## Task 6: Global `/telemetry` page + remove telemetry from the node page

A self-contained metrics-only telemetry page (own file/template/routes for easy excise later), and the node page loses its telemetry section in favor of a "Telemetry →" link.

**Files:**
- Create: `internal/web/telemetry.go`
- Create: `internal/web/templates/telemetry.html`
- Modify: `internal/web/web.go` (register routes)
- Modify: `internal/web/templates/base.html` (nav link)
- Modify: `internal/web/templates/node.html` (remove telemetry section, add link)
- Modify: `internal/web/pages.go` (drop `Telem` field, `RecentData` call, `telemetry` partial case)
- Modify: `internal/web/web_test.go` (telemetry page test; fix node sections assertion)

- [ ] **Step 1: Write the failing test**

Add to `internal/web/web_test.go`:

```go
func TestTelemetryPageMetricsOnly(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	st.InsertData("aabbccddeeff", 1001, 1, "metric", "pm25", int64(7), "", "int")
	st.InsertData("aabbccddeeff", 1001, 0, "log", "", nil, "vin: pm25=7 (olympic)", "")
	srv := serve(t, st)

	body := readBody(t, mustGet(t, srv.URL+"/telemetry"))
	if !strings.Contains(body, "pm25") {
		t.Errorf("telemetry page missing metric: %s", body)
	}
	if strings.Contains(body, "olympic") {
		t.Errorf("telemetry page leaked a log row: %s", body)
	}
	// Polled partial honors the node filter and re-emits its wrapper.
	p := readBody(t, mustGet(t, srv.URL+"/partials/telemetry?node=aabbccddeeff"))
	if !strings.Contains(p, `id="telem"`) || !strings.Contains(p, "pm25") {
		t.Errorf("telemetry partial missing wrapper/metric: %s", p)
	}
}
```

Update `TestNodeDetailRendersSections` (wanted list, ~line 85): remove `"pm25"` (telemetry no longer renders on the node page). Keep `"Telemetry"` — it is now the "Telemetry →" link text.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/web/ -run 'TestTelemetryPageMetricsOnly|TestNodeDetailRendersSections' -v`
Expected: FAIL — 404 / missing page (no `/telemetry` route yet)

- [ ] **Step 3a: Create `internal/web/telemetry.go`**

```go
package web

import (
	"fmt"
	"net/http"

	"github.com/davidg238/porta/internal/control"
)

// --- Global telemetry page. Self-contained so telemetry can be excised in
// one bounded change: delete this file, telemetry.html, the two routes in
// web.go, the nav link in base.html, and the "Telemetry →" link in node.html.

type telemRowVM struct {
	Time, Node, Name, Value, Type string
}

type telemVM struct {
	Title  string
	Node   string // friendly name when filtered, else ""
	NodeID string // node id when filtered, else ""
	Rows   []telemRowVM
}

// fmtMetric renders a NUMERIC value for display. nil (a degraded metric)
// shows as empty; int64/float64 print directly.
func fmtMetric(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func (h *Handler) nodeNames() (map[string]string, error) {
	nodes, err := h.st.ListNodes()
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(nodes))
	for _, n := range nodes {
		m[n.ID] = n.Name
	}
	return m, nil
}

func (h *Handler) telemVM(nodeID string, now int64) (telemVM, error) {
	rows, err := h.st.RecentMetrics(nodeID, 200)
	if err != nil {
		return telemVM{}, err
	}
	names, err := h.nodeNames()
	if err != nil {
		return telemVM{}, err
	}
	vm := telemVM{Title: "Telemetry", NodeID: nodeID}
	if nodeID != "" {
		vm.Node = names[nodeID]
	}
	for _, r := range rows {
		vm.Rows = append(vm.Rows, telemRowVM{
			Time:  control.RelativeAge(r.TS, now),
			Node:  names[r.DeviceID],
			Name:  r.Name,
			Value: fmtMetric(r.Value),
			Type:  r.ValueType,
		})
	}
	return vm, nil
}

func (h *Handler) handleTelemetry(w http.ResponseWriter, r *http.Request) {
	vm, err := h.telemVM(r.URL.Query().Get("node"), h.now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.render(w, "telemetry", vm)
}

func (h *Handler) handleTelemetryPartial(w http.ResponseWriter, r *http.Request) {
	vm, err := h.telemVM(r.URL.Query().Get("node"), h.now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.render(w, "telem-rows", vm)
}
```

- [ ] **Step 3b: Create `internal/web/templates/telemetry.html`**

```html
{{define "telemetry"}}{{template "head" .}}
<h1>Telemetry{{if .Node}} · {{.Node}}{{end}}</h1>
<table>
  <thead><tr><th>Time</th><th>Node</th><th>Metric</th><th>Value</th><th>Type</th></tr></thead>
  {{template "telem-rows" .}}
</table>
{{template "foot" .}}{{end}}

{{define "telem-rows"}}<tbody id="telem" hx-get="/partials/telemetry{{if .NodeID}}?node={{.NodeID}}{{end}}" hx-trigger="every 5s" hx-swap="outerHTML">
{{range .Rows}}<tr><td>{{.Time}}</td><td>{{.Node}}</td><td>{{.Name}}</td><td>{{.Value}}</td><td>{{.Type}}</td></tr>{{end}}
</tbody>{{end}}
```

- [ ] **Step 3c: Register routes in `internal/web/web.go`**

In `Register`, add after the `/log` line:

```go
	mux.HandleFunc("/telemetry", h.handleTelemetry)
	mux.HandleFunc("/partials/telemetry", h.handleTelemetryPartial)
```

- [ ] **Step 3d: Add the nav link in `internal/web/templates/base.html`**

Replace the nav line:

```html
    <nav><a href="/">Nodes</a> · <a href="/log">Command Log</a></nav>
```

with:

```html
    <nav><a href="/">Nodes</a> · <a href="/telemetry">Telemetry</a> · <a href="/log">Command Log</a></nav>
```

- [ ] **Step 3e: Update `internal/web/templates/node.html`**

In the `"node"` define, remove the line `{{template "node-telemetry" .}}`.

Delete the entire `node-telemetry` define block:

```html
{{define "node-telemetry"}}<section id="telemetry" hx-get="/n/{{.ID}}/telemetry" hx-trigger="every 2s" hx-swap="outerHTML">
  <h2>Telemetry · last 10</h2>
  {{if .Telem}}<table><tbody>{{range .Telem}}<tr><td>{{.TS}}</td><td>{{.Name}}</td><td>{{.Value}}</td><td>{{.ValueType}}</td></tr>{{end}}</tbody></table>{{else}}<p class="subtitle">no telemetry</p>{{end}}
</section>{{end}}
```

In `node-header`, add a "Telemetry →" link after the subtitle `<p>`. Change:

```html
  <p class="subtitle">{{.Kind}} · {{.IP}} · eui {{.EUI}} · poll {{.PollIntv}}</p>
```

to:

```html
  <p class="subtitle">{{.Kind}} · {{.IP}} · eui {{.EUI}} · poll {{.PollIntv}} · <a href="/telemetry?node={{.ID}}">Telemetry →</a></p>
```

- [ ] **Step 3f: Drop telemetry wiring from `internal/web/pages.go`**

Remove the `Telem    []store.DataRow` field from the `detailVM` struct.

Remove the builder line:

```go
	telem, _ := h.st.RecentData(n.ID, 10)
```

Remove `Telem:    telem,` from the returned struct literal.

Remove the `telemetry` case from `handleNodeSub`:

```go
	case "telemetry":
		h.render(w, "node-telemetry", vm)
```

If `store` is now unused in `pages.go`, the compiler will say so — but `*store.Node` is still referenced in `handleNode`/`handleNodeSub`/`detailVM`, so the import stays.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/web/ -v`
Expected: PASS (telemetry page test, updated sections test, all existing tests).
Then build to confirm no unused imports/symbols: `go build ./...`
Expected: clean build.

- [ ] **Step 5: Commit**

```bash
git add internal/web/telemetry.go internal/web/templates/telemetry.html internal/web/web.go internal/web/templates/base.html internal/web/templates/node.html internal/web/pages.go internal/web/web_test.go
git commit -m "feat(porta): global /telemetry page; drop telemetry from node page"
```

---

## Task 7: Banner gateway-settings edit toggle

Move the gateway-local `max-offline` and `rename` forms out of "Actions" into a `<details>` block in the banner, placed as a sibling of `#hdr` (outside the 2s-polled region so the header refresh can't collapse it mid-edit). Template-only — the POST handlers already exist and target `#hdr`.

**Files:**
- Modify: `internal/web/templates/node.html`
- Test: `internal/web/web_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/web/web_test.go`:

```go
func TestBannerGatewaySettingsToggle(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	srv := serve(t, st)

	body := readBody(t, mustGet(t, srv.URL+"/n/aabbccddeeff"))
	for _, want := range []string{`<details id="gw-settings"`, "/n/aabbccddeeff/max-offline", "/n/aabbccddeeff/rename"} {
		if !strings.Contains(body, want) {
			t.Errorf("banner gateway-settings missing %q: %s", want, body)
		}
	}
	// The gateway-settings block must sit before the config section (i.e. in the
	// banner, not inside the polled #hdr which precedes it).
	if i, j := strings.Index(body, `id="gw-settings"`), strings.Index(body, `id="config"`); i < 0 || j < 0 || i > j {
		t.Errorf("gw-settings (%d) should appear before config (%d)", i, j)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/web/ -run TestBannerGatewaySettingsToggle -v`
Expected: FAIL — no `id="gw-settings"` in body

- [ ] **Step 3: Edit `internal/web/templates/node.html`**

In the `"node"` define, add a `node-gwsettings` template call between the header and config:

```html
{{template "node-header" .}}
{{template "node-gwsettings" .}}
{{template "node-config" .}}
```

Add the new define block (place it right after the `node-header` define):

```html
{{define "node-gwsettings"}}<details id="gw-settings">
  <summary>edit gateway settings</summary>
  <form class="action" hx-post="/n/{{.ID}}/max-offline" hx-target="#hdr" hx-swap="outerHTML">
    <b>max-offline</b> <input name="dur" placeholder="5m" required> <button>set</button>
  </form>
  <form class="action" hx-post="/n/{{.ID}}/rename" hx-target="#hdr" hx-swap="outerHTML">
    <b>rename</b> <input name="name" placeholder="new-name" required> <button>set</button>
  </form>
</details>{{end}}
```

In `node-actions`, remove the `max-offline` and `rename` forms (they now live in `node-gwsettings`). After removal, `node-actions` contains only the `set`, `console`, and `poll-interval` forms.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/web/ -v`
Expected: PASS (new toggle test + `TestRenameFormRenamesNode` still green — the rename endpoint is unchanged).

- [ ] **Step 5: Commit**

```bash
git add internal/web/templates/node.html internal/web/web_test.go
git commit -m "feat(porta): banner gateway-settings edit toggle (max-offline, rename)"
```

---

## Final verification

- [ ] **Full suite + build + vet**

Run: `go test ./... && go build ./... && go vet ./...`
Expected: all packages PASS, clean build, no vet warnings.

- [ ] **Manual smoke against the live soak** (optional, server already running)

```bash
go build -o porta ./cmd/porta
kill "$(pgrep -f 'porta serve')"; nohup ./porta serve --db porta.db > porta-soak.log 2>&1 &
```

Then in a browser at `http://127.0.0.1:6970/`:
- Node page shows "Recent commands" with badges; queue a `set` and watch `queued → delivered → converged`.
- Banner has an "edit gateway settings" disclosure with max-offline + rename; opening it and submitting rename refreshes the header without collapsing other state.
- `/telemetry` lists `pm25` metrics with no blank rows; the node page's "Telemetry →" link filters to that node.

---

## Notes for the implementer

- **DRY:** `LifecycleOf` is the single source of truth for command state — do not recompute states in templates or handlers.
- **Convergence is per-command vs current observed:** an older `set` superseded by a newer one (last-write-wins) will not show `converged` once observed reflects the newer value. This is intended (the older command was overwritten).
- **No schema/protocol/firmware change.** If a step tempts you to add a `command_queue` column or a wire field, stop — that is explicitly out of scope (see the spec's non-goals).
- **Telemetry excise-ability:** keep all telemetry-page code in `telemetry.go` / `telemetry.html`; do not scatter it into `pages.go` or `web.go` beyond the two route registrations.
