# Panic Decode (S6) — porta side — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `porta run` retains each deployed image's `.snapshot` into jag's decode cache, and `porta monitor` / a new `porta panic` command auto-decode `kind:"panic"` telemetry rows into readable stack traces via `jag decode`.

**Architecture:** A small `panicDecoder` seam (real impl shells out to `jag decode`) is reused by `monitor` (live tail) and `panic list`/`panic show` (retrospective browse). Retention is a `toolchain.RetainSnapshot` step run by `runDeploy` after a successful install (deployed-only, best-effort). All porta-side work is verified against synthetic `kind:"panic"` rows; the nodus capture change (forwarding panics over the wire) is a separate effort consuming `docs/PANIC-REPORTING.md`.

**Tech Stack:** Go 1.22, cobra CLI, existing `internal/apiclient` (S5 telemetry reads with `kind` filter + `DataRow.ID`), `internal/toolchain` (injectable `Runner`/`Executor`), `internal/telemetry` (`FormatLine`).

**Spec:** `docs/specs/2026-06-03-panic-decode-s6-design.md`. Contract doc: `docs/PANIC-REPORTING.md`.

---

## File structure

- Create `internal/portacli/decode.go` — `panicDecoder` seam, `jagDecoder`, and the shared panic render/summary helpers (used by `monitor.go` and `panic.go`).
- Create `internal/portacli/decode_test.go` — decoder + helper tests.
- Create `internal/toolchain/retain.go` — `RetainSnapshot` + `snapshotCacheDir`.
- Create `internal/toolchain/retain_test.go`.
- Create `internal/portacli/panic.go` — `porta panic` cobra group (`list`, `show`) + their testable cores.
- Create `internal/portacli/panic_test.go`.
- Modify `internal/toolchain/build.go` — `Build` returns the snapshot path + a cleanup func.
- Modify `internal/toolchain/build_test.go` — adjust to the new `Build` signature.
- Modify `internal/portacli/run.go` — consume the new `Build` signature; retain snapshot after install.
- Modify `internal/portacli/run_test.go` — stub `snapshot uuid`, set `$PORTA_SNAPSHOT_DIR`, assert retention.
- Modify `internal/portacli/monitor.go` — add the `panicDecoder` param + panic rendering + `--no-decode`.
- Modify `internal/portacli/monitor_test.go` — add the decoder arg to existing call sites; add a shared `fakeDecoder`; add panic-row tests.
- Modify `internal/portacli/root.go` — register `newPanicCmd()`.

---

### Task 1: `panicDecoder` seam + `jagDecoder`

**Files:**
- Create: `internal/portacli/decode.go`
- Test: `internal/portacli/decode_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/portacli/decode_test.go`:

```go
package portacli

import (
	"errors"
	"strings"
	"testing"
)

// recordingRunner satisfies toolchain.Runner; returns canned output and records argv.
type recordingRunner struct {
	out  []byte
	err  error
	argv []string
}

func (r *recordingRunner) Run(name string, args ...string) ([]byte, error) {
	r.argv = append([]string{name}, args...)
	return r.out, r.err
}

func TestJagDecoderSuccess(t *testing.T) {
	rr := &recordingRunner{out: []byte("UNHANDLED EXCEPTION: OUT_OF_BOUNDS\n  at main\n")}
	d := jagDecoder{r: rr}
	got, err := d.Decode("BLOB")
	if err != nil {
		t.Fatal(err)
	}
	if got != "UNHANDLED EXCEPTION: OUT_OF_BOUNDS\n  at main" {
		t.Errorf("got %q", got)
	}
	if strings.Join(rr.argv, " ") != "jag decode BLOB" {
		t.Errorf("argv = %v", rr.argv)
	}
}

func TestJagDecoderError(t *testing.T) {
	rr := &recordingRunner{out: []byte("No such file"), err: errors.New("exit status 1")}
	d := jagDecoder{r: rr}
	if _, err := d.Decode("BLOB"); err == nil {
		t.Fatal("expected error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/portacli/ -run TestJagDecoder -v`
Expected: FAIL — `undefined: jagDecoder`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/portacli/decode.go`:

```go
// internal/portacli/decode.go
package portacli

import (
	"fmt"
	"strings"

	"github.com/davidg238/porta/internal/toolchain"
)

// panicDecoder symbolicates a base64 trace blob into a readable stack trace.
// The seam keeps runMonitor and the panic commands unit-testable with a fake.
type panicDecoder interface {
	Decode(blob string) (string, error)
}

// jagDecoder shells out to `jag decode <blob>`, which resolves the blob's
// embedded program UUID against jag's local snapshot cache (populated by
// `porta run`, see toolchain.RetainSnapshot). It uses a plain Runner (not the
// narrating Executor) so decode adds no "→ jag decode …" noise to monitor output.
type jagDecoder struct{ r toolchain.Runner }

// newJagDecoder builds the production decoder over os/exec.
func newJagDecoder() jagDecoder { return jagDecoder{r: toolchain.ExecRunner{}} }

func (d jagDecoder) Decode(blob string) (string, error) {
	out, err := d.r.Run("jag", "decode", blob)
	if err != nil {
		return "", fmt.Errorf("jag decode: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/portacli/ -run TestJagDecoder -v`
Expected: PASS (both subtests).

- [ ] **Step 5: Commit**

```bash
git add internal/portacli/decode.go internal/portacli/decode_test.go
git commit -m "feat(portacli): panicDecoder seam + jagDecoder over jag decode"
```

---

### Task 2: Panic render + summary helpers

**Files:**
- Modify: `internal/portacli/decode.go`
- Test: `internal/portacli/decode_test.go`

- [ ] **Step 1: Write the failing test**

First, add `"bytes"` and `"github.com/davidg238/porta/internal/apiclient"` to the existing import block of `internal/portacli/decode_test.go` (it already imports `"errors"`, `"strings"`, `"testing"` from Task 1). Then append these tests:

```go
// localDecoder: ok=true returns a canned 2-line trace; ok=false fails (no snapshot).
type localDecoder struct{ ok bool }

func (d localDecoder) Decode(blob string) (string, error) {
	if !d.ok {
		return "", errors.New("no snapshot")
	}
	return "UNHANDLED EXCEPTION: OUT_OF_BOUNDS\n  at main.foo", nil
}

func TestRenderPanicDecoded(t *testing.T) {
	var b bytes.Buffer
	renderPanic(&b, apiclient.DataRow{TS: 100, Kind: "panic", Text: "BLOB"}, localDecoder{ok: true})
	s := b.String()
	if !strings.Contains(s, "‼ PANIC") || !strings.Contains(s, "  at main.foo") {
		t.Errorf("got %q", s)
	}
	if strings.Contains(s, "jag decode BLOB") {
		t.Errorf("decoded output should not show the raw blob: %q", s)
	}
}

func TestRenderPanicFallback(t *testing.T) {
	var b bytes.Buffer
	renderPanic(&b, apiclient.DataRow{TS: 100, Kind: "panic", Text: "BLOB"}, localDecoder{ok: false})
	s := b.String()
	if !strings.Contains(s, "no local snapshot") || !strings.Contains(s, "jag decode BLOB") {
		t.Errorf("got %q", s)
	}
}

func TestPanicSummary(t *testing.T) {
	ok := panicSummary(apiclient.DataRow{Text: "BLOB"}, localDecoder{ok: true})
	if ok != "UNHANDLED EXCEPTION: OUT_OF_BOUNDS" {
		t.Errorf("ok summary = %q", ok)
	}
	bad := panicSummary(apiclient.DataRow{Text: "BLOB"}, localDecoder{ok: false})
	if !strings.Contains(bad, "no local snapshot") {
		t.Errorf("fallback summary = %q", bad)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/portacli/ -run 'TestRenderPanic|TestPanicSummary' -v`
Expected: FAIL — `undefined: renderPanic` / `panicSummary`.

- [ ] **Step 3: Write minimal implementation**

Append to `internal/portacli/decode.go` (and add `"io"`, `"time"`, and `"github.com/davidg238/porta/internal/apiclient"` to its import block):

```go
// panicTime formats a panic row's epoch-seconds timestamp for display.
func panicTime(ts int64) string {
	return time.Unix(ts, 0).Format("2006-01-02 15:04:05")
}

// indentLines prefixes every line of s with prefix.
func indentLines(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

// renderPanic writes a panic row: a "‼ PANIC <time>" header, then the decoded
// trace (indented) or — on decode failure or a nil decoder — the raw blob plus
// a hint that the snapshot lives where the image was built.
func renderPanic(out io.Writer, r apiclient.DataRow, dec panicDecoder) {
	fmt.Fprintf(out, "‼ PANIC  %s\n", panicTime(r.TS))
	if dec != nil {
		if trace, err := dec.Decode(r.Text); err == nil {
			fmt.Fprintln(out, indentLines(trace, "  "))
			return
		}
	}
	fmt.Fprintf(out, "  (no local snapshot — decode where the image was built)\n  jag decode %s\n", r.Text)
}

// panicSummary is the one-line summary for `panic list`: the first non-empty
// decoded line, or a fallback marker when it cannot decode.
func panicSummary(r apiclient.DataRow, dec panicDecoder) string {
	if dec != nil {
		if trace, err := dec.Decode(r.Text); err == nil {
			for _, l := range strings.Split(trace, "\n") {
				if s := strings.TrimSpace(l); s != "" {
					return s
				}
			}
		}
	}
	return "(no local snapshot — decode where built)"
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/portacli/ -run 'TestRenderPanic|TestPanicSummary' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/portacli/decode.go internal/portacli/decode_test.go
git commit -m "feat(portacli): panic render + summary helpers"
```

---

### Task 3: `toolchain.RetainSnapshot` + cache dir

**Files:**
- Create: `internal/toolchain/retain.go`
- Test: `internal/toolchain/retain_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/toolchain/retain_test.go`:

```go
package toolchain

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// uuidRunner returns a fixed UUID for `toit tool snapshot uuid`.
type uuidRunner struct{ uuid string }

func (u uuidRunner) Run(name string, args ...string) ([]byte, error) {
	return []byte(u.uuid + "\n"), nil
}

func TestRetainSnapshotCopiesToCache(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("PORTA_SNAPSHOT_DIR", cache)

	snap := filepath.Join(t.TempDir(), "app.snapshot")
	if err := os.WriteFile(snap, []byte("SNAPBYTES"), 0o600); err != nil {
		t.Fatal(err)
	}

	ex := NewExecutor(uuidRunner{uuid: "abcd-uuid"}, &bytes.Buffer{}, false)
	uuid, err := RetainSnapshot(ex, snap)
	if err != nil {
		t.Fatal(err)
	}
	if uuid != "abcd-uuid" {
		t.Errorf("uuid = %q", uuid)
	}
	got, err := os.ReadFile(filepath.Join(cache, "abcd-uuid.snapshot"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "SNAPBYTES" {
		t.Errorf("cached snapshot = %q", got)
	}
}

func TestRetainSnapshotEmptyUUID(t *testing.T) {
	t.Setenv("PORTA_SNAPSHOT_DIR", t.TempDir())
	snap := filepath.Join(t.TempDir(), "app.snapshot")
	if err := os.WriteFile(snap, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	ex := NewExecutor(uuidRunner{uuid: ""}, &bytes.Buffer{}, false)
	if _, err := RetainSnapshot(ex, snap); err == nil {
		t.Fatal("expected error on empty uuid")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/toolchain/ -run TestRetainSnapshot -v`
Expected: FAIL — `undefined: RetainSnapshot`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/toolchain/retain.go`:

```go
package toolchain

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// snapshotCacheDir is jag's decode snapshot cache; `jag decode` reads
// <dir>/<uuid>.snapshot. Defaults to ~/.local/state/toit/snapshots, overridable
// via $PORTA_SNAPSHOT_DIR (used by tests).
func snapshotCacheDir() string {
	if d := os.Getenv("PORTA_SNAPSHOT_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "toit", "snapshots")
}

// RetainSnapshot reads the snapshot's program UUID (`toit tool snapshot uuid`)
// and copies the snapshot into jag's decode cache as <uuid>.snapshot, so a later
// `jag decode <blob>` for a panic from this image symbolicates locally. Returns
// the UUID. Callers treat failures as non-fatal (decode is best-effort).
func RetainSnapshot(ex *Executor, snapshotPath string) (string, error) {
	out, err := ex.Run("snapshot uuid", "toit", "tool", "snapshot", "uuid", snapshotPath)
	if err != nil {
		return "", err
	}
	uuid := strings.TrimSpace(string(out))
	if uuid == "" {
		return "", fmt.Errorf("empty snapshot uuid for %s", snapshotPath)
	}
	dir := snapshotCacheDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	data, err := os.ReadFile(snapshotPath)
	if err != nil {
		return "", err
	}
	dst := filepath.Join(dir, uuid+".snapshot")
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return "", err
	}
	return uuid, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/toolchain/ -run TestRetainSnapshot -v`
Expected: PASS (both subtests).

- [ ] **Step 5: Commit**

```bash
git add internal/toolchain/retain.go internal/toolchain/retain_test.go
git commit -m "feat(toolchain): RetainSnapshot into jag decode cache"
```

---

### Task 4: `Build` returns the snapshot path + cleanup

**Files:**
- Modify: `internal/toolchain/build.go`
- Modify: `internal/toolchain/build_test.go`
- Modify: `internal/portacli/run.go:48` (the `toolchain.Build` call) — make it compile only

- [ ] **Step 1: Update the test to the new signature (failing)**

Replace the body of `TestBuildCompilesAndRelocates` in `internal/toolchain/build_test.go` with:

```go
func TestBuildCompilesAndRelocates(t *testing.T) {
	fr := &fileWritingRunner{imgBytes: []byte("IMAGEBYTES")}
	ex := NewExecutor(fr, &bytes.Buffer{}, false)
	img, snap, cleanup, err := Build(ex, "/tmp/app.toit")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if string(img) != "IMAGEBYTES" {
		t.Errorf("got %q, want IMAGEBYTES", img)
	}
	if snap == "" {
		t.Error("expected a non-empty snapshot path")
	}
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

Run: `go test ./internal/toolchain/ -run TestBuildCompiles -v`
Expected: FAIL — `assignment mismatch: 4 variables but Build returns 2 values` (compile error).

- [ ] **Step 3: Update `Build`**

Replace `internal/toolchain/build.go` body of `Build` with:

```go
// Build compiles a Toit app to a snapshot, relocates it to a 32-bit binary
// container image, and returns the image bytes, the on-disk snapshot path, and a
// cleanup func the caller must invoke (it removes the temp dir holding the
// snapshot). The snapshot is retained until cleanup so the caller can pass it to
// RetainSnapshot for panic decoding. All current ESP32 chips are 32-bit, so the
// relocation is `-m32 --format=binary`; the image couples to the active SDK
// version, checked separately via CheckSDK.
func Build(ex *Executor, appPath string) (img []byte, snapshotPath string, cleanup func(), err error) {
	noop := func() {}
	dir, err := os.MkdirTemp("", "porta-build-")
	if err != nil {
		return nil, "", noop, err
	}
	cleanup = func() { os.RemoveAll(dir) }

	snap := filepath.Join(dir, "app.snapshot")
	bin := filepath.Join(dir, "app.bin")

	if _, err := ex.Run("compile "+filepath.Base(appPath), "toit",
		"compile", "--snapshot", "-o", snap, appPath); err != nil {
		cleanup()
		return nil, "", noop, err
	}
	if _, err := ex.Run("relocate (esp32, -m32)", "toit",
		"tool", "snapshot-to-image", "-m32", "--format=binary", "-o", bin, snap); err != nil {
		cleanup()
		return nil, "", noop, err
	}
	b, err := os.ReadFile(bin)
	if err != nil {
		cleanup()
		return nil, "", noop, err
	}
	return b, snap, cleanup, nil
}
```

- [ ] **Step 4: Update the `run.go` caller to compile (retention added in Task 5)**

In `internal/portacli/run.go`, replace:

```go
	img, err := toolchain.Build(ex, appPath)
	if err != nil {
		return err
	}
```

with:

```go
	img, _, cleanup, err := toolchain.Build(ex, appPath)
	if err != nil {
		return err
	}
	defer cleanup()
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/toolchain/ ./internal/portacli/ -run 'TestBuild|TestRunDeploy' -v`
Expected: PASS (build tests + existing runDeploy tests still green — retention not yet wired).

- [ ] **Step 6: Commit**

```bash
git add internal/toolchain/build.go internal/toolchain/build_test.go internal/portacli/run.go
git commit -m "refactor(toolchain): Build returns snapshot path + cleanup"
```

---

### Task 5: Retain the snapshot after a successful deploy

**Files:**
- Modify: `internal/portacli/run.go` (`runDeploy`)
- Modify: `internal/portacli/run_test.go` (stub `snapshot uuid`, set `$PORTA_SNAPSHOT_DIR`, new assertions)

- [ ] **Step 1: Update the stub + add failing tests**

In `internal/portacli/run_test.go`, replace the `stubRunner` type and its `Run` method with:

```go
// stubRunner: `toit version` → fixed SDK; `snapshot uuid` → fixed uuid (or empty
// when badUUID); compile/-o and snapshot-to-image/-o write canned files.
type stubRunner struct {
	sdk     string
	badUUID bool
}

func (s stubRunner) Run(name string, args ...string) ([]byte, error) {
	for i := 0; i < len(args); i++ {
		if args[i] == "version" {
			return []byte(s.sdk + "\n"), nil
		}
		if args[i] == "uuid" {
			if s.badUUID {
				return []byte("\n"), nil
			}
			return []byte("deadbeef-uuid\n"), nil
		}
	}
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-o" {
			if runArgsContain(args, "compile") {
				_ = os.WriteFile(args[i+1], []byte("SNAP"), 0o600)
			}
			if runArgsContain(args, "snapshot-to-image") {
				_ = os.WriteFile(args[i+1], []byte("IMG"), 0o600)
			}
		}
	}
	return nil, nil
}

func runArgsContain(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}
```

In `TestRunDeployHappyPath`, add this line immediately after the `c, st := newRunClientServer(t)` line:

```go
	t.Setenv("PORTA_SNAPSHOT_DIR", t.TempDir())
```

Then append two new tests to `internal/portacli/run_test.go`:

```go
func TestRunDeployRetainsSnapshot(t *testing.T) {
	c, st := newRunClientServer(t)
	cache := t.TempDir()
	t.Setenv("PORTA_SNAPSHOT_DIR", cache)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	st.UpdateNodeIdentity("aabbccddeeff", "esp32", "v2.0.0-alpha.192")
	ex := toolchain.NewExecutor(stubRunner{sdk: "v2.0.0-alpha.192"}, &bytes.Buffer{}, false)

	var buf bytes.Buffer
	if err := runDeploy(&buf, c, ex, "aabbccddeeff", "/tmp/app.toit",
		deployOpts{Name: "blink", Lifecycle: "run-loop", Triggers: []string{"boot"}, Runlevel: 3}, false); err != nil {
		t.Fatalf("runDeploy: %v", err)
	}
	if _, err := os.Stat(cache + "/deadbeef-uuid.snapshot"); err != nil {
		t.Errorf("expected retained snapshot in cache: %v", err)
	}
}

func TestRunDeployRetentionFailureIsNonFatal(t *testing.T) {
	c, st := newRunClientServer(t)
	t.Setenv("PORTA_SNAPSHOT_DIR", t.TempDir())
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	st.UpdateNodeIdentity("aabbccddeeff", "esp32", "v2.0.0-alpha.192")
	ex := toolchain.NewExecutor(stubRunner{sdk: "v2.0.0-alpha.192", badUUID: true}, &bytes.Buffer{}, false)

	var buf bytes.Buffer
	if err := runDeploy(&buf, c, ex, "aabbccddeeff", "/tmp/app.toit",
		deployOpts{Name: "blink", Lifecycle: "run-loop", Triggers: []string{"boot"}, Runlevel: 3}, false); err != nil {
		t.Fatalf("runDeploy should succeed despite retention failure: %v", err)
	}
	if !strings.Contains(buf.String(), "warning") {
		t.Errorf("expected a retention warning, got %q", buf.String())
	}
	// The run command must still have been queued.
	if cmd, err := st.NextUndelivered("aabbccddeeff"); err != nil || cmd == nil || cmd.Verb != "run" {
		t.Fatalf("expected queued run despite retention failure, got %+v (err %v)", cmd, err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/portacli/ -run TestRunDeploy -v`
Expected: FAIL — `TestRunDeployRetainsSnapshot` (no snapshot in cache) and `TestRunDeployRetentionFailureIsNonFatal` (no "warning" output), because `runDeploy` doesn't retain yet.

- [ ] **Step 3: Wire retention into `runDeploy`**

In `internal/portacli/run.go`, change the build call to keep the snapshot path:

```go
	img, snapPath, cleanup, err := toolchain.Build(ex, appPath)
	if err != nil {
		return err
	}
	defer cleanup()
```

Then, immediately after the existing success print line
`fmt.Fprintf(out, "%s: built %s (%d B), enqueued run (command #%d)\n", nodeID, opts.Name, size, cmdID)`,
add:

```go
	if _, rerr := toolchain.RetainSnapshot(ex, snapPath); rerr != nil {
		fmt.Fprintf(out, "warning: snapshot not retained for panic decode: %v\n", rerr)
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/portacli/ -run TestRunDeploy -v`
Expected: PASS (happy path, retains-snapshot, retention-failure-non-fatal).

- [ ] **Step 5: Commit**

```bash
git add internal/portacli/run.go internal/portacli/run_test.go
git commit -m "feat(portacli): porta run retains snapshot for panic decode"
```

---

### Task 6: Auto-decode panics in `porta monitor`

**Files:**
- Modify: `internal/portacli/monitor.go`
- Modify: `internal/portacli/monitor_test.go`

- [ ] **Step 1: Add a shared fakeDecoder + failing panic tests; update existing call sites**

In `internal/portacli/monitor_test.go`:

(a) Add `"errors"` to the import block.

(b) Add this shared fake (reused by `panic_test.go`):

```go
// fakeDecoder: ok=true returns a canned 2-line trace; ok=false fails (no snapshot).
type fakeDecoder struct{ ok bool }

func (d fakeDecoder) Decode(blob string) (string, error) {
	if !d.ok {
		return "", errors.New("no snapshot")
	}
	return "UNHANDLED EXCEPTION: OUT_OF_BOUNDS\n  at main.foo", nil
}
```

(c) In every existing `runMonitor(...)` call in this file, insert `nil` as the new decoder argument right after the reader `f` argument. For example:

```go
	if err := runMonitor(context.Background(), &out, f, nil, "dev", 200, "", false, now, 10*time.Millisecond); err != nil {
```

(d) Append two new tests:

```go
func TestRunMonitorDecodesPanic(t *testing.T) {
	f := &fakeReader{window: []apiclient.DataRow{
		{ID: 1, TS: 100, Kind: "panic", Text: "BLOB"},
	}}
	var out bytes.Buffer
	now := func() int64 { return 200 }
	if err := runMonitor(context.Background(), &out, f, fakeDecoder{ok: true}, "dev", 200, "", false, now, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "‼ PANIC") || !strings.Contains(s, "OUT_OF_BOUNDS") {
		t.Errorf("got %q", s)
	}
}

func TestRunMonitorPanicFallback(t *testing.T) {
	f := &fakeReader{window: []apiclient.DataRow{
		{ID: 1, TS: 100, Kind: "panic", Text: "BLOB"},
	}}
	var out bytes.Buffer
	now := func() int64 { return 200 }
	if err := runMonitor(context.Background(), &out, f, fakeDecoder{ok: false}, "dev", 200, "", false, now, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if s := out.String(); !strings.Contains(s, "jag decode BLOB") || !strings.Contains(s, "no local snapshot") {
		t.Errorf("got %q", s)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/portacli/ -run TestRunMonitor -v`
Expected: FAIL — too many arguments to `runMonitor` (compile error) until the signature is updated.

- [ ] **Step 3: Update `monitor.go`**

In `internal/portacli/monitor.go`:

(a) Change the `runMonitor` signature to add `dec panicDecoder` right after `c telemetryReader`:

```go
func runMonitor(ctx context.Context, out io.Writer, c telemetryReader, dec panicDecoder,
	sel string, sinceS int64, kind string, follow bool,
	now func() int64, pollInterval time.Duration,
) error {
```

(b) Replace the two `fmt.Fprintln(out, telemetry.FormatLine(toStoreRow(r)))` lines (in the window loop and the follow loop) with `printMonitorRow(out, r, dec)`.

(c) Add this helper at the end of the file:

```go
// printMonitorRow renders one telemetry row: kind:"panic" rows are decoded via
// renderPanic; everything else uses the unchanged telemetry.FormatLine output.
func printMonitorRow(out io.Writer, r apiclient.DataRow, dec panicDecoder) {
	if r.Kind == "panic" {
		renderPanic(out, r, dec)
		return
	}
	fmt.Fprintln(out, telemetry.FormatLine(toStoreRow(r)))
}
```

(d) In `newMonitorCmd`, add a `--no-decode` flag and build the decoder. Replace the `RunE`'s final two lines
```go
			c := apiclient.New(serverURL())
			return runMonitor(cmd.Context(), cmd.OutOrStdout(), c, device, sinceS, kind, follow, nowSec, 2*time.Second)
```
with:
```go
			c := apiclient.New(serverURL())
			var dec panicDecoder
			if !noDecode {
				dec = newJagDecoder()
			}
			return runMonitor(cmd.Context(), cmd.OutOrStdout(), c, dec, device, sinceS, kind, follow, nowSec, 2*time.Second)
```
and add `var noDecode bool` to the existing `var` block at the top of `newMonitorCmd`, plus this flag registration alongside the others:
```go
	cmd.Flags().BoolVar(&noDecode, "no-decode", false, "print raw panic blobs instead of running jag decode")
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/portacli/ -run TestRunMonitor -v`
Expected: PASS (all monitor tests, including the two new panic tests).

- [ ] **Step 5: Commit**

```bash
git add internal/portacli/monitor.go internal/portacli/monitor_test.go
git commit -m "feat(portacli): monitor auto-decodes kind:panic rows (--no-decode opt-out)"
```

---

### Task 7: `porta panic list`

**Files:**
- Create: `internal/portacli/panic.go`
- Create: `internal/portacli/panic_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/portacli/panic_test.go`:

```go
package portacli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/davidg238/porta/internal/apiclient"
)

func TestRunPanicListNewestFirst(t *testing.T) {
	// fakeReader returns rows ascending (oldest first); list must show newest first.
	f := &fakeReader{window: []apiclient.DataRow{
		{ID: 10, TS: 100, Kind: "panic", Text: "A"},
		{ID: 11, TS: 200, Kind: "panic", Text: "B"},
	}}
	var out bytes.Buffer
	if err := runPanicList(&out, f, fakeDecoder{ok: true}, "dev", 86400, 20, func() int64 { return 1000 }); err != nil {
		t.Fatal(err)
	}
	if f.windowKind != "panic" {
		t.Errorf("expected kind=panic filter, got %q", f.windowKind)
	}
	s := out.String()
	if !strings.Contains(s, "ID") || !strings.Contains(s, "11") || !strings.Contains(s, "OUT_OF_BOUNDS") {
		t.Errorf("got %q", s)
	}
	if i10, i11 := strings.Index(s, "\n10"), strings.Index(s, "\n11"); i11 < 0 || i10 < 0 || i11 > i10 {
		t.Errorf("expected newest (11) before oldest (10): %q", s)
	}
}

func TestRunPanicListFallbackSummary(t *testing.T) {
	f := &fakeReader{window: []apiclient.DataRow{
		{ID: 10, TS: 100, Kind: "panic", Text: "A"},
	}}
	var out bytes.Buffer
	if err := runPanicList(&out, f, fakeDecoder{ok: false}, "dev", 86400, 20, func() int64 { return 1000 }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no local snapshot") {
		t.Errorf("got %q", out.String())
	}
}

func TestRunPanicListEmpty(t *testing.T) {
	f := &fakeReader{}
	var out bytes.Buffer
	if err := runPanicList(&out, f, fakeDecoder{ok: true}, "dev", 86400, 20, func() int64 { return 1000 }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no panics") {
		t.Errorf("got %q", out.String())
	}
}

func TestRunPanicListLimitKeepsNewest(t *testing.T) {
	f := &fakeReader{window: []apiclient.DataRow{
		{ID: 1, TS: 100, Kind: "panic", Text: "A"},
		{ID: 2, TS: 200, Kind: "panic", Text: "B"},
		{ID: 3, TS: 300, Kind: "panic", Text: "C"},
	}}
	var out bytes.Buffer
	if err := runPanicList(&out, f, fakeDecoder{ok: true}, "dev", 86400, 2, func() int64 { return 1000 }); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if strings.Contains(s, "\n1\t") || !strings.Contains(s, "\n2") || !strings.Contains(s, "\n3") {
		t.Errorf("limit=2 should keep newest two (2,3) and drop 1: %q", s)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/portacli/ -run TestRunPanicList -v`
Expected: FAIL — `undefined: runPanicList`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/portacli/panic.go`:

```go
// internal/portacli/panic.go
package portacli

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/davidg238/porta/internal/apiclient"
	"github.com/davidg238/porta/internal/command"
	"github.com/spf13/cobra"
)

// newPanicCmd is the `porta panic` group: browse and decode node panics.
func newPanicCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "panic",
		Short: "Browse and decode node panics",
	}
	cmd.AddCommand(newPanicListCmd())
	return cmd
}

// tailReversed keeps the most-recent `limit` rows (input is ascending by time)
// and returns them newest-first. limit <= 0 keeps all rows.
func tailReversed(rows []apiclient.DataRow, limit int) []apiclient.DataRow {
	if limit > 0 && len(rows) > limit {
		rows = rows[len(rows)-limit:]
	}
	out := make([]apiclient.DataRow, len(rows))
	for i, r := range rows {
		out[len(rows)-1-i] = r
	}
	return out
}

// runPanicList is the testable core of `porta panic list`: a newest-first table
// of recent kind:"panic" rows with id, time, and a one-line decoded summary.
func runPanicList(out io.Writer, c telemetryReader, dec panicDecoder, sel string, sinceS int64, limit int, now func() int64) error {
	until := now()
	rows, err := c.QueryTelemetryWindow(sel, until-sinceS, until, "panic", 0)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Fprintln(out, "no panics in window")
		return nil
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tTIME\tSUMMARY")
	for _, r := range tailReversed(rows, limit) {
		fmt.Fprintf(tw, "%d\t%s\t%s\n", r.ID, panicTime(r.TS), panicSummary(r, dec))
	}
	return tw.Flush()
}

func newPanicListCmd() *cobra.Command {
	var device, since string
	var limit int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List recent panics for a node (newest first)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			sinceS := int64(86400)
			if since != "" {
				s, err := command.ParseDurationSeconds(since)
				if err != nil {
					return err
				}
				sinceS = s
			}
			c := apiclient.New(serverURL())
			return runPanicList(cmd.OutOrStdout(), c, newJagDecoder(), device, sinceS, limit, nowSec)
		},
	}
	deviceFlag(cmd, &device)
	cmd.Flags().StringVar(&since, "since", "", "look-back window, e.g. 6h, 24h (default 24h)")
	cmd.Flags().IntVar(&limit, "limit", 20, "max panics to show (most recent)")
	return cmd
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/portacli/ -run TestRunPanicList -v`
Expected: PASS (all four subtests).

- [ ] **Step 5: Commit**

```bash
git add internal/portacli/panic.go internal/portacli/panic_test.go
git commit -m "feat(portacli): porta panic list — browse recent panics"
```

---

### Task 8: `porta panic show` + register the command

**Files:**
- Modify: `internal/portacli/panic.go`
- Modify: `internal/portacli/panic_test.go`
- Modify: `internal/portacli/root.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/portacli/panic_test.go`:

```go
func TestRunPanicShowMostRecent(t *testing.T) {
	f := &fakeReader{window: []apiclient.DataRow{
		{ID: 10, TS: 100, Kind: "panic", Text: "A"},
		{ID: 11, TS: 200, Kind: "panic", Text: "B"},
	}}
	var out bytes.Buffer
	if err := runPanicShow(&out, f, fakeDecoder{ok: true}, "dev", 86400, 0, func() int64 { return 1000 }); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "‼ PANIC") || !strings.Contains(s, "OUT_OF_BOUNDS") {
		t.Errorf("got %q", s)
	}
}

func TestRunPanicShowByID(t *testing.T) {
	f := &fakeReader{window: []apiclient.DataRow{
		{ID: 10, TS: 100, Kind: "panic", Text: "A"},
		{ID: 11, TS: 200, Kind: "panic", Text: "B"},
	}}
	var out bytes.Buffer
	if err := runPanicShow(&out, f, fakeDecoder{ok: true}, "dev", 86400, 10, func() int64 { return 1000 }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "‼ PANIC") {
		t.Errorf("got %q", out.String())
	}
}

func TestRunPanicShowUnknownID(t *testing.T) {
	f := &fakeReader{window: []apiclient.DataRow{
		{ID: 10, TS: 100, Kind: "panic", Text: "A"},
	}}
	var out bytes.Buffer
	if err := runPanicShow(&out, f, fakeDecoder{ok: true}, "dev", 86400, 999, func() int64 { return 1000 }); err == nil {
		t.Fatal("expected error for unknown id")
	}
}

func TestRunPanicShowEmpty(t *testing.T) {
	f := &fakeReader{}
	var out bytes.Buffer
	if err := runPanicShow(&out, f, fakeDecoder{ok: true}, "dev", 86400, 0, func() int64 { return 1000 }); err == nil {
		t.Fatal("expected error when there are no panics")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/portacli/ -run TestRunPanicShow -v`
Expected: FAIL — `undefined: runPanicShow`.

- [ ] **Step 3: Write minimal implementation**

In `internal/portacli/panic.go`, add `runPanicShow` and `newPanicShowCmd`, and register the show subcommand in `newPanicCmd`.

Change `newPanicCmd`'s `AddCommand` line to:

```go
	cmd.AddCommand(newPanicListCmd(), newPanicShowCmd())
```

Add at the end of the file:

```go
// runPanicShow is the testable core of `porta panic show`: decode and print one
// kind:"panic" row. id>0 selects by data_log id; id==0 selects the most recent.
func runPanicShow(out io.Writer, c telemetryReader, dec panicDecoder, sel string, sinceS int64, id int64, now func() int64) error {
	until := now()
	rows, err := c.QueryTelemetryWindow(sel, until-sinceS, until, "panic", 0)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return fmt.Errorf("no panics in the last window for %s", sel)
	}
	var row apiclient.DataRow
	if id > 0 {
		found := false
		for _, r := range rows {
			if r.ID == id {
				row, found = r, true
				break
			}
		}
		if !found {
			return fmt.Errorf("no panic with id %d in window (try `porta panic list`)", id)
		}
	} else {
		row = rows[len(rows)-1] // rows are ascending; last is most recent
	}
	renderPanic(out, row, dec)
	return nil
}

func newPanicShowCmd() *cobra.Command {
	var device, since string
	var id int64
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Decode and print one panic (default: most recent)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			sinceS := int64(86400)
			if since != "" {
				s, err := command.ParseDurationSeconds(since)
				if err != nil {
					return err
				}
				sinceS = s
			}
			c := apiclient.New(serverURL())
			return runPanicShow(cmd.OutOrStdout(), c, newJagDecoder(), device, sinceS, id, nowSec)
		},
	}
	deviceFlag(cmd, &device)
	cmd.Flags().StringVar(&since, "since", "", "look-back window, e.g. 6h, 24h (default 24h)")
	cmd.Flags().Int64Var(&id, "id", 0, "data_log id from `porta panic list` (default: most recent)")
	return cmd
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/portacli/ -run TestRunPanicShow -v`
Expected: PASS (all four subtests).

- [ ] **Step 5: Register the command**

In `internal/portacli/root.go`, add `newPanicCmd(),` to the `root.AddCommand(...)` list (e.g. after `newMonitorCmd(),`):

```go
	root.AddCommand(
		newServeCmd(),
		newScanCmd(),
		newPingCmd(),
		newDeviceCmd(),
		newContainerCmd(),
		newLogCmd(),
		newMonitorCmd(),
		newPanicCmd(),
		newRunCmd(),
	)
```

- [ ] **Step 6: Full build + test + smoke**

Run: `go build ./... && go test ./...`
Expected: build clean, all packages PASS.

Run: `go run ./cmd/porta panic --help` and `go run ./cmd/porta panic list --help`
Expected: the `panic` group shows `list` and `show`; `list --help` shows `--device`, `--since`, `--limit`.

- [ ] **Step 7: Commit**

```bash
git add internal/portacli/panic.go internal/portacli/panic_test.go internal/portacli/root.go
git commit -m "feat(portacli): porta panic show + register panic command"
```

---

## Self-review

**Spec coverage:**
- §1 wire contract (`kind:"panic"`) → consumed in Tasks 6–8 (no porta change needed; free-form kind). Contract docs already committed on-branch.
- §5 snapshot retention → Tasks 3–5 (`RetainSnapshot`, `Build` returns snapshot, `runDeploy` retains deployed-only, best-effort warning).
- §6 monitor auto-decode (decoder seam, `‼ PANIC` render, raw-blob fallback, `--no-decode` default-on) → Tasks 1, 2, 6.
- §6b `panic list`/`panic show` (data_log.id selector, newest-first, summary, fallback) → Tasks 7, 8.
- §7 error handling (decode failure → raw+hint, retention failure non-fatal, unknown id error) → Tasks 2, 5, 6, 8.
- §8 testing (RetainSnapshot, runDeploy retention, monitor decode+fallback, panic list/show) → covered. Hardware e2e + nodus capture are out of this (porta) plan by design (§9).

**Placeholder scan:** none — every code step shows full code; import edits are called out in prose (Task 2 Step 1, Task 6 Step 1).

**Type consistency:** `panicDecoder.Decode(blob string) (string, error)` is used identically in `jagDecoder`, `fakeDecoder`, `localDecoder`, `renderPanic`, `panicSummary`, `runMonitor`, `runPanicList`, `runPanicShow`. `Build(ex, appPath) (img []byte, snapshotPath string, cleanup func(), err error)` matches its callers in `run.go` and `build_test.go`. `RetainSnapshot(ex *Executor, snapshotPath string) (string, error)` matches `runDeploy`. `telemetryReader` (existing, with `QueryTelemetryWindow`/`QueryTelemetryAfter`) is reused unchanged by the panic commands. `runMonitor`'s new `dec panicDecoder` param is threaded through every existing call site (updated to pass `nil`) and the two new tests.
