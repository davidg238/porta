# Web node identity (chip/SDK) visibility (S4) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show each node's self-reported `chip` and `sdk` in the operator web UI's per-node detail header (identity display only — no match badge).

**Architecture:** Add two string fields to the web detail view-model (`detailVM`) populated from the already-present `store.Node.Chip`/`Sdk`, and render them in the polled `node-header` template partial with a graceful fallback for nodes that haven't reported identity. No store/protocol/server change; no new route (the header is already a 2s-polled partial).

**Tech Stack:** Go, `html/template`, the existing `internal/web` package + `net/http/httptest` tests.

**Spec:** `docs/specs/2026-06-02-web-node-identity-s4-design.md`.

**Verified facts this plan builds on:**
- `store.Node` already has `Chip string` and `Sdk string` (populated by `handler.writeReport` → `store.UpdateNodeIdentity`; Phase-1).
- `store.UpdateNodeIdentity(id, chip, sdk string) error` exists (used in tests to seed identity).
- `internal/web/pages.go`: `detailVM` struct (fields Title/ID/Name/Kind/IP/EUI/PollIntv/Gauge/Config/ConfApp/Recent/Apps) and its builder `detailVM(n *store.Node) detailVM` ending in a `return detailVM{...}` literal (Kind:`n.Kind`, IP:`n.SourceAddr`, EUI:`n.ID`, PollIntv:`humanizeDur(n.PollIntervalS)`, …).
- `internal/web/templates/node.html`: the `node-header` partial's subtitle line is
  `<p class="subtitle">{{.Kind}} · {{.IP}} · eui {{.EUI}} · poll {{.PollIntv}} · <a href="/telemetry?node={{.ID}}">Telemetry →</a></p>`.
- Test convention (`internal/web/web_test.go`): `st := testStore(t)`; `st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)`; `srv := serve(t, st)`; `body := readBody(t, mustGet(t, srv.URL+"/n/aabbccddeeff"))`; assert substrings with `strings.Contains`. (`TestNodeDetailRendersSections` is the model.)

---

### Task 1: Show chip/sdk in the node detail header

**Files:**
- Modify: `internal/web/pages.go` (`detailVM` struct + its builder)
- Modify: `internal/web/templates/node.html` (`node-header` partial subtitle)
- Test: `internal/web/web_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/web/web_test.go` (mirrors `TestNodeDetailRendersSections`):

```go
func TestNodeHeaderShowsIdentity(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	if err := st.UpdateNodeIdentity("aabbccddeeff", "esp32", "v2.0.0-alpha.192"); err != nil {
		t.Fatal(err)
	}
	srv := serve(t, st)

	body := readBody(t, mustGet(t, srv.URL+"/n/aabbccddeeff"))
	for _, want := range []string{"esp32", "v2.0.0-alpha.192"} {
		if !strings.Contains(body, want) {
			t.Errorf("header missing reported identity %q: %s", want, body)
		}
	}
}

func TestNodeHeaderIdentityFallback(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000) // no UpdateNodeIdentity → chip/sdk empty
	srv := serve(t, st)

	body := readBody(t, mustGet(t, srv.URL+"/n/aabbccddeeff"))
	for _, want := range []string{"chip ?", "sdk —"} {
		if !strings.Contains(body, want) {
			t.Errorf("header missing identity fallback %q: %s", want, body)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/web/ -run TestNodeHeaderShowsIdentity`
Expected: FAIL — the rendered header does not yet contain `esp32` / `v2.0.0-alpha.192` (the view-model has no Chip/Sdk and the template doesn't render them).

- [ ] **Step 3: Add the view-model fields**

In `internal/web/pages.go`, add to the `detailVM` struct (after `PollIntv string`):

```go
	PollIntv string
	Chip     string
	Sdk      string
```

And in the `return detailVM{...}` literal in the `detailVM(n)` builder, add (after the `PollIntv:` line):

```go
		PollIntv: humanizeDur(n.PollIntervalS),
		Chip:     n.Chip,
		Sdk:      n.Sdk,
```

- [ ] **Step 4: Render them in the header partial**

In `internal/web/templates/node.html`, replace the `node-header` subtitle line:

```html
  <p class="subtitle">{{.Kind}} · {{.IP}} · eui {{.EUI}} · poll {{.PollIntv}} · <a href="/telemetry?node={{.ID}}">Telemetry →</a></p>
```

with:

```html
  <p class="subtitle">{{.Kind}} · {{.IP}} · eui {{.EUI}} · poll {{.PollIntv}} · {{if .Chip}}{{.Chip}}{{else}}chip ?{{end}} · sdk {{if .Sdk}}{{.Sdk}}{{else}}—{{end}} · <a href="/telemetry?node={{.ID}}">Telemetry →</a></p>
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/web/`
Expected: PASS (both new tests + the existing web suite; templates are embedded so the new render is exercised).

- [ ] **Step 6: Build + vet, then commit**

Run: `go build ./... && go vet ./...`
Expected: clean.

```bash
git add internal/web/pages.go internal/web/templates/node.html internal/web/web_test.go
git commit -m "feat(porta): show reported chip/sdk in the web node header"
```

---

## Final verification

- [ ] `go build ./... && go vet ./... && go test ./...` — all green.
- [ ] Manual smoke (optional): `porta serve`, open `http://localhost:6970/n/<node>`, confirm the header subtitle shows `… · esp32 · sdk v2.0.0-alpha.192 · Telemetry →` for a node that has reported identity (and `chip ? · sdk —` for one that hasn't). The header refreshes every 2s, so identity appears automatically on the next report.

## Notes for the implementer

- **No store/protocol/server change.** `Chip`/`Sdk` already exist on `store.Node` and are populated from the report; S4 only adds the view-model fields + template render.
- **The `—` is an em dash (U+2014)**, matching the existing UI's use of `—` for "absent" (e.g. the config table's `{{else}}—{{end}}`). Use the literal em dash character.
- **No new route.** `node-header` is already polled via `hx-get="/n/{{.ID}}/header" hx-trigger="every 2s"`, so the identity updates live with no extra wiring.
