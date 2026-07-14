# Panic Decode Link Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Render a clickable `[decode ↗]` link on each `panic` row in porta's web Logs panel that hands the raw base64 blob to a local `nodus://decode` URL-scheme handler.

**Architecture:** porta-side only. The Logs render path emits a structured per-row view model so `panic` rows render `prefix → [decode ↗] → raw blob`; every other row stays the flat string it is today. The link is `nodus://decode?node=<id>&blob=<url-encoded>`. `telemetry.FormatLine`, the CLI monitor, the Prints panel, the wire protocol, and the DB schema are untouched. The nodus-side handler is a documented contract, not part of this plan.

**Tech Stack:** Go, `html/template`, `net/url`. Existing packages `internal/web`, `internal/telemetry`.

**Spec:** `docs/design/2026-06-07-panic-decode-link-design.md`

---

## Critical gotcha (read before Task 1)

`html/template` sanitizes `href` attribute values: any URL whose scheme is not
`http`/`https`/`mailto` is replaced with `#ZgotmplZ`. A plain `string` field
holding `nodus://…` **will be neutralized**. The fix is to type the href field as
`template.URL` (the `contentTypeURL` content type), which the template engine emits
verbatim. This is safe here because the value is built from a fixed scheme prefix
plus `url.Values.Encode()` (node id is hex, blob is URL-encoded), so no injection is
possible. Task 1 tests for the `ZgotmplZ` regression explicitly.

---

## File Structure

- `internal/web/node_console.go` — Modify. Add `logLine` + `logsVM` types and a
  `renderNodeLogs` method (structured Logs rendering). Leaves the existing
  `consoleVM` + `renderNodeConsole` (used by Prints) unchanged.
- `internal/web/templates/node_console.html` — Modify. The `node-logs` define
  gains the per-row conditional; `node-prints` is untouched.
- `internal/web/pages.go` — Modify. The `"logs"` case calls `renderNodeLogs`
  instead of the shared `renderNodeConsole`.
- `internal/web/web_test.go` — Modify (test). Add `TestNodeLogsPanicDecodeLink`.
- `docs/DEVSDK.md` — Modify. Document the `nodus://decode` URL-scheme contract.

---

## Task 1: Decode link on panic rows in the Logs panel

**Files:**
- Modify: `internal/web/node_console.go`
- Modify: `internal/web/templates/node_console.html`
- Modify: `internal/web/pages.go:84-86`
- Test: `internal/web/web_test.go`

- [ ] **Step 1: Add imports to the test file**

In `internal/web/web_test.go`, add `"html"` and `"regexp"` to the import block
(it already imports `net/url`, `strings`, etc.):

```go
import (
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/davidg238/porta/internal/control"
	"github.com/davidg238/porta/internal/store"
)
```

- [ ] **Step 2: Write the failing test**

Append to `internal/web/web_test.go`:

```go
func TestNodeLogsPanicDecodeLink(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	// A plain log row (no link) and a panic row (gets a decode link). The blob
	// contains +, /, = on purpose so we exercise URL-encoding round-trip.
	_ = st.InsertData("aabbccddeeff", 1002, 0, "log", "", nil, "plain log", "", "")
	_ = st.InsertData("aabbccddeeff", 1003, 0, "panic", "", nil, "a+b/c=d", "", "")
	srv := serve(t, st)

	logs := readBody(t, mustGet(t, srv.URL+"/n/aabbccddeeff/logs"))

	// Exactly one decode link: the panic row gets it, the log row does not.
	if n := strings.Count(logs, "[decode"); n != 1 {
		t.Fatalf("want exactly one decode link, got %d: %s", n, logs)
	}
	// html/template must NOT have neutralized the nodus:// scheme.
	if strings.Contains(logs, "ZgotmplZ") {
		t.Fatalf("nodus:// href was sanitized to ZgotmplZ: %s", logs)
	}
	// Link sits between the panic column and the raw blob, which stays visible.
	iPanic := strings.Index(logs, "panic")
	iLink := strings.Index(logs, "[decode")
	iBlob := strings.Index(logs, "a+b/c=d")
	if iBlob < 0 || !(iPanic < iLink && iLink < iBlob) {
		t.Fatalf("want order panic<[decode]<blob, got %d/%d/%d: %s", iPanic, iLink, iBlob, logs)
	}
	// The href round-trips: node + blob parse back to the originals.
	m := regexp.MustCompile(`href="(nodus://decode\?[^"]*)"`).FindStringSubmatch(logs)
	if m == nil {
		t.Fatalf("no nodus decode href found: %s", logs)
	}
	raw := html.UnescapeString(m[1]) // attribute escaping turns & into &amp;
	q, err := url.ParseQuery(strings.TrimPrefix(raw, "nodus://decode?"))
	if err != nil {
		t.Fatalf("href query parse: %v (%q)", err, raw)
	}
	if got := q.Get("node"); got != "aabbccddeeff" {
		t.Errorf("node param = %q, want aabbccddeeff", got)
	}
	if got := q.Get("blob"); got != "a+b/c=d" {
		t.Errorf("blob param = %q, want a+b/c=d", got)
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/web/ -run TestNodeLogsPanicDecodeLink -v`
Expected: FAIL — no `[decode` link in the output (the Logs panel still renders the
panic row as a flat string).

- [ ] **Step 4: Add the structured Logs render path**

Replace the entire body of `internal/web/node_console.go` with the following (it
keeps `consoleVM` + `renderNodeConsole` for the Prints panel and adds the Logs
types + `renderNodeLogs`):

```go
// Copyright (c) 2026 Ekorau LLC

// node_console.go is part of porta's OPTIONAL telemetry surface: the per-node
// Prints/Logs console panels on the node detail page. It is self-contained so
// telemetry can be excised in one bounded change (see telemetry.go's recipe):
// delete this file + node_console.html, the two cases in pages.go's
// handleNodeSub, and the telemetry:node-console block in node.html.
package web

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"

	"github.com/davidg238/porta/internal/store"
	"github.com/davidg238/porta/internal/telemetry"
)

// consoleVM backs the node-prints template. Lines are pre-formatted console rows
// (via telemetry.FormatLine), in chronological order (oldest→newest) so the
// newest line sits at the bottom, like a terminal tail.
type consoleVM struct {
	ID    string
	Title string
	Lines []string
	Empty string
}

// renderNodeConsole renders the Prints panel (def "node-prints") for the node,
// showing the newest 50 rows of the given kinds.
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

// logLine is one row of the Logs panel. A panic row is split so a decode link
// can sit between the "panic" column and the still-visible raw blob; every other
// row is the flat FormatLine string in Text (DecodeHref empty).
type logLine struct {
	Text       string       // full FormatLine output; used when DecodeHref == ""
	Pre        string       // panic only: "<ts>  panic   " (matches FormatLine spacing)
	DecodeHref template.URL // panic only: nodus://decode?node=&blob= (template.URL so
	//          html/template does not neutralize the non-http scheme)
	Blob string // panic only: the raw base64 panic message
}

// logsVM backs the node-logs template.
type logsVM struct {
	ID    string
	Title string
	Lines []logLine
	Empty string
}

// renderNodeLogs renders the Logs panel (kinds log+panic). panic rows carry a
// nodus://decode link; all other rows render exactly as FormatLine produces them.
func (h *Handler) renderNodeLogs(w http.ResponseWriter, n *store.Node) {
	rows, err := h.st.RecentByKinds(n.ID, []string{"log", "panic"}, 50)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	lines := make([]logLine, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- { // newest-first → chronological
		r := rows[i]
		if r.Kind == "panic" {
			href := "nodus://decode?" + url.Values{"node": {n.ID}, "blob": {r.Text}}.Encode()
			lines = append(lines, logLine{
				Pre:        telemetry.FormatTS(r.TS) + "  " + fmt.Sprintf("%-7s ", "panic"),
				DecodeHref: template.URL(href),
				Blob:       r.Text,
			})
			continue
		}
		lines = append(lines, logLine{Text: telemetry.FormatLine(r)})
	}
	h.render(w, "node-logs", logsVM{
		ID:    n.ID,
		Title: "Logs",
		Lines: lines,
		Empty: "no logs — forwarding may be off (set-forward --log on)",
	})
}
```

- [ ] **Step 5: Update the node-logs template**

In `internal/web/templates/node_console.html`, replace the `node-logs` define
(leave `node-prints` exactly as-is):

```html
{{define "node-logs"}}<section id="logs" hx-get="/n/{{.ID}}/logs" hx-trigger="every 3s" hx-swap="outerHTML">
<h2>{{.Title}}</h2>
{{if .Lines}}<pre class="console">{{range .Lines}}{{if .DecodeHref}}{{.Pre}}<a href="{{.DecodeHref}}">[decode ↗]</a>  {{.Blob}}{{else}}{{.Text}}{{end}}
{{end}}</pre>{{else}}<p class="subtitle">{{.Empty}}</p>{{end}}
</section>{{end}}
```

- [ ] **Step 6: Wire the logs route to renderNodeLogs**

In `internal/web/pages.go`, change the `"logs"` case (currently calling
`renderNodeConsole`) to:

```go
	case "logs":
		h.renderNodeLogs(w, n)
```

(The `"prints"` case is unchanged — it still calls `renderNodeConsole`.)

- [ ] **Step 7: Run the new test to verify it passes**

Run: `go test ./internal/web/ -run TestNodeLogsPanicDecodeLink -v`
Expected: PASS

- [ ] **Step 8: Run the full web + telemetry suites for regressions**

Run: `go build ./... && go test ./internal/web/ ./internal/telemetry/`
Expected: PASS (existing `TestNodeConsolePanels` still green — the raw blob
"traceblob" remains present in the panic line).

- [ ] **Step 9: Commit**

```bash
git add internal/web/node_console.go internal/web/templates/node_console.html internal/web/pages.go internal/web/web_test.go
git commit -m "feat(web): nodus:// decode link on panic rows in the Logs panel

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: Document the `nodus://decode` URL-scheme contract

**Files:**
- Modify: `docs/DEVSDK.md`

- [ ] **Step 1: Add the contract section**

In `docs/DEVSDK.md`, insert the following new section immediately before the
`## Neutrality` section:

```markdown
## `nodus://decode` URL scheme (panic decode link)

porta's web Logs panel renders a `[decode ↗]` link on each `panic` telemetry row.
The link is the porta→nodus tooling contract:

    nodus://decode?node=<node-id>&blob=<url-encoded base64 panic message>

- `node` — the porta node id (hex EUI), for labelling/fallback lookups.
- `blob` — the raw base64 panic message from the `data_log` row, URL-encoded
  (base64's `+ / =` are percent-encoded).

porta only **emits** this link. The node-repo dev tool registers an OS handler for
the `nodus` scheme (Linux: a `.desktop` file with
`MimeType=x-scheme-handler/nodus;` and `Exec=nodus decode %u`, then
`xdg-mime default …`) and implements `nodus decode <url>`: parse `blob`, run
`jag decode` against the local snapshot cache (jag resolves the snapshot by the
program uuid embedded in the message), and show the decoded trace locally
(e.g. a popup with copy-to-clipboard). Nothing is written back to porta.
```

- [ ] **Step 2: Commit**

```bash
git add docs/DEVSDK.md
git commit -m "docs(devsdk): document nodus://decode URL-scheme contract

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Self-review notes

- **Spec coverage:** affordance + position (Task 1 template/test) ✓; inline `node`+`blob`
  minimal URL with encoding (Task 1 render + round-trip test) ✓; structured-row
  approach A with FormatLine/CLI/Prints untouched (Task 1 keeps `renderNodeConsole`) ✓;
  tests incl. encoding + non-panic-no-link + Prints-no-link (Task 1; Prints covered by
  existing `TestNodeConsolePanels`) ✓; DEVSDK.md doc (Task 2) ✓; out-of-scope items
  (nodus handler, write-back, schema) not touched ✓.
- **Placeholder scan:** none — all code shown in full.
- **Type consistency:** `logLine{Text,Pre,DecodeHref(template.URL),Blob}` and
  `logsVM{ID,Title,Lines,Empty}` used identically in `renderNodeLogs`, the template,
  and asserted by the test. `renderNodeLogs(w, n)` signature matches the pages.go call.
- **Gotcha covered:** `template.URL` typing + explicit `ZgotmplZ` regression assertion.
