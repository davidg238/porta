# porta B4c — htmx Operator UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Serve a browser operator console (fleet list, per-node detail with write forms + image upload, command audit) on porta's existing `serve` HTTP listener, rendering live via htmx polling.

**Architecture:** A new cobra-free `internal/control` package becomes the single source of truth for write orchestration *and* read projections, extracted from `internal/portacli`; both the CLI and a new `internal/web` package (html/template + `//go:embed` + vendored htmx) call it. The web Handler mounts on the B4a `httpsrv.Server.Mux`, inheriting the CIDR allowlist. No wire-protocol or store-schema changes; two additive store read queries.

**Tech Stack:** Go stdlib `net/http` + `html/template` + `embed`, htmx 2.x (vendored), sqlite store, cobra CLI.

**Spec:** `docs/specs/2026-05-28-porta-b4c-htmx-operator-ui-design.md`

---

## Conventions for every task

- Run Go tests with `go test ./...` from repo root unless a narrower path is given.
- Commit only at the end of a task's last step. Branch is `feat/porta-b4c-htmx-ui` (created before Task 1).
- The store uses `sql.NullInt64` for `LastSeen`/`FirstSeen`/`LastReportAt`; treat `!Valid` as "never".
- Commands written from the web set `issued_by="web"` (CLI uses `"cli"`).
- Time is injected: handlers/control take a `now int64` (epoch seconds) so tests are deterministic.

---

## File structure

**Created:**
- `internal/control/control.go` — write orchestration (Set, SetConsole, SetPollInterval, SetMaxOffline, Rename, Uninstall, Install) + `ResolveNodeID`.
- `internal/control/view.go` — read projections (RelativeAge, AppsFromObserved, ConfigFromObserved, DesiredVsObserved + `ConfigRow`/`App`).
- `internal/control/control_test.go`, `internal/control/view_test.go`.
- `internal/web/web.go` — `Handler`, `New`, `Register`, embed, template parse, view models.
- `internal/web/gauge.go` — `Checkin` pure helper + `humanizeDur`.
- `internal/web/pages.go` — index/node/log full-page handlers.
- `internal/web/partials.go` — partial + form POST handlers.
- `internal/web/templates/*.html` — base + page + partial templates.
- `internal/web/assets/style.css`, `internal/web/assets/htmx.min.js` (vendored).
- `internal/web/*_test.go`.

**Modified:**
- `internal/portacli/mutate.go` — call `control.*` (keep print lines).
- `internal/portacli/inspect.go` — call `control.*` read projections.
- `internal/portacli/resolve.go` — delete `resolveNodeID`/`isMAC`; add a thin wrapper calling `control.ResolveNodeID`.
- `internal/store/store.go` — add `RecentCommands`; add `LoggedCommand` type.
- `internal/store/data.go` — add `RecentData`.
- `internal/portacli/serve.go` — register `web.New(st).Register(srv.Mux)` when `httpPort > 0`.

---

### Task 0: Branch

- [ ] **Step 1: Create the feature branch**

```bash
git checkout -b feat/porta-b4c-htmx-ui
git status   # clean, on feat/porta-b4c-htmx-ui
```

---

### Task 1: `internal/control` — write orchestration + ResolveNodeID

**Files:**
- Create: `internal/control/control.go`, `internal/control/control_test.go`
- Modify: `internal/portacli/mutate.go`, `internal/portacli/resolve.go`

- [ ] **Step 1: Write failing tests for the write cores**

Create `internal/control/control_test.go`:

```go
package control

import (
	"bytes"
	"strings"
	"testing"

	"github.com/davidg238/porta/internal/command"
	"github.com/davidg238/porta/internal/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestSetEnqueuesTypedCommand(t *testing.T) {
	st := newStore(t)
	if err := st.EnsureNode("n1", 100); err != nil {
		t.Fatal(err)
	}
	id, err := Set(st, "n1", "demo", "gain", int64(3), "cli", 100)
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Fatal("want nonzero command id")
	}
	cmds, _ := st.CommandLog("n1")
	if len(cmds) != 1 || cmds[0].Verb != "set" {
		t.Fatalf("got %+v", cmds)
	}
	if cmds[0].IssuedBy != "cli" {
		t.Fatalf("issued_by = %q, want cli", cmds[0].IssuedBy)
	}
	if !strings.Contains(cmds[0].Args, `"value":3`) {
		t.Fatalf("args lost type: %s", cmds[0].Args)
	}
}

func TestInstallFromReaderComputesCRCAndSize(t *testing.T) {
	st := newStore(t)
	st.EnsureNode("n1", 100)
	img := []byte("hello-image-bytes")
	wantCRC := int64(command.CRC32(img))
	id, err := Install(st, "n1", "hello", bytes.NewReader(img),
		InstallOpts{IntervalS: 30, Lifecycle: "run-once", Runlevel: 3}, "cli", 100)
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Fatal("want run command enqueued")
	}
	got, _ := st.Payload(wantCRC)
	if string(got) != string(img) {
		t.Fatalf("payload not registered under crc %d", wantCRC)
	}
	cmds, _ := st.CommandLog("n1")
	if len(cmds) != 1 || cmds[0].Verb != "run" {
		t.Fatalf("got %+v", cmds)
	}
	if !strings.Contains(cmds[0].Args, `"crc":`) || !strings.Contains(cmds[0].Args, `"size":17`) {
		t.Fatalf("run args missing crc/size: %s", cmds[0].Args)
	}
}

func TestResolveNodeID(t *testing.T) {
	st := newStore(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4", 100) // auto-names it
	n, _ := st.GetNode("aabbccddeeff")
	// MAC passes through:
	if got, _ := ResolveNodeID(st, "aabbccddeeff"); got != "aabbccddeeff" {
		t.Fatalf("MAC: got %q", got)
	}
	// Name resolves:
	if got, _ := ResolveNodeID(st, n.Name); got != "aabbccddeeff" {
		t.Fatalf("name: got %q", got)
	}
	// Unknown errors:
	if _, err := ResolveNodeID(st, "nope"); err == nil {
		t.Fatal("want error for unknown name")
	}
}
```

- [ ] **Step 2: Run tests, verify they fail to compile**

Run: `go test ./internal/control/...`
Expected: FAIL — package/functions don't exist.

- [ ] **Step 3: Implement `internal/control/control.go`**

```go
// Package control is porta's headless operations layer: it orchestrates
// command-queue writes and node resolution so the cobra CLI and the web UI
// share one implementation. Presentation (printing, HTML) stays in the
// callers; control returns structured results.
package control

import (
	"fmt"
	"io"

	"github.com/davidg238/porta/internal/command"
	"github.com/davidg238/porta/internal/config"
	"github.com/davidg238/porta/internal/store"
)

// InstallOpts mirrors the install knobs the CLI exposes.
type InstallOpts struct {
	CRC       int64 // 0 → compute from image
	IntervalS int64
	Triggers  []string
	Runlevel  int
	Lifecycle string // "" → run-once
}

// Set enqueues a per-app config write. issuedBy is "cli" or "web".
func Set(st *store.Store, id, app, key string, value any, issuedBy string, now int64) (int64, error) {
	c, err := command.Set(app, key, value)
	if err != nil {
		return 0, err
	}
	return st.EnqueueCommand(id, c.Verb, c.ArgsJSON, issuedBy, now)
}

func SetConsole(st *store.Store, id string, on bool, issuedBy string, now int64) (int64, error) {
	c := command.SetConsole(on)
	return st.EnqueueCommand(id, c.Verb, c.ArgsJSON, issuedBy, now)
}

func SetPollInterval(st *store.Store, id string, secs int64, issuedBy string, now int64) (int64, error) {
	if err := st.SetPollInterval(id, secs); err != nil {
		return 0, err
	}
	c := command.SetPollInterval(secs)
	return st.EnqueueCommand(id, c.Verb, c.ArgsJSON, issuedBy, now)
}

func SetMaxOffline(st *store.Store, id string, secs int64) error { return st.SetMaxOffline(id, secs) }

func Rename(st *store.Store, id, name string) error { return st.SetNodeName(id, name) }

func Uninstall(st *store.Store, id, name, issuedBy string, now int64) (int64, error) {
	c := command.Stop(name)
	return st.EnqueueCommand(id, c.Verb, c.ArgsJSON, issuedBy, now)
}

// Install reads the image bytes, registers the payload under its CRC32-IEEE,
// and enqueues a run. Accepts a reader so a browser upload (no temp file) and
// the CLI (os.Open) both work.
func Install(st *store.Store, id, name string, img io.Reader, opts InstallOpts, issuedBy string, now int64) (int64, error) {
	data, err := io.ReadAll(img)
	if err != nil {
		return 0, err
	}
	crc := opts.CRC
	if crc == 0 {
		crc = int64(command.CRC32(data))
	}
	triggers, err := command.TriggersFromFlags(opts.Triggers, opts.IntervalS)
	if err != nil {
		return 0, err
	}
	runCmd, err := command.Run(command.RunSpec{
		Name: name, CRC: crc, Size: int64(len(data)),
		Triggers: triggers, Runlevel: opts.Runlevel, Lifecycle: opts.Lifecycle,
	})
	if err != nil {
		return 0, err
	}
	if err := st.RegisterPayload(crc, name, data); err != nil {
		return 0, err
	}
	return st.EnqueueCommand(id, runCmd.Verb, runCmd.ArgsJSON, issuedBy, now)
}

// isMAC reports whether s is exactly 12 lowercase hex digits.
func isMAC(s string) bool {
	if len(s) != 12 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// ResolveNodeID turns a <node> arg (MAC or friendly name) into a node id.
func ResolveNodeID(st *store.Store, nodeArg string) (string, error) {
	if isMAC(nodeArg) {
		return nodeArg, nil
	}
	n, err := st.NodeByName(nodeArg)
	if err != nil {
		return "", err
	}
	if n == nil {
		return "", fmt.Errorf("no node named %q", nodeArg)
	}
	return n.ID, nil
}

var _ = config.InferScalar // InferScalar stays in config; callers infer before Set.
```

Remove the unused `config` import line if the linter objects (it is only referenced in a
doc note; drop `"github.com/davidg238/porta/internal/config"` and the `var _ =` line).

- [ ] **Step 4: Refactor `internal/portacli/mutate.go` to call control**

Replace the bodies of `runInstall`, `runUninstall`, `runSetPollInterval`, `runDeviceSet`,
`runDeviceSetConsole` so they delegate and keep their `fmt.Printf` lines. Example
(`runDeviceSet`):

```go
func runDeviceSet(out io.Writer, st *store.Store, id, app, key, valueStr string, now int64) error {
	value := config.InferScalar(valueStr)
	cmdID, err := control.Set(st, id, app, key, value, "cli", now)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: enqueued set %s.%s=%v (command #%d)\n", id, app, key, value, cmdID)
	return nil
}
```

And `runInstall` reads the file then calls control:

```go
func runInstall(st *store.Store, id, name, path string, opts installOpts, now int64) error {
	if !strings.HasSuffix(path, ".bin") {
		return fmt.Errorf("unsupported file %q (B1 accepts only prebuilt .bin)", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	cmdID, err := control.Install(st, id, name, f, control.InstallOpts{
		CRC: opts.CRC, IntervalS: opts.IntervalS, Triggers: opts.Triggers,
		Runlevel: opts.Runlevel, Lifecycle: opts.Lifecycle,
	}, "cli", now)
	if err != nil {
		return err
	}
	fmt.Printf("%s: registered %s; enqueued run (command #%d)\n", id, name, cmdID)
	return nil
}
```

Add `"github.com/davidg238/porta/internal/control"` to imports; drop now-unused imports
(`io` may still be needed for `runDeviceSet`'s `out io.Writer`). In `resolve.go`, delete
`resolveNodeID` + `isMAC` and replace call sites with `control.ResolveNodeID(st, …)` (or
keep a one-line `func resolveNodeID(st *store.Store, a string) (string, error) { return control.ResolveNodeID(st, a) }`
to minimize churn).

- [ ] **Step 5: Run all tests**

Run: `go test ./...`
Expected: PASS (control tests + unchanged portacli mutate tests).

- [ ] **Step 6: Commit**

```bash
git add internal/control/ internal/portacli/mutate.go internal/portacli/resolve.go
git commit -m "feat(porta): control — extract write orchestration + ResolveNodeID (B4c task 1)"
```

---

### Task 2: `internal/control` — read projections (desired-vs-observed)

**Files:**
- Create: `internal/control/view.go`, `internal/control/view_test.go`
- Modify: `internal/portacli/inspect.go`

- [ ] **Step 1: Write failing test for `DesiredVsObserved`**

Create `internal/control/view_test.go`:

```go
package control

import "testing"

func TestDesiredVsObservedMarksPendingAndConverged(t *testing.T) {
	st := newStore(t)
	st.EnsureNode("n1", 100)
	// desired: demo.gain=2 (set), demo.mode="fast"
	Set(st, "n1", "demo", "gain", int64(2), "cli", 100)
	Set(st, "n1", "demo", "mode", "fast", "cli", 101)
	// observed report echoes only gain=2
	st.InsertReport("n1", `{"config":{"demo":{"gain":2}}}`, "", 102)

	rows, err := DesiredVsObserved(st, "n1", "demo")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]ConfigRow{}
	for _, r := range rows {
		got[r.Key] = r
	}
	if got["gain"].Marker != "" { // matched → no marker
		t.Errorf("gain should be converged, got marker %q", got["gain"].Marker)
	}
	if got["mode"].Marker == "" { // desired present, observed absent → pending
		t.Errorf("mode should be pending")
	}
}

func TestRelativeAge(t *testing.T) {
	if RelativeAge(0, 100) != "never" {
		t.Error("zero ts = never")
	}
	if RelativeAge(95, 100) != "5s ago" {
		t.Errorf("got %q", RelativeAge(95, 100))
	}
}
```

- [ ] **Step 2: Run, verify fail**

Run: `go test ./internal/control/...`
Expected: FAIL — `DesiredVsObserved`, `ConfigRow`, `RelativeAge` undefined.

- [ ] **Step 3: Implement `internal/control/view.go`**

Move `relativeAge`→`RelativeAge`, `appsFromObserved`→`AppsFromObserved`,
`configFromObserved`→`ConfigFromObserved`, the `App` struct, and add `DesiredVsObserved`:

```go
package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/davidg238/porta/internal/config"
	"github.com/davidg238/porta/internal/store"
)

func RelativeAge(ts, now int64) string {
	if ts == 0 {
		return "never"
	}
	d := now - ts
	switch {
	case d <= 60:
		return fmt.Sprintf("%ds ago", d)
	case d <= 3600:
		return fmt.Sprintf("%dm ago", d/60)
	case d < 86400:
		return fmt.Sprintf("%dh ago", d/3600)
	default:
		return fmt.Sprintf("%dd ago", d/86400)
	}
}

type App struct {
	Name     string
	CRC      int64
	Runlevel int64
}

func AppsFromObserved(observed string) ([]App, error) {
	if observed == "" {
		return nil, nil
	}
	var obj struct {
		Apps map[string]struct {
			CRC      int64 `json:"crc"`
			Runlevel int64 `json:"runlevel"`
		} `json:"apps"`
	}
	if err := json.Unmarshal([]byte(observed), &obj); err != nil {
		return nil, err
	}
	var out []App
	for name, a := range obj.Apps {
		out = append(out, App{Name: name, CRC: a.CRC, Runlevel: a.Runlevel})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func ConfigFromObserved(observed string) map[string]map[string]any {
	if observed == "" {
		return map[string]map[string]any{}
	}
	var obj struct {
		Config map[string]map[string]any `json:"config"`
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(observed)))
	dec.UseNumber()
	if err := dec.Decode(&obj); err != nil || obj.Config == nil {
		return map[string]map[string]any{}
	}
	return obj.Config
}

// ConfigRow is one desired-vs-observed row for an app's key.
type ConfigRow struct {
	Key             string
	Desired         any
	Observed        any
	DesiredPresent  bool
	ObservedPresent bool
	Marker          string // "", "(pending)", "(drift)", "converged" per config.Marker
	ReissueCount    int    // self-heal count (≥2 → warn)
}

// DesiredVsObserved computes the union of desired ∪ observed keys for app,
// each tagged via config.Marker, plus the self-heal reissue count. This is
// the shared computation behind `device get` and the web Config panel.
func DesiredVsObserved(st *store.Store, id, app string) ([]ConfigRow, error) {
	cmds, err := st.CommandLog(id)
	if err != nil {
		return nil, err
	}
	n, err := st.GetNode(id)
	if err != nil {
		return nil, err
	}
	desired := config.ProjectDesiredForApp(cmds, app)
	observed := map[string]any{}
	if n != nil {
		if c := ConfigFromObserved(n.ObservedState)[app]; c != nil {
			observed = c
		}
	}
	seen := map[string]struct{}{}
	for k := range desired {
		seen[k] = struct{}{}
	}
	for k := range observed {
		seen[k] = struct{}{}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]ConfigRow, 0, len(keys))
	for _, k := range keys {
		d, dOK := desired[k]
		o, oOK := observed[k]
		out = append(out, ConfigRow{
			Key: k, Desired: d, Observed: o, DesiredPresent: dOK, ObservedPresent: oOK,
			Marker:       config.Marker(d, o, dOK, oOK),
			ReissueCount: config.ReconcileCount(cmds, app, k),
		})
	}
	return out, nil
}
```

- [ ] **Step 4: Refactor `inspect.go` to use the moved helpers**

Delete `relativeAge`, `appsFromObserved`, `configFromObserved`, `App`, `renderScalar`,
`unionKeys`, and the inline desired-vs-observed loop in `runDeviceGet`. Replace usages with
`control.RelativeAge`, `control.AppsFromObserved`, and rebuild `runDeviceGet` over
`control.DesiredVsObserved`:

```go
func runDeviceGet(out io.Writer, st *store.Store, id, app, key string) error {
	rows, err := control.DesiredVsObserved(st, id, app)
	if err != nil {
		return err
	}
	render := func(r control.ConfigRow) (string, string) {
		ds, os := "--", "--"
		if r.DesiredPresent {
			ds = fmt.Sprintf("%v", r.Desired)
		}
		if r.ObservedPresent {
			os = fmt.Sprintf("%v", r.Observed)
		}
		return ds, os
	}
	if key != "" {
		for _, r := range rows {
			if r.Key != key {
				continue
			}
			ds, os := render(r)
			line := fmt.Sprintf("%s: %s.%s desired=%s observed=%s", id, app, key, ds, os)
			if r.Marker != "" {
				line += " " + r.Marker
			}
			fmt.Fprintln(out, line)
			if r.ReissueCount >= 2 {
				fmt.Fprintf(out, "%s: ⚠ %s.%s: self-healed %d×\n", id, app, key, r.ReissueCount)
			}
			return nil
		}
		fmt.Fprintf(out, "%s: %s.%s desired=-- observed=--\n", id, app, key)
		return nil
	}
	fmt.Fprintf(out, "%s: config for %s\n", id, app)
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  KEY\tDESIRED\tOBSERVED\t")
	for _, r := range rows {
		ds, os := render(r)
		fmt.Fprintf(w, "  %s\t%s\t%s\t%s\n", r.Key, ds, os, r.Marker)
	}
	w.Flush()
	for _, r := range rows {
		if r.ReissueCount >= 2 {
			fmt.Fprintf(out, "%s: ⚠ %s.%s: self-healed %d×\n", id, app, r.Key, r.ReissueCount)
		}
	}
	return nil
}
```

Update `newContainerListCmd` to use `control.AppsFromObserved`, and `relativeAge` call
sites (`newScanCmd`, `newPingCmd`, `newDeviceShowCmd`) to `control.RelativeAge`. Add the
`control` import; drop `config` if no longer referenced in `inspect.go`.

- [ ] **Step 5: Run all tests**

Run: `go test ./...`
Expected: PASS (existing `device get` / `container list` / `scan` tests unchanged in behavior).

- [ ] **Step 6: Commit**

```bash
git add internal/control/view.go internal/control/view_test.go internal/portacli/inspect.go
git commit -m "feat(porta): control — extract read projections + DesiredVsObserved (B4c task 2)"
```

---

### Task 3: `internal/web` scaffold + serve wiring

**Files:**
- Create: `internal/web/web.go`, `internal/web/templates/base.html`, `internal/web/templates/index.html`, `internal/web/assets/style.css`, `internal/web/assets/htmx.min.js`, `internal/web/web_test.go`
- Modify: `internal/portacli/serve.go`

- [ ] **Step 1: Vendor htmx**

```bash
mkdir -p internal/web/assets internal/web/templates
curl -sL https://unpkg.com/htmx.org@2.0.4/dist/htmx.min.js -o internal/web/assets/htmx.min.js
test -s internal/web/assets/htmx.min.js && head -c 40 internal/web/assets/htmx.min.js
```
Expected: a non-empty minified JS file (begins with a license comment / `(function`).

- [ ] **Step 2: Write the failing scaffold test**

Create `internal/web/web_test.go`:

```go
package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/davidg238/porta/internal/store"
)

func testStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func serve(t *testing.T, st *store.Store) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	New(st).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestIndexRendersNavAndAssets(t *testing.T) {
	st := testStore(t)
	srv := serve(t, st)

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, resp)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if !strings.Contains(body, "Nodes") || !strings.Contains(body, "Command Log") {
		t.Errorf("nav missing: %s", body)
	}
	// htmx asset served:
	js, _ := http.Get(srv.URL + "/assets/htmx.min.js")
	if js.StatusCode != 200 {
		t.Errorf("htmx asset status %d", js.StatusCode)
	}
	// unknown path 404s (the "/" handler must not swallow):
	nf, _ := http.Get(srv.URL + "/nope")
	if nf.StatusCode != 404 {
		t.Errorf("unknown path got %d, want 404", nf.StatusCode)
	}
}
```

Add a `readBody` helper at the bottom of the test file:

```go
func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
```
(import `io`.)

- [ ] **Step 3: Run, verify fail**

Run: `go test ./internal/web/...`
Expected: FAIL — `New`/`Register` undefined.

- [ ] **Step 4: Implement `internal/web/web.go`**

```go
// Package web serves porta's htmx operator console on the shared HTTP mux.
// It reads through internal/store and writes through internal/control; it
// holds no node state and pushes nothing — every dynamic region is polled.
package web

import (
	"embed"
	"html/template"
	"net/http"
	"time"

	"github.com/davidg238/porta/internal/store"
)

//go:embed templates/*.html assets/*
var content embed.FS

// Handler renders the operator console.
type Handler struct {
	st   *store.Store
	now  func() int64
	tmpl *template.Template
}

// New builds a Handler. now defaults to wall-clock epoch seconds.
func New(st *store.Store) *Handler {
	return &Handler{
		st:   st,
		now:  func() int64 { return time.Now().Unix() },
		tmpl: template.Must(template.ParseFS(content, "templates/*.html")),
	}
}

// Register mounts all routes on mux. "/" is the catch-all; it 404s any path
// it does not own so it never shadows sibling routes like /health.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.Handle("/assets/", http.FileServer(http.FS(content)))
	mux.HandleFunc("/", h.handleIndex)
	mux.HandleFunc("/n/", h.handleNode)
	mux.HandleFunc("/log", h.handleLog)
	mux.HandleFunc("/partials/nodes", h.handleNodesPartial)
	mux.HandleFunc("/partials/log", h.handleLogPartial)
	// per-node partials + form POSTs are registered under /n/ and dispatched
	// in handleNode / handleNodeAction (Tasks 5-8).
}

// handleIndex renders the fleet page. Task 4 fills the node table partial.
func (h *Handler) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	h.render(w, "index", map[string]any{"Title": "Nodes"})
}

// render executes a template and writes 500 on error.
func (h *Handler) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
```

Add temporary stubs so the package compiles (filled in later tasks):

```go
func (h *Handler) handleNode(w http.ResponseWriter, r *http.Request)          { http.NotFound(w, r) }
func (h *Handler) handleLog(w http.ResponseWriter, r *http.Request)           { h.render(w, "index", map[string]any{"Title": "Command Log"}) }
func (h *Handler) handleNodesPartial(w http.ResponseWriter, r *http.Request)  { w.Write([]byte("<tbody></tbody>")) }
func (h *Handler) handleLogPartial(w http.ResponseWriter, r *http.Request)    { w.Write([]byte("<tbody></tbody>")) }
```

- [ ] **Step 5: Create `internal/web/templates/base.html`**

> **Template-naming rule (load-bearing):** all files are parsed into ONE template set, so
> every `{{define}}` name is global. Page bodies therefore CANNOT all be named `"main"` — they
> must have unique names (`"index"`, `"node"`, `"log"`). Shared chrome lives in unique-named
> `"head"`/`"foot"` partials that each page includes. `head` references `.Title`, so every
> page's data must carry a `Title` (the maps and `detailVM` both do).

```html
{{define "head"}}<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>porta · {{.Title}}</title>
  <link rel="stylesheet" href="/assets/style.css">
  <script src="/assets/htmx.min.js"></script>
</head>
<body>
  <header><b>porta</b>
    <nav><a href="/">Nodes</a> · <a href="/log">Command Log</a></nav>
  </header>
  <main>{{end}}
{{define "foot"}}</main></body></html>{{end}}
```

- [ ] **Step 6: Create `internal/web/templates/index.html`**

```html
{{define "index"}}{{template "head" .}}
<h1>{{.Title}}</h1>
<p class="subtitle">fleet overview (node table lands in Task 4)</p>
{{template "foot" .}}{{end}}
```

- [ ] **Step 7: Create `internal/web/assets/style.css`** (minimal, ~60 lines)

```css
:root { --green:#2a8a2a; --amber:#d08000; --red:#c33; --bg:#fafafa; --line:#ddd; }
* { box-sizing: border-box; }
body { font: 14px/1.5 system-ui, sans-serif; margin: 0; background: var(--bg); color: #222; }
header { display:flex; gap:1rem; align-items:baseline; padding:.6rem 1rem; border-bottom:2px solid #333; background:#fff; }
header nav a { text-decoration:none; color:#06c; }
main { padding: 1rem; }
h1 { font-size: 1.3rem; margin:.2rem 0 .6rem; }
.subtitle { color:#777; margin:.2rem 0 1rem; }
table { width:100%; border-collapse:collapse; }
th { text-align:left; font-size:.7rem; text-transform:uppercase; letter-spacing:.04em; color:#888; border-bottom:1px solid #999; padding:.3rem .4rem; }
td { padding:.35rem .4rem; border-bottom:1px solid var(--line); }
tr.node-row:hover { background:#f0f0f0; cursor:pointer; }
.dot-green { color:var(--green); } .dot-red { color:var(--red); } .dot-amber { color:var(--amber); }
.gauge { position:relative; background:#eee; border-radius:3px; height:16px; min-width:120px; overflow:hidden; }
.gauge > .fill { height:100%; border-radius:3px; }
.gauge.green .fill { background:var(--green); } .gauge.amber .fill { background:var(--amber); } .gauge.red .fill { background:var(--red); opacity:.6; }
.gauge > .lbl { position:absolute; left:6px; top:0; line-height:16px; font-size:.7rem; color:#000; }
section { background:#fff; border:1px solid var(--line); border-radius:6px; padding:.7rem .9rem; margin:.7rem 0; }
section h2 { font-size:.9rem; margin:0 0 .5rem; }
form.action { display:flex; gap:.4rem; align-items:center; flex-wrap:wrap; margin:.3rem 0; }
input, select { padding:.25rem .4rem; border:1px solid #bbb; border-radius:4px; font:inherit; }
button { padding:.3rem .7rem; border:1px solid #06c; background:#06c; color:#fff; border-radius:4px; cursor:pointer; }
.confirm { color:var(--green); font-size:.8rem; }
.warn { color:var(--red); font-size:.8rem; }
code { background:#f0f0f0; padding:0 .2rem; border-radius:3px; }
```

- [ ] **Step 8: Wire into `serve.go`**

In `internal/portacli/serve.go`, inside the `if httpPort > 0 {` block, after
`srv, err := httpsrv.New(...)` and its error check, before `srv.Run` is launched:

```go
web.New(st).Register(srv.Mux)
```
Add import `"github.com/davidg238/porta/internal/web"`.

- [ ] **Step 9: Run tests**

Run: `go test ./internal/web/... ./internal/portacli/...`
Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add internal/web/ internal/portacli/serve.go
git commit -m "feat(porta): web — scaffold operator console + serve wiring (B4c task 3)"
```

---

### Task 4: Check-in gauge + node list page

**Files:**
- Create: `internal/web/gauge.go`, `internal/web/gauge_test.go`, `internal/web/templates/nodes.html`
- Modify: `internal/web/web.go` (replace `handleNodesPartial` stub; build node-row view models), `internal/web/templates/index.html`

- [ ] **Step 1: Write failing gauge tests**

Create `internal/web/gauge_test.go`:

```go
package web

import "testing"

func TestCheckinFilling(t *testing.T) {
	// last seen 10s ago, polls every 30s, max-offline 300s, now arbitrary.
	g := Checkin(true, 100, 30, 300, 110)
	if g.Color != "green" {
		t.Errorf("color=%s", g.Color)
	}
	if g.FillPct < 30 || g.FillPct > 40 { // 10/30 ≈ 33%
		t.Errorf("fill=%d", g.FillPct)
	}
	if g.Label == "" || g.Online != true {
		t.Errorf("label=%q online=%v", g.Label, g.Online)
	}
}

func TestCheckinOverdue(t *testing.T) {
	g := Checkin(true, 100, 30, 300, 140) // 40s since seen, expected 30s
	if g.Color != "amber" || g.FillPct != 100 || g.Online != true {
		t.Errorf("got %+v", g)
	}
}

func TestCheckinOffline(t *testing.T) {
	g := Checkin(true, 100, 30, 300, 500) // 400s > max-offline
	if g.Color != "red" || g.Online != false {
		t.Errorf("got %+v", g)
	}
}

func TestCheckinNeverSeen(t *testing.T) {
	g := Checkin(false, 0, 30, 300, 500)
	if g.Color != "red" || g.Online != false || g.FillPct != 0 {
		t.Errorf("got %+v", g)
	}
}

func TestHumanizeDur(t *testing.T) {
	cases := map[int64]string{5: "5s", 90: "1m", 1800: "30m", 7200: "2h"}
	for in, want := range cases {
		if got := humanizeDur(in); got != want {
			t.Errorf("humanizeDur(%d)=%q want %q", in, got, want)
		}
	}
}
```

- [ ] **Step 2: Run, verify fail**

Run: `go test ./internal/web/... -run Checkin`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement `internal/web/gauge.go`**

```go
package web

import "fmt"

// CheckinState is the render model for the next-check-in gauge. It doubles as
// the online/offline indicator: Color matches the status dot.
type CheckinState struct {
	Online  bool
	Color   string // "green" | "amber" | "red"
	FillPct int    // 0..100
	Label   string // "every 30s · next ~8s" | "overdue 1s" | "offline (>5m)"
}

// Checkin derives the gauge from last-seen + poll interval + max-offline.
//   elapsed <= interval        → green, filling, "every <iv> · next ~<remain>"
//   interval < elapsed <= max  → amber, full,    "overdue <elapsed-interval>"
//   elapsed > max OR never     → red,   (0 or full), "offline (>{max})"
func Checkin(seenValid bool, lastSeen, pollIntervalS, maxOfflineS, now int64) CheckinState {
	if !seenValid {
		return CheckinState{Online: false, Color: "red", FillPct: 0, Label: "never seen"}
	}
	elapsed := now - lastSeen
	if elapsed < 0 {
		elapsed = 0
	}
	switch {
	case elapsed <= pollIntervalS:
		pct := 0
		if pollIntervalS > 0 {
			pct = int(elapsed * 100 / pollIntervalS)
		}
		remain := pollIntervalS - elapsed
		return CheckinState{
			Online: true, Color: "green", FillPct: pct,
			Label: fmt.Sprintf("every %s · next ~%s", humanizeDur(pollIntervalS), humanizeDur(remain)),
		}
	case elapsed <= maxOfflineS:
		return CheckinState{
			Online: true, Color: "amber", FillPct: 100,
			Label: fmt.Sprintf("overdue %s", humanizeDur(elapsed-pollIntervalS)),
		}
	default:
		return CheckinState{
			Online: false, Color: "red", FillPct: 100,
			Label: fmt.Sprintf("offline (>%s)", humanizeDur(maxOfflineS)),
		}
	}
}

// humanizeDur renders seconds as a compact "5s"/"30m"/"2h" string.
func humanizeDur(s int64) string {
	switch {
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%dm", s/60)
	default:
		return fmt.Sprintf("%dh", s/3600)
	}
}
```

- [ ] **Step 4: Build the node-row view model + partial handler**

In `web.go` add the view model and replace the `handleNodesPartial` stub:

```go
type nodeRowVM struct {
	ID, Name, Kind, IP, SeenAgo, Summary string
	Gauge                                CheckinState
}

func (h *Handler) nodeRows(now int64) ([]nodeRowVM, error) {
	nodes, err := h.st.ListNodes()
	if err != nil {
		return nil, err
	}
	out := make([]nodeRowVM, 0, len(nodes))
	for _, n := range nodes {
		seen := "never"
		var lastSeen int64
		if n.LastSeen.Valid {
			lastSeen = n.LastSeen.Int64
			seen = control.RelativeAge(lastSeen, now)
		}
		out = append(out, nodeRowVM{
			ID: n.ID, Name: n.Name, Kind: n.Kind, IP: n.SourceAddr, SeenAgo: seen,
			Summary: summarize(n.ObservedState),
			Gauge:   Checkin(n.LastSeen.Valid, lastSeen, n.PollIntervalS, n.MaxOfflineS, now),
		})
	}
	return out, nil
}

// summarize renders the node-list "state summary" cell.
func summarize(observed string) string {
	apps, _ := control.AppsFromObserved(observed)
	cfg := control.ConfigFromObserved(observed)
	keys := 0
	for _, m := range cfg {
		keys += len(m)
	}
	if len(apps) == 0 {
		return fmt.Sprintf("idle · %d cfg", keys)
	}
	names := make([]string, 0, len(apps))
	for _, a := range apps {
		names = append(names, a.Name)
	}
	return fmt.Sprintf("%s · %d cfg", strings.Join(names, ","), keys)
}

func (h *Handler) handleNodesPartial(w http.ResponseWriter, r *http.Request) {
	rows, err := h.nodeRows(h.now())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	h.render(w, "nodes-rows", rows)
}
```
Add imports `"fmt"`, `"strings"`, `"github.com/davidg238/porta/internal/control"`.

- [ ] **Step 5: Create `internal/web/templates/nodes.html`**

```html
{{define "nodes-table"}}
<table>
  <thead><tr><th></th><th>Name</th><th>Kind</th><th>Last seen</th><th>IP</th><th>State</th><th>Next check-in</th></tr></thead>
  <tbody id="nodes" hx-get="/partials/nodes" hx-trigger="every 2s" hx-swap="outerHTML">
    {{template "nodes-rows" .Rows}}
  </tbody>
</table>
{{end}}

{{define "nodes-rows"}}<tbody id="nodes" hx-get="/partials/nodes" hx-trigger="every 2s" hx-swap="outerHTML">
{{range .}}<tr class="node-row" onclick="location.href='/n/{{.ID}}'">
  <td class="dot-{{.Gauge.Color}}">{{if .Gauge.Online}}●{{else}}○{{end}}</td>
  <td><b>{{.Name}}</b></td><td>{{.Kind}}</td><td>{{.SeenAgo}}</td><td>{{.IP}}</td><td>{{.Summary}}</td>
  <td><div class="gauge {{.Gauge.Color}}"><div class="fill" style="width:{{.Gauge.FillPct}}%"></div><span class="lbl">{{.Gauge.Label}}</span></div></td>
</tr>{{end}}
</tbody>{{end}}
```

Note: `nodes-rows` is rendered both standalone (the 2s poll, `outerHTML`-swapping the
`<tbody>`) and embedded in `nodes-table`. Both render the same `<tbody>` wrapper so the swap
is idempotent.

- [ ] **Step 6: Update `index.html` to mount the table**

```html
{{define "index"}}{{template "head" .}}
<h1>Nodes</h1>
{{template "nodes-table" .}}
{{template "foot" .}}{{end}}
```

And in `handleIndex`, pass rows:

```go
func (h *Handler) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	rows, err := h.nodeRows(h.now())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	h.render(w, "index", map[string]any{"Title": "Nodes", "Rows": rows})
}
```

- [ ] **Step 7: Add a render test**

Append to `web_test.go`:

```go
func TestNodeTableRendersRowAndGauge(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	srv := serve(t, st)

	body := readBody(t, mustGet(t, srv.URL+"/"))
	if !strings.Contains(body, "192.168.1.9") || !strings.Contains(body, "gauge") {
		t.Errorf("row/gauge missing: %s", body)
	}
	// partial endpoint returns just the tbody:
	p := readBody(t, mustGet(t, srv.URL+"/partials/nodes"))
	if !strings.Contains(p, "<tbody") || !strings.Contains(p, "aabbccddeeff") {
		t.Errorf("partial missing tbody/node: %s", p)
	}
}

func mustGet(t *testing.T, url string) *http.Response {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}
```

- [ ] **Step 8: Run tests, commit**

Run: `go test ./internal/web/...`
Expected: PASS.

```bash
git add internal/web/
git commit -m "feat(porta): web — check-in gauge + node list page (B4c task 4)"
```

---

### Task 5: Per-node detail page — read sections

**Files:**
- Create: `internal/web/pages.go` (move node handler here), `internal/web/templates/node.html`
- Modify: `internal/web/web.go` (remove `handleNode` stub; add detail view models + partial routing), `internal/store/data.go` (add `RecentData`)

- [ ] **Step 1: Add `RecentData` to the store (failing test first)**

Append to `internal/store/data_test.go` (create if absent):

```go
func TestRecentDataReturnsNewestFirstLimited(t *testing.T) {
	st, _ := Open(":memory:")
	defer st.Close()
	for i := int64(1); i <= 5; i++ {
		st.InsertData("n1", 100+i, i, "metric", "pm25", i, "", "int")
	}
	rows, err := st.RecentData("n1", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0].TS != 105 {
		t.Fatalf("want newest-first 3 rows, got %+v", rows)
	}
}
```

Run: `go test ./internal/store/... -run RecentData` → FAIL.

Implement in `data.go`:

```go
// RecentData returns the device's newest <= limit rows, newest first.
func (s *Store) RecentData(deviceID string, limit int) ([]DataRow, error) {
	rows, err := s.db.Query(
		`SELECT ts, seq, COALESCE(kind,''), COALESCE(name,''), value, COALESCE(text,''), COALESCE(value_type,'')
		 FROM data_log WHERE device_id = ? ORDER BY ts DESC, seq DESC LIMIT ?`, deviceID, limit)
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
```

Run: `go test ./internal/store/... -run RecentData` → PASS.

- [ ] **Step 2: Write failing detail-page test**

Append to `web_test.go`:

```go
func TestNodeDetailRendersSections(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	control.Set(st, "aabbccddeeff", "demo", "gain", int64(2), "cli", 1000)
	st.InsertReport("aabbccddeeff", `{"config":{"demo":{"gain":2}},"apps":{"demo":{"crc":99,"runlevel":3}}}`, "", 1001)
	st.InsertData("aabbccddeeff", 1002, 1, "metric", "pm25", int64(12), "", "int")
	srv := serve(t, st)

	body := readBody(t, mustGet(t, srv.URL+"/n/aabbccddeeff"))
	for _, want := range []string{"demo", "gain", "pm25", "Config", "Telemetry", "Pending", "Containers", "Actions"} {
		if !strings.Contains(body, want) {
			t.Errorf("detail page missing %q", want)
		}
	}
	// unknown node 404s:
	if mustGet(t, srv.URL+"/n/deadbeef0000").StatusCode != 404 {
		t.Error("unknown node should 404")
	}
}
```

Run: `go test ./internal/web/... -run NodeDetail` → FAIL.

- [ ] **Step 3: Implement detail view models + handler in `pages.go`**

Move `handleNode` out of `web.go` (delete the stub) into `internal/web/pages.go`:

```go
package web

import (
	"net/http"
	"strings"

	"github.com/davidg238/porta/internal/control"
	"github.com/davidg238/porta/internal/store"
)

type detailVM struct {
	Title    string
	ID       string
	Name     string
	Kind     string
	IP       string
	EUI      string
	PollIntv string
	Gauge    CheckinState
	Config   []control.ConfigRow
	ConfApp  string
	Telem    []store.DataRow
	Pending  []store.Command
	Apps     []control.App
}

// handleNode dispatches GET /n/<id> (full page) and the /n/<id>/... routes
// (partials in this task, form POSTs in Tasks 6-8).
func (h *Handler) handleNode(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/n/")
	parts := strings.SplitN(rest, "/", 2)
	idArg := parts[0]
	id, err := control.ResolveNodeID(h.st, idArg)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	n, err := h.st.GetNode(id)
	if err != nil || n == nil {
		http.NotFound(w, r)
		return
	}
	if len(parts) == 2 && parts[1] != "" {
		h.handleNodeSub(w, r, n, parts[1]) // partials + actions (later tasks)
		return
	}
	h.render(w, "node", h.detailVM(n))
}

// handleNodeSub routes the per-node sub-paths. Tasks 6-8 extend the switch.
func (h *Handler) handleNodeSub(w http.ResponseWriter, r *http.Request, n *store.Node, sub string) {
	switch sub {
	case "header":
		h.render(w, "node-header", h.detailVM(n))
	case "config":
		h.render(w, "node-config", h.detailVM(n))
	case "telemetry":
		h.render(w, "node-telemetry", h.detailVM(n))
	case "pending":
		h.render(w, "node-pending", h.detailVM(n))
	case "containers":
		h.render(w, "node-containers", h.detailVM(n))
	default:
		http.NotFound(w, r)
	}
}

func (h *Handler) detailVM(n *store.Node) detailVM {
	now := h.now()
	// Config: pick the first app present in desired ∪ observed (single-app
	// nodes are the norm today; multi-app shows the first alphabetically).
	app := firstApp(n)
	cfg, _ := control.DesiredVsObserved(h.st, n.ID, app)
	telem, _ := h.st.RecentData(n.ID, 10)
	pending, _ := h.st.UndeliveredCommands(n.ID)
	apps, _ := control.AppsFromObserved(n.ObservedState)
	var lastSeen int64
	if n.LastSeen.Valid {
		lastSeen = n.LastSeen.Int64
	}
	return detailVM{
		Title: n.Name, ID: n.ID, Name: n.Name, Kind: n.Kind, IP: n.SourceAddr, EUI: n.ID,
		PollIntv: humanizeDur(n.PollIntervalS),
		Gauge:    Checkin(n.LastSeen.Valid, lastSeen, n.PollIntervalS, n.MaxOfflineS, now),
		Config:   cfg, ConfApp: app, Telem: telem, Pending: pending, Apps: apps,
	}
}

// firstApp returns an app name to show in the Config panel: the first
// observed app, else the first desired app, else "" (empty config table).
func firstApp(n *store.Node) string {
	if apps, _ := control.AppsFromObserved(n.ObservedState); len(apps) > 0 {
		return apps[0].Name
	}
	cfg := control.ConfigFromObserved(n.ObservedState)
	for app := range cfg {
		return app
	}
	return ""
}
```

In `web.go`'s `Register`, the `/n/` route already points at `handleNode`; remove the old
`handleNode` stub from `web.go`.

- [ ] **Step 4: Create `internal/web/templates/node.html`**

```html
{{define "node"}}{{template "head" .}}
<a href="/">← all nodes</a>
<div id="hdr" hx-get="/n/{{.ID}}/header" hx-trigger="every 2s" hx-swap="outerHTML">{{template "node-header" .}}</div>

<section id="config" hx-get="/n/{{.ID}}/config" hx-trigger="every 2s" hx-swap="outerHTML">{{template "node-config" .}}</section>
<section id="telemetry" hx-get="/n/{{.ID}}/telemetry" hx-trigger="every 2s" hx-swap="outerHTML">{{template "node-telemetry" .}}</section>
<section id="pending" hx-get="/n/{{.ID}}/pending" hx-trigger="every 2s" hx-swap="outerHTML">{{template "node-pending" .}}</section>
<section id="actions">{{template "node-actions" .}}</section>
<section id="containers" hx-get="/n/{{.ID}}/containers" hx-trigger="every 5s" hx-swap="outerHTML">{{template "node-containers" .}}</section>
{{template "foot" .}}{{end}}

{{define "node-header"}}<div id="hdr" hx-get="/n/{{.ID}}/header" hx-trigger="every 2s" hx-swap="outerHTML">
  <h1>{{.Name}} <span class="dot-{{.Gauge.Color}}">{{if .Gauge.Online}}● online{{else}}○ offline{{end}}</span></h1>
  <p class="subtitle">{{.Kind}} · {{.IP}} · eui {{.EUI}} · poll {{.PollIntv}}</p>
  <div class="gauge {{.Gauge.Color}}" style="width:280px;height:20px"><div class="fill" style="width:{{.Gauge.FillPct}}%"></div><span class="lbl">{{.Gauge.Label}}</span></div>
</div>{{end}}

{{define "node-config"}}<section id="config" hx-get="/n/{{.ID}}/config" hx-trigger="every 2s" hx-swap="outerHTML">
  <h2>Config — desired vs observed{{if .ConfApp}} · {{.ConfApp}}{{end}}</h2>
  {{if .Config}}<table><thead><tr><th>Key</th><th>Desired</th><th>Observed</th><th></th></tr></thead><tbody>
  {{range .Config}}<tr><td>{{.Key}}</td>
    <td>{{if .DesiredPresent}}{{.Desired}}{{else}}—{{end}}</td>
    <td>{{if .ObservedPresent}}{{.Observed}}{{else}}—{{end}}</td>
    <td>{{.Marker}}{{if ge .ReissueCount 2}} <span class="warn">⚠ self-healed {{.ReissueCount}}×</span>{{end}}</td></tr>{{end}}
  </tbody></table>{{else}}<p class="subtitle">no config</p>{{end}}
</section>{{end}}

{{define "node-telemetry"}}<section id="telemetry" hx-get="/n/{{.ID}}/telemetry" hx-trigger="every 2s" hx-swap="outerHTML">
  <h2>Telemetry · last 10</h2>
  {{if .Telem}}<table><tbody>{{range .Telem}}<tr><td>{{.TS}}</td><td>{{.Name}}</td><td>{{.Value}}</td><td>{{.ValueType}}</td></tr>{{end}}</tbody></table>{{else}}<p class="subtitle">no telemetry</p>{{end}}
</section>{{end}}

{{define "node-pending"}}<section id="pending" hx-get="/n/{{.ID}}/pending" hx-trigger="every 2s" hx-swap="outerHTML">
  <h2>Pending commands</h2>
  {{if .Pending}}<table><tbody>{{range .Pending}}<tr><td>#{{.ID}}</td><td>{{.Verb}}</td><td><code>{{.Args}}</code></td></tr>{{end}}</tbody></table>{{else}}<p class="subtitle">none undelivered</p>{{end}}
</section>{{end}}

{{define "node-containers"}}<section id="containers" hx-get="/n/{{.ID}}/containers" hx-trigger="every 5s" hx-swap="outerHTML">
  <h2>Containers</h2>
  {{if .Apps}}<table><tbody>{{range .Apps}}<tr><td>{{.Name}}</td><td>crc {{.CRC}}</td><td>runlevel {{.Runlevel}}</td></tr>{{end}}</tbody></table>{{else}}<p class="subtitle">none observed</p>{{end}}
  <!-- install/uninstall forms added in Task 7 -->
</section>{{end}}

{{define "node-actions"}}<section id="actions"><h2>Actions</h2><p class="subtitle">forms added in Task 6</p></section>{{end}}
```

Each partial template re-emits its own wrapper element (`outerHTML` swap is idempotent),
mirroring the `nodes-rows` pattern from Task 4.

- [ ] **Step 5: Run tests, commit**

Run: `go test ./internal/web/... ./internal/store/...`
Expected: PASS.

```bash
git add internal/web/ internal/store/data.go internal/store/data_test.go
git commit -m "feat(porta): web — per-node detail read sections + store.RecentData (B4c task 5)"
```

---

### Task 6: Per-node write forms

**Files:**
- Modify: `internal/web/partials.go` (create), `internal/web/pages.go` (route POSTs in `handleNodeSub`), `internal/web/templates/node.html` (fill `node-actions`)

- [ ] **Step 1: Write failing form-POST test**

Append to `web_test.go`:

```go
func TestSetFormEnqueuesWebCommand(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	srv := serve(t, st)

	form := url.Values{"app": {"demo"}, "key": {"gain"}, "value": {"3"}}
	resp, err := http.PostForm(srv.URL+"/n/aabbccddeeff/set", form)
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, resp)
	if resp.StatusCode != 200 || !strings.Contains(body, "queued") {
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	cmds, _ := st.CommandLog("aabbccddeeff")
	if len(cmds) != 1 || cmds[0].Verb != "set" || cmds[0].IssuedBy != "web" {
		t.Fatalf("want one web 'set' command, got %+v", cmds)
	}
}
```
(import `net/url`.)

Run: `go test ./internal/web/... -run SetForm` → FAIL.

- [ ] **Step 2: Implement the form handlers in `partials.go`**

```go
package web

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/davidg238/porta/internal/command"
	"github.com/davidg238/porta/internal/config"
	"github.com/davidg238/porta/internal/control"
	"github.com/davidg238/porta/internal/store"
)

// confirm renders the post-write confirmation + the refreshed pending panel,
// so a single swap shows both "queued #N" and the new queue state.
func (h *Handler) confirm(w http.ResponseWriter, n *store.Node, msg string) {
	vm := h.detailVM(n)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<p class="confirm">%s — delivers on next check-in (%s)</p>`, msg, vm.Gauge.Label)
	_ = h.tmpl.ExecuteTemplate(w, "node-pending", vm)
}

func (h *Handler) postSet(w http.ResponseWriter, r *http.Request, n *store.Node) {
	app, key, val := r.FormValue("app"), r.FormValue("key"), r.FormValue("value")
	id, err := control.Set(h.st, n.ID, app, key, config.InferScalar(val), "web", h.now())
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	h.confirm(w, n, fmt.Sprintf("queued #%d set %s.%s=%s", id, app, key, val))
}

func (h *Handler) postConsole(w http.ResponseWriter, r *http.Request, n *store.Node) {
	on := r.FormValue("state") == "on"
	id, err := control.SetConsole(h.st, n.ID, on, "web", h.now())
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	h.confirm(w, n, fmt.Sprintf("queued #%d set-console %s", id, r.FormValue("state")))
}

func (h *Handler) postPollInterval(w http.ResponseWriter, r *http.Request, n *store.Node) {
	secs, err := command.ParseDurationSeconds(r.FormValue("dur"))
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	id, _ := control.SetPollInterval(h.st, n.ID, secs, "web", h.now())
	h.confirm(w, n, fmt.Sprintf("queued #%d set-poll-interval %ds", id, secs))
}

func (h *Handler) postMaxOffline(w http.ResponseWriter, r *http.Request, n *store.Node) {
	secs, err := command.ParseDurationSeconds(r.FormValue("dur"))
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	_ = control.SetMaxOffline(h.st, n.ID, secs)
	// store-only: refresh header (gauge depends on max-offline).
	n2, _ := h.st.GetNode(n.ID)
	h.render(w, "node-header", h.detailVM(n2))
}

func (h *Handler) postRename(w http.ResponseWriter, r *http.Request, n *store.Node) {
	name := r.FormValue("name")
	if name == "" {
		http.Error(w, "empty name", 400)
		return
	}
	_ = control.Rename(h.st, n.ID, name)
	n2, _ := h.st.GetNode(n.ID)
	h.render(w, "node-header", h.detailVM(n2))
}

var _ = strconv.Itoa // keep strconv if unused elsewhere; remove if linter objects
```

Remove the `strconv` import + the `var _` line if not needed.

- [ ] **Step 3: Route the POSTs in `handleNodeSub`**

Extend the switch in `pages.go`:

```go
	case "set":
		h.postSet(w, r, n)
	case "console":
		h.postConsole(w, r, n)
	case "poll-interval":
		h.postPollInterval(w, r, n)
	case "max-offline":
		h.postMaxOffline(w, r, n)
	case "rename":
		h.postRename(w, r, n)
```
Place these cases before `default:`.

- [ ] **Step 4: Fill `node-actions` template**

Replace the `node-actions` define in `node.html`:

```html
{{define "node-actions"}}<section id="actions">
  <h2>Actions</h2>
  <div id="action-result"></div>
  <form class="action" hx-post="/n/{{.ID}}/set" hx-target="#pending" hx-swap="outerHTML">
    <b>set</b> <input name="app" placeholder="app" required> <input name="key" placeholder="key" required>
    <input name="value" placeholder="value" required> <button>queue</button>
  </form>
  <form class="action" hx-post="/n/{{.ID}}/console" hx-target="#pending" hx-swap="outerHTML">
    <b>console</b> <select name="state"><option>on</option><option>off</option></select> <button>queue</button>
  </form>
  <form class="action" hx-post="/n/{{.ID}}/poll-interval" hx-target="#pending" hx-swap="outerHTML">
    <b>poll-interval</b> <input name="dur" placeholder="30s" required> <button>queue</button>
  </form>
  <form class="action" hx-post="/n/{{.ID}}/max-offline" hx-target="#hdr" hx-swap="outerHTML">
    <b>max-offline</b> <input name="dur" placeholder="5m" required> <button>set</button>
  </form>
  <form class="action" hx-post="/n/{{.ID}}/rename" hx-target="#hdr" hx-swap="outerHTML">
    <b>rename</b> <input name="name" placeholder="new-name" required> <button>set</button>
  </form>
</section>{{end}}
```

Note the `set`/`console`/`poll-interval` forms target `#pending` (the confirm handler emits
the confirmation line + refreshed pending panel). `max-offline`/`rename` target `#hdr`.

- [ ] **Step 5: Run tests, commit**

Run: `go test ./internal/web/...`
Expected: PASS.

```bash
git add internal/web/
git commit -m "feat(porta): web — per-node write forms (B4c task 6)"
```

---

### Task 7: Container install (multipart upload) + uninstall

**Files:**
- Modify: `internal/web/partials.go` (add `postInstall`, `postUninstall`), `internal/web/pages.go` (route them), `internal/web/templates/node.html` (add install/uninstall forms to `node-containers`)

- [ ] **Step 1: Write failing multipart-upload test**

Append to `web_test.go`:

```go
func TestInstallUploadRegistersPayloadAndQueuesRun(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	srv := serve(t, st)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("name", "demo")
	mw.WriteField("interval", "30s")
	mw.WriteField("lifecycle", "run-once")
	fw, _ := mw.CreateFormFile("image", "demo.bin")
	fw.Write([]byte("IMAGE-BYTES"))
	mw.Close()

	resp, err := http.Post(srv.URL+"/n/aabbccddeeff/containers/install", mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, readBody(t, resp))
	}
	cmds, _ := st.CommandLog("aabbccddeeff")
	if len(cmds) != 1 || cmds[0].Verb != "run" || cmds[0].IssuedBy != "web" {
		t.Fatalf("want web run command, got %+v", cmds)
	}
}
```
(imports `bytes`, `mime/multipart`.)

Run: `go test ./internal/web/... -run InstallUpload` → FAIL.

- [ ] **Step 2: Implement `postInstall` / `postUninstall`**

Add to `partials.go`:

```go
const maxUpload = 8 << 20 // 8 MiB cap on uploaded images

func (h *Handler) postInstall(w http.ResponseWriter, r *http.Request, n *store.Node) {
	if err := r.ParseMultipartForm(maxUpload); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	file, hdr, err := r.FormFile("image")
	if err != nil {
		http.Error(w, "image file required", 400)
		return
	}
	defer file.Close()
	name := r.FormValue("name")
	if name == "" {
		name = strings.TrimSuffix(hdr.Filename, ".bin")
	}
	opts := control.InstallOpts{Lifecycle: r.FormValue("lifecycle"), Runlevel: 3}
	if iv := r.FormValue("interval"); iv != "" {
		secs, err := command.ParseDurationSeconds(iv)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		opts.IntervalS = secs
	}
	id, err := control.Install(h.st, n.ID, name, file, opts, "web", h.now())
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	h.confirm(w, n, fmt.Sprintf("queued #%d run %s (uploaded %d B)", id, name, hdr.Size))
}

func (h *Handler) postUninstall(w http.ResponseWriter, r *http.Request, n *store.Node) {
	name := r.FormValue("name")
	if name == "" {
		http.Error(w, "name required", 400)
		return
	}
	id, err := control.Uninstall(h.st, n.ID, name, "web", h.now())
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	h.confirm(w, n, fmt.Sprintf("queued #%d stop %s", id, name))
}
```
Add `"strings"` to the `partials.go` import set.

- [ ] **Step 3: Route them in `handleNodeSub`**

The sub-path for these is two segments (`containers/install`). Update `handleNode` to pass
the full remaining path, and match in `handleNodeSub`:

```go
	case "containers/install":
		h.postInstall(w, r, n)
	case "containers/uninstall":
		h.postUninstall(w, r, n)
```
`handleNode` already passes `parts[1]` (everything after `/n/<id>/`), so `containers/install`
arrives intact — no change needed there beyond confirming `SplitN(rest, "/", 2)` keeps the
tail joined (it does).

- [ ] **Step 4: Add forms to `node-containers`**

Insert before the closing `</section>` of the `node-containers` define (replacing the
`<!-- install/uninstall forms added in Task 7 -->` comment):

```html
  <form class="action" hx-post="/n/{{.ID}}/containers/install" hx-target="#pending" hx-swap="outerHTML" hx-encoding="multipart/form-data">
    <b>install</b> <input type="file" name="image" accept=".bin" required>
    <input name="name" placeholder="name"> <input name="interval" placeholder="30s">
    <select name="lifecycle"><option>run-once</option><option>run-loop</option></select>
    <button>upload &amp; queue run</button>
  </form>
  <form class="action" hx-post="/n/{{.ID}}/containers/uninstall" hx-target="#pending" hx-swap="outerHTML">
    <b>uninstall</b> <input name="name" placeholder="app" required> <button>queue stop</button>
  </form>
```

`hx-encoding="multipart/form-data"` makes htmx send the file as multipart.

- [ ] **Step 5: Run tests, commit**

Run: `go test ./internal/web/...`
Expected: PASS.

```bash
git add internal/web/
git commit -m "feat(porta): web — container install upload + uninstall (B4c task 7)"
```

---

### Task 8: Command audit page (`/log`)

**Files:**
- Modify: `internal/store/store.go` (add `LoggedCommand` + `RecentCommands`), `internal/web/web.go` (`handleLog`/`handleLogPartial` real impl), `internal/web/pages.go` or `partials.go` (log view model), `internal/web/templates/log.html` (create)

- [ ] **Step 1: Add `RecentCommands` to the store (failing test first)**

Append to `internal/store/store_test.go`:

```go
func TestRecentCommandsCrossDeviceNewestFirst(t *testing.T) {
	st, _ := Open(":memory:")
	defer st.Close()
	st.EnqueueCommand("n1", "set", `{"a":1}`, "cli", 100)
	st.EnqueueCommand("n2", "stop", `{}`, "web", 101)
	rows, err := st.RecentCommands(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].DeviceID != "n2" || rows[0].Verb != "stop" {
		t.Fatalf("want newest-first cross-device, got %+v", rows)
	}
}
```

Run: `go test ./internal/store/... -run RecentCommands` → FAIL.

Implement in `store.go` (near the other command queries):

```go
// LoggedCommand is a command queue row with its device id, for the global
// audit view (the per-device Command lacks device_id).
type LoggedCommand struct {
	Command
	DeviceID string
}

// RecentCommands returns the newest <= limit commands across all devices,
// newest first.
func (s *Store) RecentCommands(limit int) ([]LoggedCommand, error) {
	rows, err := s.db.Query(`SELECT `+cmdCols+`, device_id FROM command_queue ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LoggedCommand
	for rows.Next() {
		var c Command
		var dev string
		if err := rows.Scan(&c.ID, &c.Verb, &c.Args, &c.IssuedAt, &c.IssuedBy, &c.DeliveredAt, &dev); err != nil {
			return nil, err
		}
		out = append(out, LoggedCommand{Command: c, DeviceID: dev})
	}
	return out, rows.Err()
}
```
(`cmdCols` already lists `id, verb, args, issued_at, issued_by, delivered_at`; appending
`, device_id` keeps Scan order aligned.)

Run: `go test ./internal/store/... -run RecentCommands` → PASS.

- [ ] **Step 2: Write failing /log render test**

Append to `web_test.go`:

```go
func TestLogPageRendersCommands(t *testing.T) {
	st := testStore(t)
	st.EnqueueCommand("aabbccddeeff", "set", `{"app":"demo","key":"gain","value":3}`, "web", 1000)
	srv := serve(t, st)

	body := readBody(t, mustGet(t, srv.URL+"/log"))
	for _, want := range []string{"aabbccddeeff", "set", "web", "Command Log"} {
		if !strings.Contains(body, want) {
			t.Errorf("/log missing %q", want)
		}
	}
}
```

Run: `go test ./internal/web/... -run LogPage` → FAIL.

- [ ] **Step 3: Implement `handleLog` / `handleLogPartial` + view model**

Replace the `handleLog`/`handleLogPartial` stubs in `web.go`:

```go
type logRowVM struct {
	ID          int64
	DeviceID    string
	Verb        string
	Args        string
	IssuedBy    string
	QueuedAgo   string
	Delivered   string
}

func (h *Handler) logRows(now int64) ([]logRowVM, error) {
	cmds, err := h.st.RecentCommands(200)
	if err != nil {
		return nil, err
	}
	out := make([]logRowVM, 0, len(cmds))
	for _, c := range cmds {
		delivered := "pending"
		if c.DeliveredAt.Valid {
			delivered = control.RelativeAge(c.DeliveredAt.Int64, now)
		}
		out = append(out, logRowVM{
			ID: c.ID, DeviceID: c.DeviceID, Verb: c.Verb, Args: c.Args, IssuedBy: c.IssuedBy,
			QueuedAgo: control.RelativeAge(c.IssuedAt, now), Delivered: delivered,
		})
	}
	return out, nil
}

func (h *Handler) handleLog(w http.ResponseWriter, r *http.Request) {
	rows, err := h.logRows(h.now())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	h.render(w, "log", map[string]any{"Title": "Command Log", "Rows": rows})
}

func (h *Handler) handleLogPartial(w http.ResponseWriter, r *http.Request) {
	rows, err := h.logRows(h.now())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	h.render(w, "log-rows", rows)
}
```
Add `"github.com/davidg238/porta/internal/control"` import to `web.go` if not present.

- [ ] **Step 4: Create `internal/web/templates/log.html`**

```html
{{define "log"}}{{template "head" .}}
<h1>Command Log</h1>
<table>
  <thead><tr><th>#</th><th>Node</th><th>Verb</th><th>Args</th><th>By</th><th>Queued</th><th>Delivered</th></tr></thead>
  <tbody id="log" hx-get="/partials/log" hx-trigger="every 2s" hx-swap="outerHTML">{{template "log-rows" .Rows}}</tbody>
</table>
{{template "foot" .}}{{end}}

{{define "log-rows"}}<tbody id="log" hx-get="/partials/log" hx-trigger="every 2s" hx-swap="outerHTML">
{{range .}}<tr><td>#{{.ID}}</td><td>{{.DeviceID}}</td><td>{{.Verb}}</td><td><code>{{.Args}}</code></td><td>{{.IssuedBy}}</td><td>{{.QueuedAgo}}</td><td>{{.Delivered}}</td></tr>{{end}}
</tbody>{{end}}
```

- [ ] **Step 5: Run tests, commit**

Run: `go test ./internal/web/... ./internal/store/...`
Expected: PASS.

```bash
git add internal/web/ internal/store/store.go internal/store/store_test.go
git commit -m "feat(porta): web — command audit page + store.RecentCommands (B4c task 8)"
```

---

### Task 9: End-to-end integration test + final review

**Files:**
- Create: `internal/web/integration_test.go`

- [ ] **Step 1: Write the integration test**

```go
package web

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/davidg238/porta/internal/store"
)

func TestEndToEndOperatorFlow(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	srv := serve(t, st)

	// 1. fleet page lists the node
	if !strings.Contains(readBody(t, mustGet(t, srv.URL+"/")), "aabbccddeeff") {
		t.Fatal("node missing from fleet page")
	}
	// 2. detail page renders
	if mustGet(t, srv.URL+"/n/aabbccddeeff").StatusCode != 200 {
		t.Fatal("detail page not 200")
	}
	// 3. queue a set via the form
	http.PostForm(srv.URL+"/n/aabbccddeeff/set",
		url.Values{"app": {"demo"}, "key": {"gain"}, "value": {"7"}})
	// 4. upload an image
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("name", "demo")
	mw.WriteField("interval", "1m")
	fw, _ := mw.CreateFormFile("image", "demo.bin")
	fw.Write([]byte("BYTES"))
	mw.Close()
	http.Post(srv.URL+"/n/aabbccddeeff/containers/install", mw.FormDataContentType(), &buf)

	// 5. both land in the queue, tagged web
	cmds, _ := st.CommandLog("aabbccddeeff")
	if len(cmds) != 2 {
		t.Fatalf("want 2 queued commands, got %d: %+v", len(cmds), cmds)
	}
	var verbs []string
	for _, c := range cmds {
		if c.IssuedBy != "web" {
			t.Errorf("cmd #%d issued_by=%q want web", c.ID, c.IssuedBy)
		}
		verbs = append(verbs, c.Verb)
	}
	// 6. payload registered
	if ok, _ := st.PayloadExists(int64(crc32OfBytes("BYTES"))); !ok {
		t.Error("payload not registered")
	}
	// 7. /log shows them
	if !strings.Contains(readBody(t, mustGet(t, srv.URL+"/log")), "demo") {
		t.Error("/log missing the queued set")
	}
	_ = verbs
	_ = store.DefaultPollIntervalS
}

func crc32OfBytes(s string) uint32 {
	// mirror command.CRC32 to avoid an import cycle in the assert
	return crc32ieee([]byte(s))
}
```

Since `command.CRC32` is the canonical helper, import it instead of reimplementing — replace
`crc32OfBytes`/`crc32ieee` with:

```go
import "github.com/davidg238/porta/internal/command"
// ...
if ok, _ := st.PayloadExists(int64(command.CRC32([]byte("BYTES")))); !ok {
```
and delete the `crc32OfBytes` helper.

- [ ] **Step 2: Run the full suite**

Run: `go test ./...`
Expected: PASS across all packages.

- [ ] **Step 3: Manual smoke (optional but recommended)**

```bash
go run ./cmd/porta serve --http-port 6970 --db /tmp/porta-b4c.db &
sleep 1
curl -s localhost:6970/health
curl -s localhost:6970/ | grep -o 'porta · Nodes'
kill %1
```
Expected: `/health` JSON, the page title present.

- [ ] **Step 4: Commit**

```bash
git add internal/web/integration_test.go
git commit -m "test(porta): web — end-to-end operator flow (B4c task 9)"
```

---

## Self-review notes (author check against the spec)

- **Spec §4.1 control writes + ResolveNodeID** → Task 1. **§4.1 read projections / DesiredVsObserved** → Task 2. ✔
- **§4.2 web package (embed/template/CSS/htmx)** → Task 3. ✔
- **§5.1 node list + gauge** → Task 4. **§5.2 detail read sections** → Task 5. **§6 write forms** → Task 6. **§6 container upload/uninstall** → Task 7. **§5.3 /log** → Task 8. ✔
- **§7 gauge math** → Task 4 (`Checkin`) with green/amber/red + never-seen boundary tests. ✔
- **§8 polling only** → every dynamic region uses `hx-trigger="every 2s"` (containers 5s); no SSE. ✔
- **§9 binding/auth** → inherited from B4a (`serve.go` mounts on `srv.Mux` behind the allowlist); nothing new. ✔
- **§10 testing** → control unit tests (T1/T2), web httptest per route (T3-T8), gauge table tests (T4), integration (T9). ✔
- **Store gaps** the spec implied: `RecentData` (T5) and `RecentCommands`+`LoggedCommand` (T8) added additively — no schema change. ✔
- **Type consistency:** control funcs take `issuedBy string`; web passes `"web"`, CLI `"cli"`. `Install` takes `io.Reader`. `Checkin(seenValid bool, lastSeen, pollIntervalS, maxOfflineS, now int64)` used identically in `nodeRows` and `detailVM`. `DesiredVsObserved`→`[]control.ConfigRow` rendered in both `device get` and `node-config`. ✔

## Execution note
Subagent-driven development: dispatch one fresh subagent per task (Task 0 first), review between tasks. Tasks 1→2→3 are sequential (later tasks import `internal/control` and `internal/web`); Tasks 4-8 are sequential within `internal/web` but each is small. Keep host suites green at every commit.
