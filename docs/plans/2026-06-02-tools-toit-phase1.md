# `/tools/toit` Phase 1 — `porta run` + node identity (Implementation Plan)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `porta run <app.toit> -d <node>` — compile a Toit app, relocate it to a container image, and deliver it via the existing payload/run path — guarded by an SDK-version match against the node's self-reported identity.

**Architecture:** A new `internal/toolchain` package wraps the `toit` CLI behind an injectable `Runner` and a narrating `Executor` (every command is announced with its exact argv; `-v` streams child output). `porta run` resolves the node, reads its reported `chip`/`sdk` (new `nodes` columns, populated from the report), refuses on SDK mismatch, builds the image (`toit compile --snapshot` + `toit tool snapshot-to-image -m32 --format=binary`), and reuses `control.Install` to register the payload and enqueue the `run`. Lifecycle + triggers are prompted; everything else is a flag.

**Tech Stack:** Go, cobra, sqlite (`mattn/go-sqlite3`), the Toit SDK CLI (`toit`).

**Scope:** Porta repo only. The nodus-side report emit (`chip`/`sdk`) is a separate-repo companion (see Task 8 note); until it lands, a node's SDK is unknown and `porta run` blocks — the porta side is fully unit-testable with a crafted report body and seeded identity.

---

### Task 1: Add `chip`/`sdk` to the `nodes` store

**Files:**
- Modify: `internal/store/store.go` (schema ~line 18-29; `Node` struct ~line 71-83; `nodeCols` ~line 149-151; `scanNode` ~line 153-161; add `UpdateNodeIdentity`)
- Test: `internal/store/store_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/store/store_test.go`:

```go
func TestUpdateNodeIdentity(t *testing.T) {
	st := openTestStore(t)
	if err := st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateNodeIdentity("aabbccddeeff", "esp32c6", "v2.0.0-alpha.192"); err != nil {
		t.Fatal(err)
	}
	n, err := st.GetNode("aabbccddeeff")
	if err != nil || n == nil {
		t.Fatalf("GetNode: %v / %v", n, err)
	}
	if n.Chip != "esp32c6" || n.Sdk != "v2.0.0-alpha.192" {
		t.Errorf("got chip=%q sdk=%q, want esp32c6 / v2.0.0-alpha.192", n.Chip, n.Sdk)
	}
	// Empty values must not clobber a known identity.
	if err := st.UpdateNodeIdentity("aabbccddeeff", "", ""); err != nil {
		t.Fatal(err)
	}
	n, _ = st.GetNode("aabbccddeeff")
	if n.Chip != "esp32c6" || n.Sdk != "v2.0.0-alpha.192" {
		t.Errorf("empty update clobbered identity: chip=%q sdk=%q", n.Chip, n.Sdk)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestUpdateNodeIdentity`
Expected: build failure — `n.Chip`/`n.Sdk` undefined and `UpdateNodeIdentity` undefined.

- [ ] **Step 3: Add the columns, struct fields, and method**

In `internal/store/store.go`, add two columns to the `nodes` `CREATE TABLE` (after `observed_state TEXT`):

```sql
  observed_state TEXT,
  chip TEXT,
  sdk TEXT
);
```

Add to the `Node` struct (after `ObservedState string`):

```go
	ObservedState string
	Chip          string
	Sdk           string
```

Extend `nodeCols` (append the two columns, COALESCEd to ''):

```go
const nodeCols = `id, COALESCE(name,''), COALESCE(source_addr,''), kind, first_seen, last_seen,
	COALESCE(poll_interval_s,30), COALESCE(max_offline_s,300), last_report_at,
	COALESCE(observed_state,''), COALESCE(chip,''), COALESCE(sdk,'')`
```

Extend `scanNode`'s `Scan` call (append the two fields, matching column order):

```go
	err := row.Scan(&n.ID, &n.Name, &n.SourceAddr, &n.Kind, &n.FirstSeen,
		&n.LastSeen, &n.PollIntervalS, &n.MaxOfflineS, &n.LastReportAt, &n.ObservedState,
		&n.Chip, &n.Sdk)
```

Add the method (near `SetNodeName`):

```go
// UpdateNodeIdentity records the node's self-reported firmware identity.
// Empty chip/sdk are COALESCEd so a report missing the field never clobbers a
// previously-known value.
func (s *Store) UpdateNodeIdentity(id, chip, sdk string) error {
	_, err := s.db.Exec(
		`UPDATE nodes SET chip = COALESCE(?, chip), sdk = COALESCE(?, sdk) WHERE id = ?`,
		nullStr(chip), nullStr(sdk), id)
	return err
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/`
Expected: PASS (whole package, since the schema/struct change touches shared code).

- [ ] **Step 5: Commit**

```bash
git add internal/store/store.go internal/store/store_test.go
git commit -m "feat(porta): nodes chip/sdk identity columns + UpdateNodeIdentity"
```

> **DB gotcha (no migration):** `CREATE TABLE IF NOT EXISTS` does **not** add
> columns to an already-created table, so an existing `porta.db` (e.g. the running
> soak) will fail `scanNode` after this change. Per [[porta-no-legacy]] we
> recreate rather than migrate: stop any running gateway and delete its
> `porta.db` (+ `-wal`/`-shm`) so the schema is recreated fresh. Tests use a fresh
> `t.TempDir()` db and are unaffected.

---

### Task 2: Ingest `chip`/`sdk` from the report

**Files:**
- Modify: `internal/handler/handler.go` (`writeReport`, ~line 142-162)
- Test: `internal/handler/handler_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/handler/handler_test.go` (mirrors the existing report tests; `newH` returns `(*Handler, *store.Store)`):

```go
func TestWriteReportStoresIdentity(t *testing.T) {
	h, st := newH(t)
	body := []byte(`{"apps":{},"config":{},"health":{},"chip":"esp32c6","sdk":"v2.0.0-alpha.192"}`)
	if err := h.Write("report?id=aabbccddeeff", "p:1", body); err != nil {
		t.Fatalf("Write: %v", err)
	}
	n, _ := st.GetNode("aabbccddeeff")
	if n == nil || n.Chip != "esp32c6" || n.Sdk != "v2.0.0-alpha.192" {
		t.Fatalf("identity not stored: %+v", n)
	}
	// A report without chip/sdk must not clobber the stored identity.
	if err := h.Write("report?id=aabbccddeeff", "p:1", []byte(`{"apps":{},"config":{}}`)); err != nil {
		t.Fatal(err)
	}
	n, _ = st.GetNode("aabbccddeeff")
	if n.Chip != "esp32c6" || n.Sdk != "v2.0.0-alpha.192" {
		t.Errorf("identity clobbered: chip=%q sdk=%q", n.Chip, n.Sdk)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/handler/ -run TestWriteReportStoresIdentity`
Expected: FAIL — `n.Chip`/`n.Sdk` are empty (ingestion not wired).

- [ ] **Step 3: Wire ingestion into `writeReport`**

In `internal/handler/handler.go`, the `obj` map and `field` helper already exist. `chip`/`sdk` are JSON strings, so decode them from `obj`. Add a small string-extract helper inside `writeReport` and call `UpdateNodeIdentity` after `InsertReport` succeeds (before `reconcileAfterReport`):

```go
	if err := h.store.InsertReport(id, observed, health, h.now()); err != nil {
		return err
	}
	// Self-reported firmware identity (additive; absent keys decode to "").
	strField := func(k string) string {
		raw, ok := obj[k]
		if !ok {
			return ""
		}
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return ""
		}
		return s
	}
	if err := h.store.UpdateNodeIdentity(id, strField("chip"), strField("sdk")); err != nil {
		h.log("porta: identity update error for %s: %v", id, err)
	}
	h.reconcileAfterReport(id, field("config"))
	return nil
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/handler/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/handler/handler.go internal/handler/handler_test.go
git commit -m "feat(porta): ingest node chip/sdk identity from report"
```

---

### Task 3: Narration engine — `Runner` + `Executor`

**Files:**
- Create: `internal/toolchain/exec.go`
- Test: `internal/toolchain/exec_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/toolchain/exec_test.go`:

```go
package toolchain

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// fakeRunner records invocations and returns canned results.
type fakeRunner struct {
	calls   [][]string
	results map[string]runResult // keyed by argv[0]
}
type runResult struct {
	stdout []byte
	err    error
}

func (f *fakeRunner) Run(name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	r := f.results[name]
	return r.stdout, r.err
}

func TestExecutorNarratesAndRuns(t *testing.T) {
	fr := &fakeRunner{results: map[string]runResult{"toit": {stdout: []byte("ok")}}}
	var log bytes.Buffer
	ex := NewExecutor(fr, &log, false)
	out, err := ex.Run("compile", "toit", "compile", "x.toit")
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "ok" {
		t.Errorf("out=%q, want ok", out)
	}
	if len(fr.calls) != 1 || fr.calls[0][0] != "toit" {
		t.Fatalf("calls=%v", fr.calls)
	}
	// Default (non-verbose) narration announces the step label and the argv.
	s := log.String()
	if !strings.Contains(s, "compile") || !strings.Contains(s, "toit compile x.toit") {
		t.Errorf("narration missing label/argv: %q", s)
	}
}

func TestExecutorReportsFailureWithCommand(t *testing.T) {
	fr := &fakeRunner{results: map[string]runResult{"toit": {err: errors.New("boom")}}}
	var log bytes.Buffer
	ex := NewExecutor(fr, &log, false)
	_, err := ex.Run("compile", "toit", "compile", "x.toit")
	if err == nil {
		t.Fatal("expected error")
	}
	// On failure the narration includes the rerunnable command.
	if !strings.Contains(log.String(), "toit compile x.toit") {
		t.Errorf("failure narration missing command: %q", log.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/toolchain/`
Expected: build failure — package/`NewExecutor`/`Runner` don't exist.

- [ ] **Step 3: Implement the engine**

Create `internal/toolchain/exec.go`:

```go
// Package toolchain wraps the Toit SDK CLI behind an injectable runner and a
// narrating executor, so porta can compile + relocate payloads while showing
// the operator every underlying command ("trainer wheels").
package toolchain

import (
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// Runner executes an external command and returns its combined stdout.
// The real implementation shells out; tests inject a fake.
type Runner interface {
	Run(name string, args ...string) ([]byte, error)
}

// ExecRunner runs commands via os/exec, returning combined output.
type ExecRunner struct{}

func (ExecRunner) Run(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

// Executor narrates and runs commands. When verbose, child output is written
// to out as it returns; otherwise only a tidy per-step summary is shown.
type Executor struct {
	r       Runner
	out     io.Writer
	verbose bool
	now     func() time.Time
}

// NewExecutor builds an Executor over r, narrating to out.
func NewExecutor(r Runner, out io.Writer, verbose bool) *Executor {
	return &Executor{r: r, out: out, verbose: verbose, now: time.Now}
}

// Run announces (label + exact argv), executes, and reports success/failure.
// On failure it prints the rerunnable command so the operator can retry by hand.
func (e *Executor) Run(label, name string, args ...string) ([]byte, error) {
	cmdline := name + " " + strings.Join(args, " ")
	fmt.Fprintf(e.out, "→ %s\n  %s\n", label, cmdline)
	start := e.now()
	out, err := e.r.Run(name, args...)
	if e.verbose && len(out) > 0 {
		fmt.Fprintf(e.out, "%s\n", out)
	}
	if err != nil {
		fmt.Fprintf(e.out, "✗ %s — %v\n  rerun: %s\n", label, err, cmdline)
		if !e.verbose && len(out) > 0 {
			fmt.Fprintf(e.out, "%s\n", out)
		}
		return out, fmt.Errorf("%s: %w", label, err)
	}
	fmt.Fprintf(e.out, "✓ %s (%s)\n", label, e.now().Sub(start).Round(time.Millisecond))
	return out, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/toolchain/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/toolchain/exec.go internal/toolchain/exec_test.go
git commit -m "feat(porta): toolchain narrating executor over injectable runner"
```

---

### Task 4: SDK version + conflict check

**Files:**
- Create: `internal/toolchain/sdk.go`
- Test: `internal/toolchain/sdk_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/toolchain/sdk_test.go`:

```go
package toolchain

import (
	"bytes"
	"strings"
	"testing"
)

func TestSDKVersionParsesToitVersion(t *testing.T) {
	fr := &fakeRunner{results: map[string]runResult{"toit": {stdout: []byte("v2.0.0-alpha.192\n")}}}
	ex := NewExecutor(fr, &bytes.Buffer{}, false)
	v, err := SDKVersion(ex)
	if err != nil {
		t.Fatal(err)
	}
	if v != "v2.0.0-alpha.192" {
		t.Errorf("got %q, want v2.0.0-alpha.192", v)
	}
}

func TestCheckSDK(t *testing.T) {
	if err := CheckSDK("v2.0.0-alpha.192", "v2.0.0-alpha.192"); err != nil {
		t.Errorf("match should pass: %v", err)
	}
	err := CheckSDK("v2.0.0-alpha.192", "v2.0.0-alpha.999")
	if err == nil {
		t.Fatal("mismatch should error")
	}
	if !strings.Contains(err.Error(), "v2.0.0-alpha.192") || !strings.Contains(err.Error(), "v2.0.0-alpha.999") {
		t.Errorf("error should name both versions: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/toolchain/ -run 'SDK'`
Expected: build failure — `SDKVersion`/`CheckSDK` undefined.

- [ ] **Step 3: Implement**

Create `internal/toolchain/sdk.go`:

```go
package toolchain

import (
	"fmt"
	"strings"
)

// SDKVersion returns the active Toit SDK version (`toit version`), trimmed.
func SDKVersion(ex *Executor) (string, error) {
	out, err := ex.Run("toit version", "toit", "version")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// CheckSDK errors when the active build SDK differs from the node's reported
// SDK — a relocated image only runs on the SDK it was built with.
func CheckSDK(nodeSDK, activeSDK string) error {
	if nodeSDK == activeSDK {
		return nil
	}
	return fmt.Errorf("SDK mismatch: node runs %q but build toolchain is %q — image would not run (use --force to override)",
		nodeSDK, activeSDK)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/toolchain/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/toolchain/sdk.go internal/toolchain/sdk_test.go
git commit -m "feat(porta): toolchain SDK version probe + match guard"
```

---

### Task 5: Build — compile + relocate to a container image

**Files:**
- Create: `internal/toolchain/build.go`
- Test: `internal/toolchain/build_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/toolchain/build_test.go`. The fake runner writes canned bytes to the `snapshot-to-image` `-o` target so `Build` can read the result:

```go
package toolchain

import (
	"bytes"
	"os"
	"testing"
)

// fileWritingRunner extends fakeRunner: when it sees a `-o <path>` arg, it
// writes canned image bytes there (simulating snapshot-to-image output).
type fileWritingRunner struct {
	calls   [][]string
	imgBytes []byte
}

func (f *fileWritingRunner) Run(name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-o" && len(f.imgBytes) > 0 && hasArg(args, "snapshot-to-image") {
			if err := os.WriteFile(args[i+1], f.imgBytes, 0o600); err != nil {
				return nil, err
			}
		}
	}
	return nil, nil
}
func hasArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func TestBuildCompilesAndRelocates(t *testing.T) {
	fr := &fileWritingRunner{imgBytes: []byte("IMAGEBYTES")}
	ex := NewExecutor(fr, &bytes.Buffer{}, false)
	img, err := Build(ex, "/tmp/app.toit")
	if err != nil {
		t.Fatal(err)
	}
	if string(img) != "IMAGEBYTES" {
		t.Errorf("got %q, want IMAGEBYTES", img)
	}
	// Expect a compile step then a snapshot-to-image -m32 --format=binary step.
	if len(fr.calls) != 2 {
		t.Fatalf("calls=%v", fr.calls)
	}
	if !hasArg(fr.calls[0], "compile") || !hasArg(fr.calls[0], "--snapshot") {
		t.Errorf("first call not a snapshot compile: %v", fr.calls[0])
	}
	c2 := fr.calls[1]
	if !hasArg(c2, "snapshot-to-image") || !hasArg(c2, "-m32") || !hasArg(c2, "--format=binary") {
		t.Errorf("second call not snapshot-to-image -m32 binary: %v", c2)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/toolchain/ -run TestBuild`
Expected: build failure — `Build` undefined.

- [ ] **Step 3: Implement**

Create `internal/toolchain/build.go`:

```go
package toolchain

import (
	"os"
	"path/filepath"
)

// Build compiles a Toit app to a snapshot, relocates it to a 32-bit binary
// container image, and returns the image bytes. All current ESP32 chips are
// 32-bit, so the relocation is `-m32 --format=binary` (the recipe nodus uses in
// host/build-envelope.sh); the image couples to the active SDK version, checked
// separately via CheckSDK. Temp artifacts are cleaned up.
func Build(ex *Executor, appPath string) ([]byte, error) {
	dir, err := os.MkdirTemp("", "porta-build-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	snap := filepath.Join(dir, "app.snapshot")
	img := filepath.Join(dir, "app.bin")

	if _, err := ex.Run("compile "+filepath.Base(appPath), "toit",
		"compile", "--snapshot", "-o", snap, appPath); err != nil {
		return nil, err
	}
	if _, err := ex.Run("relocate (esp32, -m32)", "toit",
		"tool", "snapshot-to-image", "-m32", "--format=binary", "-o", img, snap); err != nil {
		return nil, err
	}
	return os.ReadFile(img)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/toolchain/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/toolchain/build.go internal/toolchain/build_test.go
git commit -m "feat(porta): toolchain Build — compile + snapshot-to-image relocation"
```

---

### Task 6: `runDeploy` — the testable core of `porta run`

**Files:**
- Create: `internal/portacli/run.go`
- Test: `internal/portacli/run_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/portacli/run_test.go`. It seeds a node identity, injects a runner that emits a matching SDK version and image bytes, and asserts the payload + run land:

```go
package portacli

import (
	"bytes"
	"os"
	"testing"

	"github.com/davidg238/porta/internal/store"
	"github.com/davidg238/porta/internal/toolchain"
)

// stubRunner: `toit version` → fixed SDK; snapshot-to-image -o → canned bytes.
type stubRunner struct{ sdk string }

func (s stubRunner) Run(name string, args ...string) ([]byte, error) {
	for i := 0; i < len(args); i++ {
		if args[i] == "version" {
			return []byte(s.sdk + "\n"), nil
		}
	}
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-o" {
			for _, a := range args {
				if a == "snapshot-to-image" {
					_ = os.WriteFile(args[i+1], []byte("IMG"), 0o600)
				}
			}
		}
	}
	return nil, nil
}

func newRunStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/d.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestRunDeployHappyPath(t *testing.T) {
	st := newRunStore(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	st.UpdateNodeIdentity("aabbccddeeff", "esp32", "v2.0.0-alpha.192")
	ex := toolchain.NewExecutor(stubRunner{sdk: "v2.0.0-alpha.192"}, &bytes.Buffer{}, false)

	err := runDeploy(st, ex, "aabbccddeeff", "/tmp/app.toit",
		deployOpts{Name: "blink", Lifecycle: "run-loop", Triggers: []string{"boot"}, Runlevel: 3}, false, 2000)
	if err != nil {
		t.Fatalf("runDeploy: %v", err)
	}
	// A run command must now be queued.
	cmd, err := st.NextUndelivered("aabbccddeeff")
	if err != nil || cmd == nil || cmd.Verb != "run" {
		t.Fatalf("expected queued run, got %+v (err %v)", cmd, err)
	}
}

func TestRunDeployBlocksOnUnknownIdentity(t *testing.T) {
	st := newRunStore(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000) // no UpdateNodeIdentity
	ex := toolchain.NewExecutor(stubRunner{sdk: "v2.0.0-alpha.192"}, &bytes.Buffer{}, false)
	err := runDeploy(st, ex, "aabbccddeeff", "/tmp/app.toit",
		deployOpts{Name: "blink", Lifecycle: "run-once"}, false, 2000)
	if err == nil {
		t.Fatal("expected block on unknown identity")
	}
}

func TestRunDeployRefusesSDKMismatch(t *testing.T) {
	st := newRunStore(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	st.UpdateNodeIdentity("aabbccddeeff", "esp32", "v2.0.0-alpha.192")
	ex := toolchain.NewExecutor(stubRunner{sdk: "v9.9.9"}, &bytes.Buffer{}, false)
	err := runDeploy(st, ex, "aabbccddeeff", "/tmp/app.toit",
		deployOpts{Name: "blink", Lifecycle: "run-once"}, false, 2000)
	if err == nil {
		t.Fatal("expected SDK mismatch refusal")
	}
	// --force overrides.
	if err := runDeploy(st, ex, "aabbccddeeff", "/tmp/app.toit",
		deployOpts{Name: "blink", Lifecycle: "run-once"}, true, 2000); err != nil {
		t.Errorf("--force should override: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/portacli/ -run TestRunDeploy`
Expected: build failure — `runDeploy`/`deployOpts` undefined.

- [ ] **Step 3: Implement `runDeploy` + `deployOpts`**

Create `internal/portacli/run.go`:

```go
package portacli

import (
	"bytes"
	"fmt"

	"github.com/davidg238/porta/internal/control"
	"github.com/davidg238/porta/internal/store"
	"github.com/davidg238/porta/internal/toolchain"
)

type deployOpts struct {
	Name      string
	Lifecycle string // run-once | run-loop
	Triggers  []string
	Runlevel  int
	PowerMode string // "" → leave unchanged
}

// runDeploy is the testable core of `porta run`: identity + SDK guard, build,
// then register-payload + enqueue-run via control.Install. force skips the SDK
// match refusal.
func runDeploy(st *store.Store, ex *toolchain.Executor, id, appPath string, opts deployOpts, force bool, now int64) error {
	node, err := st.GetNode(id)
	if err != nil {
		return err
	}
	if node == nil || node.Sdk == "" {
		return fmt.Errorf("node %s hasn't reported its firmware identity yet — wait for a check-in (or flash it via `porta flash`) before deploying", id)
	}
	active, err := toolchain.SDKVersion(ex)
	if err != nil {
		return err
	}
	if !force {
		if err := toolchain.CheckSDK(node.Sdk, active); err != nil {
			return err
		}
	}
	img, err := toolchain.Build(ex, appPath)
	if err != nil {
		return err
	}
	cmdID, err := control.Install(st, id, opts.Name, bytes.NewReader(img), control.InstallOpts{
		Triggers: opts.Triggers, Runlevel: opts.Runlevel, Lifecycle: opts.Lifecycle,
	}, "cli", now)
	if err != nil {
		return err
	}
	fmt.Printf("%s: built %s (%d B), enqueued run (command #%d)\n", id, opts.Name, len(img), cmdID)
	if opts.PowerMode != "" {
		if _, err := control.SetPowerMode(st, id, opts.PowerMode, "cli", now); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/portacli/ -run TestRunDeploy`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/portacli/run.go internal/portacli/run_test.go
git commit -m "feat(porta): runDeploy core — identity/SDK guard, build, enqueue run"
```

---

### Task 7: Wire `porta run` into the CLI

**Files:**
- Modify: `internal/portacli/run.go` (add `newRunCmd`)
- Modify: `internal/portacli/root.go` (register the command)
- Test: `internal/portacli/run_test.go` (flag-registration check)

- [ ] **Step 1: Write the failing test**

Add to `internal/portacli/run_test.go`:

```go
func TestNewRunCmdRegistersFlags(t *testing.T) {
	cmd := newRunCmd()
	if cmd.Use == "" || cmd.Flags().Lookup("device") == nil {
		t.Fatal("run cmd missing device flag")
	}
	for _, f := range []string{"name", "lifecycle", "trigger", "runlevel", "power-mode", "force", "verbose"} {
		if cmd.Flags().Lookup(f) == nil {
			t.Errorf("missing --%s flag", f)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/portacli/ -run TestNewRunCmd`
Expected: build failure — `newRunCmd` undefined.

- [ ] **Step 3: Implement `newRunCmd` and register it**

Append to `internal/portacli/run.go`. The full import block for the file (after Tasks 6+7) is exactly:

```go
import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/davidg238/porta/internal/control"
	"github.com/davidg238/porta/internal/store"
	"github.com/davidg238/porta/internal/toolchain"
	"github.com/spf13/cobra"
)
```

Then add the command and prompt helpers:

```go
func newRunCmd() *cobra.Command {
	var device string
	var opts deployOpts
	var force, verbose bool
	cmd := &cobra.Command{
		Use:   "run <app.toit>",
		Short: "Compile a Toit app, relocate it, and deploy it to a node (jag-run analog)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appPath := args[0]
			if !strings.HasSuffix(appPath, ".toit") {
				return fmt.Errorf("expected a .toit source file, got %q", appPath)
			}
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			id, err := resolveNodeID(st, device)
			if err != nil {
				return err
			}
			if opts.Name == "" {
				base := filepath.Base(appPath)
				opts.Name = strings.TrimSuffix(base, filepath.Ext(base))
			}
			// Prompt for the two run-shape answers; flags win when set.
			if opts.Lifecycle == "" {
				opts.Lifecycle = promptChoice("Lifecycle", []string{"run-once", "run-loop"}, "run-once")
			}
			if len(opts.Triggers) == 0 {
				opts.Triggers = promptTriggers()
			}
			ex := toolchain.NewExecutor(toolchain.ExecRunner{}, cmd.OutOrStdout(), verbose)
			return runDeploy(st, ex, id, appPath, opts, force, nowSec())
		},
	}
	deviceFlag(cmd, &device)
	cmd.Flags().StringVar(&opts.Name, "name", "", "container name (default: source file stem)")
	cmd.Flags().StringVar(&opts.Lifecycle, "lifecycle", "", "run-once or run-loop (prompted if unset)")
	cmd.Flags().StringArrayVar(&opts.Triggers, "trigger", nil, "trigger spec (boot, gpio-high=21, …); prompted if unset")
	cmd.Flags().IntVar(&opts.Runlevel, "runlevel", 3, "runlevel")
	cmd.Flags().StringVar(&opts.PowerMode, "power-mode", "", "deep-sleep or always-on (optional)")
	cmd.Flags().BoolVar(&force, "force", false, "deploy even if the build SDK differs from the node's")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "stream every underlying tool call")
	return cmd
}

// promptChoice asks the user to pick from options, returning def on empty input.
func promptChoice(label string, options []string, def string) string {
	fmt.Printf("%s %v [%s]: ", label, options, def)
	var in string
	fmt.Scanln(&in)
	in = strings.TrimSpace(in)
	for _, o := range options {
		if in == o {
			return o
		}
	}
	return def
}

// promptTriggers asks for a space-separated trigger list (empty → none).
func promptTriggers() []string {
	fmt.Print("Triggers (space-separated: boot, gpio-high=21, …; empty = none): ")
	var line string
	fmt.Scanln(&line)
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	return strings.Fields(line)
}
```

Add `os` to imports only if used; remove if not. Then register in `internal/portacli/root.go` `AddCommand(...)`:

```go
		newMonitorCmd(),
		newRunCmd(),
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/portacli/`
Expected: PASS.

- [ ] **Step 5: Build + vet whole tree, then commit**

Run: `go build ./... && go vet ./...`
Expected: clean.

```bash
git add internal/portacli/run.go internal/portacli/root.go internal/portacli/run_test.go
git commit -m "feat(porta): porta run command (compile+relocate+deploy, jag-run analog)"
```

---

### Task 8: Document the report identity fields in PROTOCOL.md

**Files:**
- Modify: `docs/PROTOCOL.md` (the report section)

> **Cross-repo companion (not in this plan):** the nodus supervisor must emit
> `chip` and `sdk` in its report body for identity to populate. That is a
> separate change in `~/workspaceToit/nodus` (`src/report.toit`), requires the
> Toit skills, and needs the on-device API for reading chip + SDK version. Track
> it as its own small task. Until it lands, `porta run` correctly blocks on
> unknown identity.

- [ ] **Step 1: Add the fields to the report schema docs**

In `docs/PROTOCOL.md`, in the report/observed-state section, document the two additive top-level keys:

```markdown
- `chip` (string, optional): the node's chip model, e.g. `esp32`, `esp32c6`,
  `esp32s3`. Used by the gateway's `porta run` to validate payload/SDK
  compatibility. Absent on firmware that predates identity reporting.
- `sdk` (string, optional): the Toit SDK version the node firmware was built
  with, e.g. `v2.0.0-alpha.192`. `porta run` refuses to deploy an image built
  with a different SDK (overridable with `--force`). Absent → `porta run`
  blocks until the node reports it.
```

- [ ] **Step 2: Commit**

```bash
git add docs/PROTOCOL.md
git commit -m "docs(porta): document additive report chip/sdk identity fields"
```

---

## Final verification

- [ ] Run the full suite: `go build ./... && go vet ./... && go test ./...` — expected all green.
- [ ] Manual smoke (optional, needs `toit` on PATH and a node that has reported identity): `porta run examples/blink.toit -d <node> -v` and observe the narrated compile/relocate/upload steps + a queued run via `porta log -d <node>`.

## Notes for the implementer

- **No envelope fetch in Phase 1.** The payload relocation is `-m32 --format=binary` with the active SDK; `toitlang/envelopes` and chip selection are Phase 2 (`porta flash`).
- **`toit` on PATH.** `ExecRunner` invokes `toit` directly; if it's not found the error surfaces through the narrated step. (The toit-exe skill documents locating the SDK if a fully-qualified path is later wanted.)
- **`NextUndelivered`** is the existing store accessor used in Task 6's test to confirm the queued run; it already exists (used by `handler.Complete`).
