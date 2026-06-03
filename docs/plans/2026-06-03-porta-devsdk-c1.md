# porta devsdk carve (C1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Carve a public `devsdk/` out of porta — promote `apiclient` and the narrating command runner, add the `provision` contract, and document the surface — with zero behavior change, so the nodus repo can import it in C2/C3.

**Architecture:** Pure refactor + one new package. `internal/apiclient` → `devsdk/apiclient` (package name unchanged); `internal/toolchain/exec.go` → `devsdk/exec` (Runner + ExecRunner + Executor as one cohesive package); new `devsdk/provision` defines the stable `firmware.config["porta"]` shape. The Toit-specific `internal/toolchain/{build,sdk,retain}.go` and `porta run` stay (they re-point to `devsdk/exec`); they move to nodus in C2. `devsdk/flash` and `devsdk/opverbs` are deferred (see spec §3.1).

**Tech Stack:** Go 1.26, module `github.com/davidg238/porta`, cobra CLI, stdlib `testing`.

**Spec:** `docs/specs/2026-06-03-porta-devsdk-nodus-flash-design.md` (§3.1, §4 C1).

---

## File Structure

- `devsdk/apiclient/` — moved verbatim from `internal/apiclient/` (package `apiclient`).
- `devsdk/exec/exec.go` + `exec_test.go` — moved from `internal/toolchain/exec.go` + `exec_test.go`, repackaged `exec`.
- `devsdk/provision/provision.go` + `provision_test.go` — NEW: `Gateway` + `firmware.config["porta"]` render/parse.
- `docs/DEVSDK.md` — NEW: northbound contract (API envelope, devsdk surface, the `porta` config shape, dependency direction).
- Re-pointed importers (import paths only, no logic change):
  - apiclient consumers: `internal/portacli/{run,decode,panic,monitor,mutate}.go` (+ their `_test.go`).
  - exec consumers: `internal/toolchain/{build,sdk,retain}.go`, `internal/portacli/{run,decode}.go` (+ `_test.go`).

---

## Task 1: Move `internal/apiclient` → `devsdk/apiclient`

**Files:**
- Move: `internal/apiclient/{client.go,client_test.go,telemetry_test.go}` → `devsdk/apiclient/`
- Modify (import path only): every file importing `github.com/davidg238/porta/internal/apiclient`

- [ ] **Step 1: Move the package directory with git (preserves history)**

```bash
cd ~/workspaceToit/porta
mkdir -p devsdk
git mv internal/apiclient devsdk/apiclient
```

- [ ] **Step 2: Re-point all importers (source + tests)**

Find them, then rewrite the import path. The package name (`apiclient`) is unchanged, so only the path string changes:

```bash
grep -rl "github.com/davidg238/porta/internal/apiclient" --include="*.go" . \
  | xargs sed -i 's#github.com/davidg238/porta/internal/apiclient#github.com/davidg238/porta/devsdk/apiclient#g'
```

Expected importers (verify the grep matched these): `internal/portacli/{run,decode,panic,monitor,mutate}.go` and `internal/portacli/{run_test,decode_test,panic_test,monitor_test,mutate_test}.go`.

- [ ] **Step 3: Verify build + the moved package's own tests pass**

Run: `go build ./... && go test ./devsdk/apiclient/... ./internal/portacli/...`
Expected: PASS (no behavior changed; the package just lives at a new path).

- [ ] **Step 4: Confirm nothing still references the old path**

Run: `grep -rn "internal/apiclient" --include="*.go" . ; echo "exit:$?"`
Expected: no matches (grep exit 1 / no lines).

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor(devsdk): promote internal/apiclient -> devsdk/apiclient

Public HTTP control-plane client so node-repo dev tools (C2/C3) can import
it. Package name unchanged; only the import path moves. Zero behavior change.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: Move `internal/toolchain/exec.go` → `devsdk/exec`

The Runner + ExecRunner + Executor become one cohesive package `exec`. `internal/toolchain/{build,sdk,retain}.go` keep their Toit-specific logic but now reference `exec.Executor`/`exec.Runner`. The stdlib `os/exec` import inside the new package is aliased `osexec` to avoid shadowing confusion.

**Files:**
- Move: `internal/toolchain/exec.go` → `devsdk/exec/exec.go`; `internal/toolchain/exec_test.go` → `devsdk/exec/exec_test.go`
- Modify: `internal/toolchain/{build,sdk,retain}.go`, `internal/portacli/{run,decode}.go` (+ `_test.go`)

- [ ] **Step 1: Move the two files with git**

```bash
cd ~/workspaceToit/porta
mkdir -p devsdk/exec
git mv internal/toolchain/exec.go devsdk/exec/exec.go
git mv internal/toolchain/exec_test.go devsdk/exec/exec_test.go
```

- [ ] **Step 2: Repackage the moved files and alias the stdlib import**

In `devsdk/exec/exec.go`: change the package clause and doc comment, and alias `os/exec`:

```go
// Package exec is porta's injectable, narrating runner for external dev tools
// ("trainer wheels"): Runner abstracts the shell-out (tests inject a fake);
// Executor narrates each command (apt-style summary, or full transcript when
// verbose). Promoted from internal/toolchain so node-repo dev tools can reuse it.
package exec

import (
	"fmt"
	"io"
	osexec "os/exec"
	"strings"
	"time"
)
```

And the one call site inside the file:

```go
func (ExecRunner) Run(name string, args ...string) ([]byte, error) {
	return osexec.Command(name, args...).CombinedOutput()
}
```

In `devsdk/exec/exec_test.go`: change its package clause to `package exec` (it was `package toolchain`).

- [ ] **Step 3: Re-point `internal/toolchain/{build,sdk,retain}.go` to `exec.Executor`**

These three files are `package toolchain` and used the same-package `Executor`/`Runner`. Add the import and qualify the type. For each file add to its import block:

```go
	"github.com/davidg238/porta/devsdk/exec"
```

Then qualify the receiver type in each signature:
- `internal/toolchain/build.go:15` — `func Build(ex *exec.Executor, appPath string)` (was `*Executor`)
- `internal/toolchain/sdk.go:9` — `func SDKVersion(ex *exec.Executor)` (was `*Executor`)
- `internal/toolchain/retain.go:25` — `func RetainSnapshot(ex *exec.Executor, snapshotPath string)` (was `*Executor`)

(There are no other `Executor`/`Runner` references in these three files — confirmed by grep.)

- [ ] **Step 4: Re-point the toolchain tests**

`internal/toolchain/{build_test,sdk_test,retain_test}.go` construct an Executor (they call the old same-package `NewExecutor`/`ExecRunner`). Re-point them:

```bash
sed -i -E 's/\bNewExecutor\(/exec.NewExecutor(/g; s/\bExecRunner\{/exec.ExecRunner{/g; s/\*Executor\b/*exec.Executor/g' \
  internal/toolchain/build_test.go internal/toolchain/sdk_test.go internal/toolchain/retain_test.go
```

Then add `"github.com/davidg238/porta/devsdk/exec"` to each of those test files' import blocks. (If a test defines a fake Runner, its `toolchain.Runner`/bare `Runner` references also become `exec.Runner` — the same sed covers bare `Runner` only if present; verify by building in Step 6 and fix any leftover.)

- [ ] **Step 5: Re-point the portacli consumers**

`internal/portacli/run.go` uses `toolchain.NewExecutor`, `toolchain.ExecRunner`, `toolchain.Executor` (and keeps `toolchain.SDKVersion/CheckSDK/Build/RetainSnapshot`). `internal/portacli/decode.go` uses only `toolchain.Runner` + `toolchain.ExecRunner` (it can drop the `internal/toolchain` import entirely).

In `internal/portacli/run.go`:
- line 31: `ex *toolchain.Executor` → `ex *exec.Executor`
- line 96: `toolchain.NewExecutor(toolchain.ExecRunner{}, ...)` → `exec.NewExecutor(exec.ExecRunner{}, ...)`
- add import `"github.com/davidg238/porta/devsdk/exec"` (keep the existing `internal/toolchain` import — `Build`/`SDKVersion`/`CheckSDK`/`RetainSnapshot` still live there).

In `internal/portacli/decode.go`:
- line 24: `r toolchain.Runner` → `r exec.Runner`
- line 27: `toolchain.ExecRunner{}` → `exec.ExecRunner{}`
- replace the `internal/toolchain` import with `"github.com/davidg238/porta/devsdk/exec"`.

Apply the same `toolchain.Executor`/`toolchain.Runner`/`toolchain.NewExecutor`/`toolchain.ExecRunner` → `exec.*` rename in `internal/portacli/{run_test,decode_test}.go` and add the `devsdk/exec` import where needed.

- [ ] **Step 6: Build, vet, and run the affected tests**

Run: `go build ./... && go vet ./... && go test ./devsdk/exec/... ./internal/toolchain/... ./internal/portacli/...`
Expected: PASS. If the compiler reports an unqualified `Runner`/`Executor`/`NewExecutor` the sed missed, qualify it with `exec.` and re-run.

- [ ] **Step 7: Confirm exec no longer lives in toolchain**

Run: `test ! -f internal/toolchain/exec.go && grep -rn "func NewExecutor\|type Executor\|type Runner\b" internal/toolchain/ ; echo "exit:$?"`
Expected: no matches in `internal/toolchain/` (the runner now lives only in `devsdk/exec`).

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "refactor(devsdk): promote toolchain runner -> devsdk/exec

Runner + ExecRunner + Executor become one cohesive devsdk/exec package so
node-repo dev tools (C2) can build on the narrating runner. Toit-specific
build/sdk/retain stay in internal/toolchain and re-point to devsdk/exec.
os/exec aliased osexec. Zero behavior change.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: New `devsdk/provision` — the `firmware.config["porta"]` contract

The stable, neutral provisioning contract: a node's `firmware.config["porta"]` object is `{"host": <string>, "port": <int>}`. `nodus flash` (C3) injects it; the nodus supervisor (C3 companion) reads it. WiFi is intentionally out of scope here (it rides jag's `--wifi-*` flags on the nodus side). The *injection mechanism* is a C3 spike; the *shape* is fixed here.

**Files:**
- Create: `devsdk/provision/provision.go`
- Test: `devsdk/provision/provision_test.go`

- [ ] **Step 1: Write the failing tests**

Create `devsdk/provision/provision_test.go`:

```go
package provision

import (
	"reflect"
	"testing"
)

func TestGatewayPortaConfig(t *testing.T) {
	g := Gateway{Host: "192.168.0.175", Port: 6969}
	got := g.PortaConfig()
	want := map[string]any{"host": "192.168.0.175", "port": 6969}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PortaConfig() = %#v, want %#v", got, want)
	}
}

func TestParseGatewayHostPort(t *testing.T) {
	g, err := ParseGateway("192.168.0.175:6969", 6969)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if g.Host != "192.168.0.175" || g.Port != 6969 {
		t.Fatalf("got %+v", g)
	}
}

func TestParseGatewayHostOnlyUsesDefaultPort(t *testing.T) {
	g, err := ParseGateway("gw.local", 6969)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if g.Host != "gw.local" || g.Port != 6969 {
		t.Fatalf("got %+v", g)
	}
}

func TestParseGatewayRejectsEmptyAndBadPort(t *testing.T) {
	if _, err := ParseGateway("", 6969); err == nil {
		t.Fatal("expected error for empty input")
	}
	if _, err := ParseGateway("gw:notaport", 6969); err == nil {
		t.Fatal("expected error for non-numeric port")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./devsdk/provision/...`
Expected: FAIL (build error — `Gateway`, `PortaConfig`, `ParseGateway` undefined).

- [ ] **Step 3: Write the minimal implementation**

Create `devsdk/provision/provision.go`:

```go
// Package provision renders the gateway-address provisioning that a node-repo
// flash tool (e.g. nodus flash) injects into a device's firmware.config. The
// neutral contract is the firmware.config["porta"] object:
//
//	{"host": <string>, "port": <int>}
//
// which the node's supervisor reads to find its gateway. WiFi is out of scope
// here (node tools provision it via their own flasher, e.g. jag's --wifi-*
// flags). The injection MECHANISM is the node tool's concern; this package
// fixes only the shape.
package provision

import (
	"fmt"
	"strconv"
	"strings"
)

// PortaConfigKey is the firmware.config key under which the gateway address
// lives: firmware.config["porta"].
const PortaConfigKey = "porta"

// Gateway is a node's gateway address.
type Gateway struct {
	Host string
	Port int
}

// PortaConfig returns the firmware.config["porta"] object for g.
func (g Gateway) PortaConfig() map[string]any {
	return map[string]any{"host": g.Host, "port": g.Port}
}

// ParseGateway parses "host" or "host:port". When the port is omitted it uses
// defPort. The host must be non-empty and the port (if present) numeric.
// IPv6 literals are out of scope (bench provisioning uses IPv4 / hostnames).
func ParseGateway(s string, defPort int) (Gateway, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Gateway{}, fmt.Errorf("empty gateway address")
	}
	host, port := s, defPort
	if i := strings.LastIndex(s, ":"); i >= 0 {
		host = s[:i]
		p, err := strconv.Atoi(s[i+1:])
		if err != nil {
			return Gateway{}, fmt.Errorf("invalid gateway port %q: %w", s[i+1:], err)
		}
		port = p
	}
	if host == "" {
		return Gateway{}, fmt.Errorf("empty gateway host in %q", s)
	}
	return Gateway{Host: host, Port: port}, nil
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./devsdk/provision/... -v`
Expected: PASS (all four tests).

- [ ] **Step 5: Commit**

```bash
git add devsdk/provision/
git commit -m "feat(devsdk): provision — firmware.config[\"porta\"] contract

Stable neutral gateway-address shape {host,port} + host[:port] parser that
nodus flash (C3) injects and the nodus supervisor reads. WiFi stays a node-
tool concern; injection mechanism is the C3 spike.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: `docs/DEVSDK.md` — the northbound contract

**Files:**
- Create: `docs/DEVSDK.md`

- [ ] **Step 1: Write the document**

Create `docs/DEVSDK.md` with this content:

```markdown
# porta dev-SDK (`devsdk/`) — the northbound contract

`devsdk/` is porta's **public** Go surface for node-repo dev tools (`nodus`,
`nodus-st`). It is the northbound counterpart to `docs/PROTOCOL.md` (the
southbound wire contract). **Dependencies point one way:** node repos import
`github.com/davidg238/porta/devsdk/...`; **porta never imports a node repo.**
See `docs/specs/2026-06-03-porta-devsdk-nodus-flash-design.md` for the full
architecture.

## Packages

- `devsdk/apiclient` — HTTP client for the porta control-plane API
  (`internal/apisrv`). Cobra-free and store-free: dev tools POST/PATCH the
  server instead of opening the store, keeping the server the single writer.
- `devsdk/exec` — injectable, narrating runner for external dev tools
  (`toit`, `jag`, `esptool`): `Runner` abstracts the shell-out (tests inject a
  fake); `Executor` narrates each command (tidy summary, or full transcript
  when verbose).
- `devsdk/provision` — the gateway-address provisioning contract (below).

Deferred (not yet present): `devsdk/flash` (a neutral flasher interface — its
shape will be derived in C3 from the real nodus flasher, then promoted here if
`nodus-st` reuses it; the jag-specific wrapper stays in `nodus/tool/flash`) and
`devsdk/opverbs` (reusable neutral `list`/`log`/`monitor` cobra commands).

## API envelope

Every control-plane response is `{"ok":bool,"data":<json>,"error":string}`.
`apiclient` decodes this; on a transport failure it adds an "is `porta serve`
running?" hint. The base URL defaults to `$PORTA_SERVER` or
`http://localhost:6970`.

## `firmware.config["porta"]` provisioning contract

A node finds its gateway from its firmware config at the key `porta`:

    firmware.config["porta"] = {"host": <string>, "port": <int>}

`devsdk/provision` fixes this shape (`Gateway.PortaConfig()`, `ParseGateway`).
A node-repo flash tool injects it at first flash; the node's supervisor reads
it (falling back to a compiled-in default for bench `jag run`). WiFi is **not**
part of this contract — node tools provision WiFi via their own flasher (e.g.
jag's `--wifi-ssid/--wifi-password`). The *injection mechanism* is the node
tool's concern; `devsdk` fixes only the shape.

## Neutrality

The porta gateway implements **zero** language- or hardware-specific function.
All language/hardware specifics (compile, relocate, flash, decode, per-kind
presentation) live in node repos. Any future language-specific *gateway*
function arrives as a node-repo-owned **sidecar** process, never compiled into
the gateway binary (see design spec §6).
```

- [ ] **Step 2: Sanity-check the doc renders and links resolve**

Run: `test -f docs/DEVSDK.md && grep -c "firmware.config" docs/DEVSDK.md`
Expected: file exists; at least 2 matches.

- [ ] **Step 3: Commit**

```bash
git add docs/DEVSDK.md
git commit -m "docs: DEVSDK.md — northbound dev-SDK contract

Documents the public devsdk surface (apiclient/exec/provision), the API
envelope, the firmware.config[\"porta\"] provisioning shape, the one-way
dependency rule, and the sidecar-only model for gw extensions.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: Whole-tree verification (zero behavior change)

**Files:** none (verification only).

- [ ] **Step 1: Full build + vet + test**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all PASS — the carve changed no behavior.

- [ ] **Step 2: Confirm the old paths are fully gone**

Run: `grep -rn "internal/apiclient" --include="*.go" . ; test ! -f internal/toolchain/exec.go && echo "exec moved OK"`
Expected: no `internal/apiclient` matches; "exec moved OK" printed.

- [ ] **Step 3: Confirm the new public surface exists**

Run: `ls devsdk/apiclient/ devsdk/exec/ devsdk/provision/ docs/DEVSDK.md`
Expected: all three package dirs + the doc present.

- [ ] **Step 4: Smoke the CLI still wires up (no panic, help renders)**

Run: `go run ./cmd/porta --help`
Expected: the porta command tree prints (serve/scan/ping/device/container/log/monitor/panic/run), no error.

---

## Self-review notes (for the implementer)

- **Spec coverage:** C1's §4 scope is fully covered — apiclient (T1), exec (T2), provision
  (T3), DEVSDK.md (T4), zero-behavior-change acceptance (T5). `devsdk/flash` and
  `devsdk/opverbs` are deferred per spec §3.1 (no task by design).
- **Type consistency:** the runner type is `exec.Executor` / `exec.Runner` / `exec.ExecRunner`
  / `exec.NewExecutor` everywhere after T2; the client type is `apiclient.Client` at the new
  path after T1. `provision.Gateway` / `Gateway.PortaConfig()` / `provision.ParseGateway` are
  used consistently in T3 and documented identically in T4.
- **No new behavior** is introduced except `devsdk/provision` (which is covered by its own
  unit tests in T3). Everything else is a path/package move kept green by existing tests.

---

## C2 handoff enabler (note, not a task here)

C2/C3 run in the **nodus** repo (`~/workspaceToit/nodus`) against this spec. To import the
freshly-carved `devsdk` before a porta release is tagged, the nodus `go.mod` will use a
local replace directive:

    require github.com/davidg238/porta v0.0.0
    replace github.com/davidg238/porta => ../porta

(or a pinned tag once C1 is merged + tagged). The nodus agent reads
`docs/specs/2026-06-03-porta-devsdk-nodus-flash-design.md` + `docs/DEVSDK.md` (absolute
paths under `~/workspaceToit/porta/`) and needs no other porta context. A self-contained
nodus-side C2+C3 plan will be written into the nodus repo when C1 lands.
