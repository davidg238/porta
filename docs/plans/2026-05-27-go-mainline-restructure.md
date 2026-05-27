# Go-Mainline Restructure (sub-project A) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the Go server porta's mainline at repo root and park the Toit gateway as a still-deployable example, with **zero server behavior change**.

**Architecture:** Pure file restructure of the `porta` repo. The Go server (`gateway-go/`) moves to an idiomatic `cmd/porta` + `internal/` layout at repo root with its module renamed `github.com/davidg238/jast-gw` → `github.com/davidg238/porta`. The full Toit gateway (`gateway/`) moves intact to `examples/toit-gateway/` so the live gw85224-01 deployment keeps building. Compile-service aux files relocate verbatim (reworked later in sub-project C). Each task is independently buildable and committed.

**Tech Stack:** Go 1.26 (module at repo root), Toit (host, via the custom `toit-sqlite` runtime at `~/workspaceToit/sqlite/build/bin/toit-sqlite`), GitHub Actions.

**Spec:** `docs/specs/2026-05-27-porta-go-mainline-restructure-design.md`

**Conventions for every task:** run all commands from the repo root `~/workspaceToit/porta` unless stated otherwise. The branch `refactor/go-mainline-restructure` already exists and is checked out.

---

## File Structure (after this plan)

```
porta/
  cmd/porta/main.go              # was gateway-go/main.go
  internal/{cli,debug,debugui,gateway,helpers,mcpserver,store,tftp}/   # was gateway-go/<pkg>/
  go.mod  go.sum                 # at root; module github.com/davidg238/porta
  examples/toit-gateway/         # was gateway/ (full Toit gateway, intact + deployable)
    run-host-tests.sh            # NEW: runs *_test.toit via toit-sqlite
  scripts/run-compile-service.sh # was gateway-go/scripts/ (stale paths; fixed in C)
  deploy/                        # Toit-gateway container deploy (build-kit.sh repointed)
    compile-svc/{README.md,jast-compile-svc.service}  # was gateway-go/deploy/
  .github/workflows/ci.yml       # NEW: Go build+test
  docs/  CLAUDE.md  README.md
```

---

## Task 1: Move the Go server to repo root (cmd/porta + internal/, module rename)

This is one atomic move — the build is only green once every file has moved and every import is rewritten. Do all steps before the verify step.

**Files:**
- Move: `gateway-go/main.go` → `cmd/porta/main.go`
- Move: `gateway-go/{cli,debug,debugui,gateway,helpers,mcpserver,store,tftp}/` → `internal/<pkg>/`
- Move: `gateway-go/{go.mod,go.sum}` → repo root
- Move: `gateway-go/scripts/run-compile-service.sh` → `scripts/run-compile-service.sh`
- Move: `gateway-go/deploy/{README.md,jast-compile-svc.service}` → `deploy/compile-svc/`
- Modify: `go.mod` (module path), all `*.go` files with a `jast-gw` import (12 files)

- [ ] **Step 1: Establish the baseline is green (in the old location)**

Run:
```bash
( cd gateway-go && go build ./... && go test ./... )
```
Expected: builds and tests pass (exit 0). This is the behavior we must preserve.

- [ ] **Step 2: Move every file with `git mv`**

```bash
mkdir -p cmd/porta internal scripts deploy/compile-svc
git mv gateway-go/main.go cmd/porta/main.go
for d in cli debug debugui gateway helpers mcpserver store tftp; do
  git mv "gateway-go/$d" "internal/$d"
done
git mv gateway-go/go.mod go.mod
git mv gateway-go/go.sum go.sum
git mv gateway-go/scripts/run-compile-service.sh scripts/run-compile-service.sh
git mv gateway-go/deploy/README.md deploy/compile-svc/README.md
git mv gateway-go/deploy/jast-compile-svc.service deploy/compile-svc/jast-compile-svc.service
rm -rf gateway-go    # clears now-empty dirs + any untracked leftovers (e.g. a *.db)
```
Expected: `gateway-go/` no longer exists; `git status` shows renames.

- [ ] **Step 3: Rename the module in `go.mod`**

Edit `go.mod` line 1:
```
module github.com/davidg238/porta
```
(was `module github.com/davidg238/jast-gw`). Leave the `go 1.26.1` line and `require` blocks unchanged.

- [ ] **Step 4: Rewrite every internal import path**

The 8 packages now live under `internal/`, so `…/jast-gw/<pkg>` becomes `…/porta/internal/<pkg>`:
```bash
grep -rl 'github.com/davidg238/jast-gw/' --include='*.go' . \
  | xargs sed -i 's#github.com/davidg238/jast-gw/#github.com/davidg238/porta/internal/#g'
```
Verify nothing references the old path anymore:
```bash
grep -rn 'davidg238/jast-gw' --include='*.go' . ; echo "exit: $?"
```
Expected: no matches (grep exit 1).

- [ ] **Step 5: Verify the build and tests are green from repo root**

```bash
go build ./... && go test ./...
```
Expected: builds and all tests pass (exit 0) — identical to the Step 1 baseline, now from the new layout. `//go:embed static/debug.html` in `internal/debugui/handler.go` still resolves because `static/` moved with the package.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "$(cat <<'EOF'
refactor(porta): Go server → repo root (cmd/porta + internal/), module → .../porta

Move gateway-go/ to idiomatic layout: main.go→cmd/porta, the 8 packages→internal/,
go.mod/go.sum→root. Rename module github.com/davidg238/jast-gw→.../porta and rewrite
import prefixes to .../porta/internal/<pkg>. Relocate compile-svc aux files verbatim
(scripts/, deploy/compile-svc/ — paths reworked in sub-project C). No behavior change.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: Park the Toit gateway → examples/toit-gateway

Move the whole Toit gateway intact so `deploy/build-kit.sh` still builds the live gw85224-01 container, and add a runnable host-test script so the example stays honestly testable.

**Files:**
- Move: `gateway/` → `examples/toit-gateway/`
- Create: `examples/toit-gateway/run-host-tests.sh`
- Modify: `deploy/build-kit.sh` (compile path)

- [ ] **Step 1: Move the gateway directory**

The untracked `.packages/` is regenerated later, so drop it before the move to keep the rename clean:
```bash
rm -rf gateway/.packages
mkdir -p examples
git mv gateway examples/toit-gateway
```
Expected: `examples/toit-gateway/gateway.toit` exists; `gateway/` is gone.

- [ ] **Step 2: Repoint the deploy build kit**

Edit `deploy/build-kit.sh`, the compile line:
```bash
( cd "$REPO/examples/toit-gateway" && "$TS" compile --snapshot -o "$KIT/gateway.snapshot" gateway.toit )
```
(was `cd "$REPO/gateway"`). No other change to `deploy/`.

- [ ] **Step 3: Add a host-test script**

Create `examples/toit-gateway/run-host-tests.sh`:
```bash
#!/usr/bin/env bash
# Run the Toit gateway host test suite with the custom toit-sqlite runtime
# (the sqlite dep links an external C lib, so the stock `toit` runtime can't RUN it;
# stock `toit` still resolves packages fine). Override with TOIT_SQLITE=/path/to/bin.
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TS="${TOIT_SQLITE:-$HOME/workspaceToit/sqlite/build/bin/toit-sqlite}"
[ -x "$TS" ] || { echo "toit-sqlite not found at $TS (set TOIT_SQLITE)"; exit 1; }

cd "$HERE"
toit pkg install >/dev/null          # regenerate .packages (path + url deps)
fail=0
for t in *_test.toit; do
  printf '%-28s ' "$t"
  if "$TS" run "$t" >/tmp/toit-test.log 2>&1; then echo PASS; else echo FAIL; cat /tmp/toit-test.log; fail=1; fi
done
exit $fail
```
Make it executable:
```bash
chmod +x examples/toit-gateway/run-host-tests.sh
```

- [ ] **Step 4: Verify the suite passes from the new path**

```bash
./examples/toit-gateway/run-host-tests.sh
```
Expected: every `*_test.toit` prints `PASS` and the script exits 0. (Regenerates `.packages` via `toit-sqlite pkg install` first; the local sqlite path dep in `package.yaml` resolves on this dev box.)

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "$(cat <<'EOF'
refactor(porta): park Toit gateway → examples/toit-gateway (intact, deployable)

Move the full Toit gateway under examples/ so deploy/build-kit.sh still builds the
live gw85224-01 container (build path repointed). Add run-host-tests.sh so the example
stays honestly host-tested via the toit-sqlite runtime. Telemetry-slice trim deferred.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: Housekeeping — root artifacts, .gitignore, docs

**Files:**
- Delete (untracked): root `*.bin *.snapshot *.image *.envelope`
- Modify: `.gitignore`, `CLAUDE.md`, `README.md`

- [ ] **Step 1: Remove the untracked root build artifacts**

```bash
rm -f *.bin *.snapshot *.image *.envelope
```
These are gitignored regenerated outputs (chatty/control_demo/vin/supervisor/firmware-esp32), so nothing tracked changes.

- [ ] **Step 2: Fix the stale Go-binary path in `.gitignore`**

Edit `.gitignore`, replace the line:
```
# Go gateway build output
gateway/jast-gw
```
with:
```
# Go server build output
/porta
/cmd/porta/porta
```

- [ ] **Step 3: Update the Layout section in `CLAUDE.md`**

Replace the `gateway/` and `gateway-go/` bullets in the `## Layout` section with:
```markdown
- `cmd/porta/` + `internal/` — the **Go gateway** (mainline), module
  `github.com/davidg238/porta`. The northbound control plane: command queue, node
  inventory, telemetry, TFTP delivery, MCP (`internal/mcpserver`), htmx debug UI
  (`internal/debugui`). Talks to the ST compile service over HTTP (`-compile-url`).
- `examples/toit-gateway/` — the parked Toit gateway (a full, still-deployable Toit
  rewrite of the control plane, backed by the Toit `sqlite`/`tftp` libs). Kept as a
  living example; `deploy/build-kit.sh` builds it into the gw85224-01 container.
  Run its host tests with `examples/toit-gateway/run-host-tests.sh`.
- `tools/` — **planned** (sub-project C): `tools/smalltalk` (ST→bytecode transpiler,
  moving in from st-zephyr) + `tools/toit` (jag/SDK image build). Not present yet.
```
Also update the `deploy/` bullet to note `deploy/compile-svc/` holds the ST compile-service unit/readme.

- [ ] **Step 4: Update `README.md`**

In `README.md`, update any path references so the Go server is described as the
mainline at `cmd/porta`/`internal/`, and the Toit gateway as `examples/toit-gateway`.
(Leave the user's existing `#### Links` section intact.)

- [ ] **Step 5: Verify the build still passes (paths only changed in docs/ignore)**

```bash
go build ./...
```
Expected: exit 0.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "$(cat <<'EOF'
chore(porta): housekeeping — clean root artifacts, fix gitignore, update docs

Drop stale root build artifacts (gitignored), repoint the Go-binary gitignore entry,
and update CLAUDE.md/README to the Go-mainline layout (cmd/porta+internal/, Toit gw at
examples/toit-gateway, tools/ flagged as planned).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: Add CI (Go on GitHub; Toit tested locally)

GitHub CI covers the Go server fully. Full Toit-example CI is **not** feasible on a stock runner: the `gateway` package depends on `sqlite` via a machine-specific absolute path (an unpublished local package) and needs the custom `toit-sqlite` runtime. The example stays tested via `run-host-tests.sh` on the dev box; the workflow documents this.

**Files:**
- Create: `.github/workflows/ci.yml`

- [ ] **Step 1: Create the workflow**

Create `.github/workflows/ci.yml`:
```yaml
name: ci
on:
  push:
    branches: [master]
  pull_request:

jobs:
  go:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - run: go build ./...
      - run: go test ./...

  # NOTE: the Toit example (examples/toit-gateway) is NOT built in CI — its sqlite
  # dependency is a local, unpublished package referenced by absolute path, and it
  # requires the custom toit-sqlite runtime. Test it locally with:
  #   ./examples/toit-gateway/run-host-tests.sh
  # This is revisited if/when sqlite is published (relates to sub-project C tooling).
```

- [ ] **Step 2: Verify the workflow's commands locally**

```bash
go build ./... && go test ./...
```
Expected: exit 0 (these are exactly what the `go` job runs).

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "$(cat <<'EOF'
ci(porta): add GitHub Actions Go build+test

Go server is CI-gated on push/PR (build + test, Go version from go.mod). Toit example
CI is intentionally omitted (local unpublished sqlite dep + custom runtime); it is
tested locally via examples/toit-gateway/run-host-tests.sh — documented in the workflow.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Final verification (after all tasks)

- [ ] `go build ./... && go test ./...` green from repo root.
- [ ] `./examples/toit-gateway/run-host-tests.sh` all PASS.
- [ ] `git grep -n 'davidg238/jast-gw'` returns nothing in `*.go` (module fully renamed).
- [ ] `deploy/build-kit.sh` points at `examples/toit-gateway` (visual check; a full kit build needs `toit-sqlite` + the sqlite build tree).
- [ ] Repo root is clean: `cmd/ internal/ examples/ deploy/ scripts/ docs/ .github/ go.mod go.sum README.md CLAUDE.md` and no leftover `gateway-go/` or root `*.bin/*.snapshot`.
