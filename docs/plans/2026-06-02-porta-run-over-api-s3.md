# `porta run` over the API (S3) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Re-point `porta run` to flow entirely through the control-plane HTTP API — compile/relocate stay client-side, the SDK guard reads the node's reported SDK via the S1 API, and the built `.bin` + `run` command are delivered via the S1 multipart install — so `porta run` no longer opens the local store.

**Architecture:** Add one narrow read (`NodeIdentity`) to the otherwise write-only `internal/apiclient`; re-point `runDeploy` from `*store.Store`/`control.Install` to `*apiclient.Client` (raw selector resolved server-side); re-point `newRunCmd` to build a client from `serverURL()` and drop `openStore`/`resolveNodeID`. Rewrite the test to drive the real `apisrv.Handler` over `httptest` (mirrors the S2 `mutate_test` harness). No protocol, schema, or server change.

**Tech Stack:** Go, cobra, `net/http` + `net/http/httptest`, sqlite (`mattn/go-sqlite3`), the Toit SDK CLI (`toit`) behind the existing injectable `toolchain.Runner`.

**Spec:** `docs/specs/2026-06-02-porta-run-over-api-s3-design.md`.

**Verified signatures this plan builds on (do not change them):**
- `apiclient.New(baseURL string) *Client`; `Client.do(req *http.Request) (json.RawMessage, error)` (private; transport→"is porta serve running?" wrap, non-2xx/ok=false→server error string).
- `Client.Install(sel, name string, image io.Reader, opts InstallOpts) (int64, string, int64, error)` → `(cmdID, nodeID, size, err)`; `InstallOpts{Lifecycle string; Runlevel int; IntervalS int64; Triggers []string}`.
- `Client.Command(sel, verb string, args any) (int64, string, error)` → `(cmdID, nodeID, err)`.
- `apisrv.New(st *store.Store) *Handler`; `(*Handler).Register(mux *http.ServeMux)`; `GET /api/nodes/{sel}` returns a JSON detail object whose data includes `"chip"` and `"sdk"` string fields.
- `toolchain.NewExecutor(r Runner, out io.Writer, verbose bool) *Executor`; `toolchain.SDKVersion(ex) (string, error)`; `toolchain.CheckSDK(nodeSDK, activeSDK string) error`; `toolchain.Build(ex, appPath) ([]byte, error)`; `toolchain.ExecRunner{}`.
- `serverURL() string` (in `internal/portacli/client.go`); `deviceFlag(cmd, *string)`, `promptChoice`, `promptTriggers` (in `run.go`/`inspect.go`).
- `store.Store`: `TouchNode(id, addr string, now int64) error`, `UpdateNodeIdentity(id, chip, sdk string) error`, `NextUndelivered(id string) (*command.Command, error)`, `RecentCommandsForDevice(id string, n int) ([]..., error)`.

---

### Task 1: Add `NodeIdentity` read to `apiclient`

**Files:**
- Modify: `internal/apiclient/client.go` (add method + a private response struct near the other `*Resp` structs; update the package doc comment ~lines 1-5)
- Test: `internal/apiclient/client_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/apiclient/client_test.go` (the file already has `httptest`-stub helpers; this test is self-contained so it needs no shared helper):

```go
func TestNodeIdentity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/nodes/aabbccddeeff" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true,"data":{"id":"aabbccddeeff","chip":"esp32","sdk":"v2.0.0-alpha.192"}}`)
	}))
	defer srv.Close()

	chip, sdk, err := New(srv.URL).NodeIdentity("aabbccddeeff")
	if err != nil {
		t.Fatalf("NodeIdentity: %v", err)
	}
	if chip != "esp32" || sdk != "v2.0.0-alpha.192" {
		t.Errorf("got chip=%q sdk=%q, want esp32 / v2.0.0-alpha.192", chip, sdk)
	}
}

func TestNodeIdentityServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"ok":false,"error":"node not found"}`)
	}))
	defer srv.Close()

	_, _, err := New(srv.URL).NodeIdentity("nope")
	if err == nil || !strings.Contains(err.Error(), "node not found") {
		t.Fatalf("expected server error string, got %v", err)
	}
}
```

If `io`/`strings`/`http`/`httptest` aren't already imported in the test file, add them.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/apiclient/ -run TestNodeIdentity`
Expected: build failure — `NodeIdentity` undefined.

- [ ] **Step 3: Implement `NodeIdentity`**

In `internal/apiclient/client.go`, update the package doc comment's "write-side" framing to note the one read. Change the opening sentence of the package comment to:

```go
// Package apiclient is the HTTP client for the porta control-plane API
// (internal/apisrv). It is cobra-free and store-free: the CLI's mutating
// commands use it to POST/PATCH the server instead of opening the store, which
// keeps the server the single writer (one trustworthy audit trail). It also
// carries one narrow read — NodeIdentity — for `porta run`'s SDK guard.
```

Then add (near `patchResp`, after `PatchNode`):

```go
// identityResp decodes just the chip/sdk fields of a GET /api/nodes/{sel} detail.
type identityResp struct {
	Chip string `json:"chip"`
	Sdk  string `json:"sdk"`
}

// NodeIdentity fetches the node's reported chip/sdk (GET /api/nodes/{sel}), for
// `porta run`'s SDK guard. The full node-detail read stays deferred (S2). A node
// that exists but hasn't reported yet returns ("", "", nil); an unknown node
// surfaces the server's 404 error string. Other detail fields are ignored.
func (c *Client) NodeIdentity(sel string) (chip, sdk string, err error) {
	req, err := http.NewRequest("GET", c.baseURL+"/api/nodes/"+url.PathEscape(sel), nil)
	if err != nil {
		return "", "", err
	}
	data, err := c.do(req)
	if err != nil {
		return "", "", err
	}
	var r identityResp
	if err := json.Unmarshal(data, &r); err != nil {
		return "", "", err
	}
	return r.Chip, r.Sdk, nil
}
```

(`http`, `url`, `json` are already imported in `client.go`.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/apiclient/`
Expected: PASS (whole package).

- [ ] **Step 5: Commit**

```bash
git add internal/apiclient/client.go internal/apiclient/client_test.go
git commit -m "feat(porta): apiclient NodeIdentity read for the run SDK guard"
```

---

### Task 2: Re-point `porta run` (core + command) to the API client

**Files:**
- Modify: `internal/portacli/run.go` (`runDeploy` signature + body, `newRunCmd` RunE, imports)
- Test: `internal/portacli/run_test.go` (full rewrite)

`runDeploy` and `newRunCmd` are one logical change to a single file (the test can't compile against the old signature, and `newRunCmd` calls `runDeploy`), so they land together — the package is green at this task's end.

- [ ] **Step 1: Rewrite the test against the API-client core**

Replace the entire contents of `internal/portacli/run_test.go` with:

```go
package portacli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/davidg238/porta/internal/apiclient"
	"github.com/davidg238/porta/internal/apisrv"
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

// newRunClientServer stands up the REAL apisrv.Handler over a temp store behind
// an httptest server and returns a client pointed at it plus the store (so tests
// can seed identity and assert what landed). Mirrors mutate_test's harness.
func newRunClientServer(t *testing.T) (*apiclient.Client, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/d.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	mux := http.NewServeMux()
	apisrv.New(st).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return apiclient.New(srv.URL), st
}

func TestRunDeployHappyPath(t *testing.T) {
	c, st := newRunClientServer(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	st.UpdateNodeIdentity("aabbccddeeff", "esp32", "v2.0.0-alpha.192")
	ex := toolchain.NewExecutor(stubRunner{sdk: "v2.0.0-alpha.192"}, &bytes.Buffer{}, false)

	var buf bytes.Buffer
	err := runDeploy(&buf, c, ex, "aabbccddeeff", "/tmp/app.toit",
		deployOpts{Name: "blink", Lifecycle: "run-loop", Triggers: []string{"boot"}, Runlevel: 3}, false)
	if err != nil {
		t.Fatalf("runDeploy: %v", err)
	}
	// A run command must now be queued, stamped issued_by="api".
	cmd, err := st.NextUndelivered("aabbccddeeff")
	if err != nil || cmd == nil || cmd.Verb != "run" {
		t.Fatalf("expected queued run, got %+v (err %v)", cmd, err)
	}
	rows, _ := st.RecentCommandsForDevice("aabbccddeeff", 10)
	if len(rows) == 0 || rows[0].IssuedBy != "api" {
		t.Errorf("expected issued_by=api, got %+v", rows)
	}
	// Confirmation leads with the resolved node id.
	if got := buf.String(); !strings.Contains(got, "aabbccddeeff: built blink") || !strings.Contains(got, "enqueued run") {
		t.Errorf("confirmation = %q", got)
	}
}

func TestRunDeployBlocksOnUnknownIdentity(t *testing.T) {
	c, st := newRunClientServer(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000) // no UpdateNodeIdentity → sdk=""
	ex := toolchain.NewExecutor(stubRunner{sdk: "v2.0.0-alpha.192"}, &bytes.Buffer{}, false)
	// Blocked even with force=true (force overrides mismatch, not unknown identity).
	if err := runDeploy(&bytes.Buffer{}, c, ex, "aabbccddeeff", "/tmp/app.toit",
		deployOpts{Name: "blink", Lifecycle: "run-once"}, true); err == nil {
		t.Fatal("expected block on unknown identity even with force")
	}
}

func TestRunDeployRefusesSDKMismatch(t *testing.T) {
	c, st := newRunClientServer(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	st.UpdateNodeIdentity("aabbccddeeff", "esp32", "v2.0.0-alpha.192")
	ex := toolchain.NewExecutor(stubRunner{sdk: "v9.9.9"}, &bytes.Buffer{}, false)
	if err := runDeploy(&bytes.Buffer{}, c, ex, "aabbccddeeff", "/tmp/app.toit",
		deployOpts{Name: "blink", Lifecycle: "run-once"}, false); err == nil {
		t.Fatal("expected SDK mismatch refusal")
	}
	// --force overrides the mismatch and the run lands.
	if err := runDeploy(&bytes.Buffer{}, c, ex, "aabbccddeeff", "/tmp/app.toit",
		deployOpts{Name: "blink", Lifecycle: "run-once"}, true); err != nil {
		t.Errorf("--force should override: %v", err)
	}
	cmd, _ := st.NextUndelivered("aabbccddeeff")
	if cmd == nil || cmd.Verb != "run" {
		t.Fatalf("expected queued run after --force, got %+v", cmd)
	}
}

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

> **Note on `RecentCommandsForDevice` row field:** the command-log row type exposes the issuer as `IssuedBy` (used by `apisrv/reads.go`). If the store's row type names it differently, adjust the assertion to match — the field carries the `issued_by` value either way.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/portacli/ -run TestRunDeploy`
Expected: build failure — `runDeploy` still has the old `(out, st, ex, id, appPath, opts, force, now)` signature; `c` (an `*apiclient.Client`) doesn't match `*store.Store`.

- [ ] **Step 3: Re-point `runDeploy`**

In `internal/portacli/run.go`, replace the `runDeploy` function (lines ~27-64) with:

```go
// runDeploy is the testable core of `porta run`: SDK guard (read the node's
// reported sdk via the API, compare against the local toolchain), local build,
// then deliver the image + enqueue run via the control-plane API. force skips
// the SDK match refusal (but not the unknown-identity block). The server stamps
// issued_by="api".
func runDeploy(out io.Writer, c *apiclient.Client, ex *toolchain.Executor, sel, appPath string, opts deployOpts, force bool) error {
	_, sdk, err := c.NodeIdentity(sel)
	if err != nil {
		return err
	}
	if sdk == "" {
		return fmt.Errorf("node %s hasn't reported its firmware identity yet — wait for a check-in (or flash it via `porta flash`) before deploying", sel)
	}
	active, err := toolchain.SDKVersion(ex)
	if err != nil {
		return err
	}
	if !force {
		if err := toolchain.CheckSDK(sdk, active); err != nil {
			return err
		}
	}
	img, err := toolchain.Build(ex, appPath)
	if err != nil {
		return err
	}
	cmdID, nodeID, size, err := c.Install(sel, opts.Name, bytes.NewReader(img), apiclient.InstallOpts{
		Lifecycle: opts.Lifecycle, Runlevel: opts.Runlevel, Triggers: opts.Triggers,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: built %s (%d B), enqueued run (command #%d)\n", nodeID, opts.Name, size, cmdID)
	if opts.PowerMode != "" {
		if _, _, err := c.Command(sel, "set-power-mode", map[string]any{"mode": opts.PowerMode}); err != nil {
			return err
		}
	}
	return nil
}
```

Update the `run.go` import block: remove `"github.com/davidg238/porta/internal/control"` and `"github.com/davidg238/porta/internal/store"`; add `"github.com/davidg238/porta/internal/apiclient"`. Keep `bytes`, `fmt`, `io`, `toolchain`. (`bufio`, `os`, `path/filepath`, `strings`, `cobra` remain — they're used by `newRunCmd`/prompts, re-pointed in Task 3.) The exact post-Task-2 import block:

```go
import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/davidg238/porta/internal/apiclient"
	"github.com/davidg238/porta/internal/toolchain"
	"github.com/spf13/cobra"
)
```

> After this step the package still won't compile because `newRunCmd` calls the old `openStore`/`resolveNodeID`/`runDeploy(...)` shape — fixed in Step 4 below, before the green run.

- [ ] **Step 4: Re-point `newRunCmd`**

In `internal/portacli/run.go`, replace the `RunE` body of `newRunCmd` (the `openStore`/`resolveNodeID` block, ~lines 74-100) with the client-backed version. The full `RunE`:

```go
		RunE: func(cmd *cobra.Command, args []string) error {
			appPath := args[0]
			if !strings.HasSuffix(appPath, ".toit") {
				return fmt.Errorf("expected a .toit source file, got %q", appPath)
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
			c := apiclient.New(serverURL())
			ex := toolchain.NewExecutor(toolchain.ExecRunner{}, cmd.OutOrStdout(), verbose)
			return runDeploy(cmd.OutOrStdout(), c, ex, device, appPath, opts, force)
		},
```

Note: `device` is passed raw (server resolves the selector); `nowSec()` is gone (server stamps the time). The flag registration block below `RunE` is unchanged.

- [ ] **Step 5: Run the package tests to verify they pass**

Run: `go test ./internal/portacli/`
Expected: PASS — `runDeploy` happy-path/unknown-identity/mismatch tests go CLI-core → HTTP → apisrv → store; `TestNewRunCmdRegistersFlags` passes.

- [ ] **Step 6: Build + vet the whole tree**

Run: `go build ./... && go vet ./...`
Expected: clean (no unused `store`/`control`/`nowSec` references remain in `run.go`).

- [ ] **Step 7: Commit**

```bash
git add internal/portacli/run.go internal/portacli/run_test.go
git commit -m "feat(porta): porta run over the API (no local store; serve required)"
```

---

### Task 3: Whole-suite verification

**Files:** none (verification only).

- [ ] **Step 1: Full suite**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all green.

- [ ] **Step 2: Confirm `porta run` no longer imports store/control**

Run: `grep -n "internal/store\|internal/control" internal/portacli/run.go`
Expected: no matches.

- [ ] **Step 3: Manual smoke (optional — needs `toit` on PATH + a node that reported identity)**

In one shell: `porta serve` (starts UDP + HTTP on :6970). In another:
`porta run examples/blink.toit -d <node> -v` → observe the narrated compile/relocate steps, a confirmation line leading with the resolved node id, and a queued run via `porta log -d <node>` showing `issued_by=api`. With the server down, the same command should print the "is `porta serve` running?" hint.

---

## Notes for the implementer

- **No server/protocol/schema change.** S1 already exposes `chip`/`sdk` on `GET /api/nodes/{sel}` and the multipart install; S3 is purely client-side re-pointing plus the one `apiclient` read.
- **Unknown identity vs `--force`:** the `sdk == ""` block is intentionally checked before (and independent of) `force` — `--force` overrides only an SDK *mismatch*. This is Phase-1 parity and is asserted in `TestRunDeployBlocksOnUnknownIdentity`.
- **Selector is raw.** The CLI no longer resolves `-d` locally; the server resolves it and echoes the 12-hex `node_id` back (used in the confirmation line), exactly like the S2 writes.
- **`EnsureNode` on write.** Installing to an unseen-but-well-formed MAC creates the node server-side (S2 backport); the SDK guard's `NodeIdentity` read runs first, so in practice the node already exists (it reported identity). The test seeds via `TouchNode`+`UpdateNodeIdentity`.
- **Task 2 is one logical change to `run.go`** (core + command wiring + test rewrite) committed once. The package is non-compiling only between Step 3 and Step 4 within the task; it is green at the task boundary.
```
