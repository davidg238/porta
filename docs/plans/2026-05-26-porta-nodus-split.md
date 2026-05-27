# porta → porta/nodus Split Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Carve the node project (`nodus`) out of the gateway project (`porta`) into two repos that share no code and talk only over the wire — first proving the boundary in-repo (`device/` → a canonical `nodus/` Toit package + examples + tooling, with zero cross-imports), then extracting `nodus/` into its own repo with history.

**Architecture:** Three staged phases. **Phase 0** merges the hardware-verified run-once lifecycle framework to `master` so it lands on both sides before any carving. **Phase 1** restructures everything node-side under a single `nodus/` subtree as a proper Toit package (`src/` + `tests/` + `examples/` + `host/` + `docs/`), fixes every build/test seam, and verifies the gateway↔nodus import boundary is empty. **Phase 2** extracts `nodus/` via `git subtree split` into a new repo and trims it from `porta`. The wire contract gets a canonical `PROTOCOL.md` owned by `porta` (the gateway controls heterogeneous nodes — `nodus` is just the first implementation).

**Tech Stack:** Toit (device firmware + Toit gateway), Toit Package Manager (`toit pkg install --local`), `expect` host tests run with `toit <file>_test.toit`, Go tests (`go test ./...`), `git subtree`, `gh`. SDK pinned at `v2.0.0-alpha.192` (`host/SDK_VERSION`).

**Reference spec:** `docs/specs/2026-05-26-porta-nodus-split-design.md`.

**Executor legend:** 🤖 = agent-executable. 🧑 = **user-run** (the environment classifier gates push-to-master and remote/`gh` writes; the user runs these via `! <command>` so output lands in the session).

---

## File Map

**Phase 1 target layout (in the `porta` repo, on a fresh branch):**

```
porta/
├── nodus/                              ← single subtree extracted in Phase 2
│   ├── package.yaml                    name: nodus; dep: tftp (renamed from loader)
│   ├── package.lock
│   ├── src/                            ← all library + service modules (relative imports kept)
│   │   ├── supervisor.toit  transport.toit  goal_state.toit  inventory.toit
│   │   ├── node_command.toit  node_id.toit  report.toit  triggers.toit
│   │   ├── image_writer.toit  flash_image.toit  schedule_store.toit  config_store.toit
│   │   ├── control_service.toit  telemetry_service.toit
│   │   └── telemetry_buffer.toit  telemetry_codec.toit
│   ├── tests/
│   │   ├── package.yaml                dep: nodus (path: ..)
│   │   ├── package.lock
│   │   └── *_test.toit                 imports rewritten `.X` → `nodus.X`
│   ├── examples/
│   │   ├── vindriktning/  vin.toit + vindriktning.toit + olympic.toit + olympic_test.toit + package.yaml (path: ../..)
│   │   ├── chatty/        chatty.toit + package.yaml (path: ../..)
│   │   ├── control-demo/  control_demo.toit + package.yaml (path: ../..)
│   │   └── hello/         hello.toit (no package.yaml — no deps)
│   ├── host/             build-envelope.sh + capture_sink.go + SDK_VERSION + image + image.crc32
│   ├── docs/             node-side specs/plans
│   ├── README.md
│   └── CLAUDE.md
├── gateway/  gateway-go/  deploy/      ← stays = porta (unchanged)
├── docs/                               ← gateway-side specs/plans + PROTOCOL.md
└── CLAUDE.md                           ← trimmed to gateway scope
```

**Test invocation reference:**
- One Toit suite: `toit nodus/tests/<name>_test.toit` (exit 0 = pass).
- All node suites: `for f in nodus/tests/*_test.toit nodus/examples/*/*_test.toit; do toit "$f" >/dev/null 2>&1 && echo "ok  $f" || echo "FAIL $f"; done`
- Gateway Toit suites: `for f in gateway/*_test.toit; do toit "$f" >/dev/null 2>&1 && echo "ok  $f" || echo "FAIL $f"; done`
- Go: `cd gateway-go && go test ./...`
- Image build: `toit compile -s -o X.snapshot <file>.toit && toit tool snapshot-to-image -m32 --format=binary -o X.bin X.snapshot`

---

## Phase 0 — Land the lifecycle framework

### Task 1: Merge `feat/vin-run-once-lifecycle` → master 🧑

**Files:** none (git operation).

- [ ] **Step 1: Confirm the branch is clean and host-green**

Run: `git -C /home/david/workspaceToit/porta status -s` (expect empty) and
`for f in device/*_test.toit gateway/*_test.toit; do toit "$f" >/dev/null 2>&1 && echo "ok $f" || echo "FAIL $f"; done`
Expected: every line `ok`.

- [ ] **Step 2: Merge to master (user-run)**

The classifier gates push-to-master; run via `!`:

```bash
git checkout master && git merge --no-ff feat/vin-run-once-lifecycle -m "Merge feat/vin-run-once-lifecycle: run-once/run-loop lifecycle framework + vin example + split design"
git push origin master
```

- [ ] **Step 3: Verify framework on master**

Run: `git log --oneline -1` (shows the merge commit) and re-run the host-suite loop on `master`.
Expected: merge present; all suites `ok`. The split spec (`docs/specs/2026-05-26-porta-nodus-split-design.md`) is now on master.

---

## Phase 1 — Clean boundary, in-repo

All Phase 1 work happens on a fresh branch off the post-merge `master`.

### Task 2: Scaffold the `nodus` package root (`src/` layout) 🤖

**Files:**
- Rename: `device/` → `nodus/`
- Create: `nodus/src/` (move all non-`_test` `.toit` into it)
- Modify: `nodus/package.yaml` (name `loader` → `nodus`)

- [ ] **Step 1: Branch**

```bash
cd /home/david/workspaceToit/porta
git checkout master && git pull
git checkout -b refactor/split-nodus
```

- [ ] **Step 2: Rename the dir and create `src/`**

```bash
git mv device nodus
mkdir nodus/src
for f in supervisor transport goal_state inventory node_command node_id report triggers image_writer flash_image schedule_store config_store control_service telemetry_service telemetry_buffer telemetry_codec; do git mv "nodus/$f.toit" "nodus/src/$f.toit"; done
```

(Leaves `nodus/package.yaml`, `nodus/package.lock`, and the `*_test.toit` + example `.toit` files at `nodus/` root for now — Tasks 3 and 4 relocate them.)

- [ ] **Step 3: Rename the package**

In `nodus/package.yaml`, change the `name:` line:

```yaml
name: nodus
description: Porta node — on-device loader/supervisor (the keeper) + node services.
```

(Keep the existing `dependencies: { tftp: { path: /home/david/workspaceToit/tftp } }` block unchanged.)

- [ ] **Step 4: Verify the library compiles from `src/`**

Run: `toit compile -s -o /tmp/sup.snapshot nodus/src/supervisor.toit`
Expected: exit 0 (relative imports within `src/` resolve; `nodus/package.yaml` supplies `tftp`). The root-level `*_test.toit` are knowingly broken here — fixed in Task 3.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "refactor(nodus): rename device→nodus; move library modules into src/"
```

---

### Task 3: Move tests to `nodus/tests/` and repoint imports 🤖

**Files:**
- Create: `nodus/tests/` (move all `*_test.toit` into it), `nodus/tests/package.yaml`, `nodus/tests/package.lock`
- Modify: every moved `*_test.toit` (`import .X` → `import nodus.X`)

- [ ] **Step 1: Move the test files**

```bash
cd /home/david/workspaceToit/porta
mkdir nodus/tests
git mv nodus/*_test.toit nodus/tests/
```

- [ ] **Step 2: Register the local package for the tests**

```bash
cd nodus/tests && toit pkg install --local .. && cd ../..
```

This writes `nodus/tests/package.yaml` (a `nodus: { path: .. }` dependency) and `package.lock`. The import prefix defaults to the package name `nodus`.

- [ ] **Step 3: Repoint the imports**

Rewrite leading-dot library imports to package imports (the leading-dot form referenced sibling modules that now live in `src/`):

```bash
for f in nodus/tests/*_test.toit; do sed -i -E 's/^import \.([a-z_]+)/import nodus.\1/' "$f"; done
```

Then **manually verify** no test imported a sibling *test* or fixture (those would be wrongly rewritten):
`grep -nE '^import nodus\.[a-z_]+_test' nodus/tests/*_test.toit` — expect no matches. If any appear, revert that line to a relative `import .name` (the helper should move into `tests/` and stay relative).

- [ ] **Step 4: Verify every suite passes**

Run:

```bash
for f in nodus/tests/*_test.toit; do toit "$f" >/dev/null 2>&1 && echo "ok  $f" || echo "FAIL $f"; done
```

Expected: every line `ok`. Investigate any `FAIL` (most likely a missed/over-eager import rewrite).

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "refactor(nodus): tests→tests/ as a local-dep package; import nodus.X"
```

---

### Task 4: Move the vindriktning example into `examples/vindriktning/` 🤖

**Files:**
- Create: `nodus/examples/vindriktning/` with `vin.toit`, `vindriktning.toit`, `olympic.toit`, `olympic_test.toit`, `package.yaml`, `package.lock`
- Modify: `vin.toit` (`import .telemetry_service` → `import nodus.telemetry_service`)

- [ ] **Step 1: Move the files**

```bash
cd /home/david/workspaceToit/porta
mkdir -p nodus/examples/vindriktning
for f in vin vindriktning olympic olympic_test; do git mv "nodus/$f.toit" "nodus/examples/vindriktning/$f.toit"; done
```

- [ ] **Step 2: Register the nodus dependency**

```bash
cd nodus/examples/vindriktning && toit pkg install --local ../.. && cd /home/david/workspaceToit/porta
```

- [ ] **Step 3: Repoint vin's library import**

In `nodus/examples/vindriktning/vin.toit`, change only the device-library import:

```toit
import nodus.telemetry_service show TelemetryServiceClient
```

Leave `import gpio`, `import .vindriktning show Vindriktning`, and `import .olympic show olympic-mean` unchanged (the driver + helper are vendored alongside, so they stay relative).

- [ ] **Step 4: Verify olympic test + vin/payload build**

```bash
toit nodus/examples/vindriktning/olympic_test.toit && echo "olympic ok"
toit compile -s -o /tmp/vin.snapshot nodus/examples/vindriktning/vin.toit && echo "vin compiles"
toit tool snapshot-to-image -m32 --format=binary -o /tmp/vin.bin /tmp/vin.snapshot && ls -l /tmp/vin.bin
```

Expected: `olympic ok`; `vin compiles`; `vin.bin` ~40 KB.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "refactor(nodus): vindriktning example → examples/vindriktning/ (nodus path-dep)"
```

---

### Task 5: Move the remaining examples (chatty, control-demo, hello) 🤖

**Files:**
- Create: `nodus/examples/chatty/{chatty.toit,package.yaml,package.lock}`, `nodus/examples/control-demo/{control_demo.toit,package.yaml,package.lock}`, `nodus/examples/hello/hello.toit`
- Modify: `chatty.toit`, `control_demo.toit` (library imports → `nodus.X`)

- [ ] **Step 1: Move the files**

```bash
cd /home/david/workspaceToit/porta
mkdir -p nodus/examples/chatty nodus/examples/control-demo nodus/examples/hello
git mv nodus/chatty.toit       nodus/examples/chatty/chatty.toit
git mv nodus/control_demo.toit nodus/examples/control-demo/control_demo.toit
git mv nodus/hello.toit        nodus/examples/hello/hello.toit
```

- [ ] **Step 2: Register nodus deps (chatty, control-demo only — hello has none)**

```bash
cd nodus/examples/chatty       && toit pkg install --local ../.. && cd /home/david/workspaceToit/porta
cd nodus/examples/control-demo && toit pkg install --local ../.. && cd /home/david/workspaceToit/porta
```

- [ ] **Step 3: Repoint library imports**

In `nodus/examples/chatty/chatty.toit`:

```toit
import nodus.telemetry_service show TelemetryServiceClient
```

In `nodus/examples/control-demo/control_demo.toit`:

```toit
import nodus.control_service show ControlServiceClient
import nodus.telemetry_service show TelemetryServiceClient
```

`hello.toit` has no imports — leave it.

- [ ] **Step 4: Verify each builds**

```bash
for ex in chatty/chatty control-demo/control_demo hello/hello; do
  toit compile -s -o /tmp/ex.snapshot "nodus/examples/$ex.toit" && echo "ok  $ex" || echo "FAIL $ex"
done
```

Expected: three `ok` lines.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "refactor(nodus): chatty/control-demo/hello → examples/ (nodus path-dep)"
```

---

### Task 6: Relocate host tooling and repoint `build-envelope.sh` 🤖

**Files:**
- Rename: `host/` → `nodus/host/`
- Modify: `nodus/host/build-envelope.sh` (`device/supervisor.toit` → `src/supervisor.toit`)

- [ ] **Step 1: Move host/**

```bash
cd /home/david/workspaceToit/porta
git mv host nodus/host
```

- [ ] **Step 2: Repoint the supervisor source path**

In `nodus/host/build-envelope.sh`, change the compile line:

```bash
toit compile -s -o supervisor.snapshot src/supervisor.toit
```

(The script is now run from the `nodus/` directory; `src/supervisor.toit` is relative to that. If the script computes paths from its own location, adjust the `device/` reference there instead.)

- [ ] **Step 3: Verify the envelope builds**

Run from the `nodus/` directory (per the script's documented invocation `bash host/build-envelope.sh`):

```bash
cd /home/david/workspaceToit/porta/nodus && bash host/build-envelope.sh && cd /home/david/workspaceToit/porta
```

Expected: the script completes and reports the supervisor container present (no `jaguar`). Investigate any path error.

- [ ] **Step 4: Commit**

```bash
git add -A && git commit -m "refactor(nodus): host/ tooling → nodus/host/; build-envelope uses src/supervisor.toit"
```

---

### Task 7: Split docs between the two projects 🤖

**Files:**
- Rename: node-side specs/plans → `nodus/docs/`

- [ ] **Step 1: Move node-side docs**

```bash
cd /home/david/workspaceToit/porta
mkdir -p nodus/docs/specs nodus/docs/plans
git mv docs/specs/2026-05-21-toit-tftp-loader-design.md            nodus/docs/specs/
git mv docs/specs/2026-05-22-porta-no-jaguar-supervisor-design.md  nodus/docs/specs/
git mv docs/specs/2026-05-24-node-lifecycle-reliability-design.md  nodus/docs/specs/
git mv docs/plans/2026-05-21-toit-tftp-loader-smoke-test.md        nodus/docs/plans/
git mv docs/plans/2026-05-22-porta-no-jaguar-supervisor.md         nodus/docs/plans/
git mv docs/plans/2026-05-26-vin-run-once-lifecycle.md             nodus/docs/plans/
```

(Gateway-side docs — `porta-toit-gateway-design`, `config-self-heal-*`, `d5-*`, `m2-*`, `tftp-engine-*`, `gateway-b1/b2`, `security-transport-evolution`, and the split design itself — stay in `docs/`.)

- [ ] **Step 2: Verify the split is sensible**

Run: `ls docs/specs docs/plans nodus/docs/specs nodus/docs/plans`
Expected: node-lifecycle/loader/no-jaguar/vin docs under `nodus/docs/`; gateway + split-design docs remain under `docs/`.

- [ ] **Step 3: Commit**

```bash
git add -A && git commit -m "refactor(nodus): node-side specs/plans → nodus/docs/"
```

---

### Task 8: `.gitignore`, CLAUDE.md, READMEs 🤖

**Files:**
- Modify: `.gitignore`
- Create: `nodus/README.md`, `nodus/CLAUDE.md`
- Modify: `CLAUDE.md` (trim to gateway scope), move/repoint the existing top-level README content as needed

- [ ] **Step 1: Update `.gitignore`**

Replace the explicit per-file artifact rules (the `/chatty.bin`, `/control_demo.bin`, `/vin.bin`, `/supervisor.*`, `/firmware-esp32.envelope*` block) with globs that cover the new locations:

```gitignore
# Build artifacts (regenerated)
*.snapshot
*.bin
*.image
*.envelope
*.envelope.gz
nodus/host/image
nodus/host/image.crc32
nodus/host/firmware
```

Keep the `*.db`, `gateway/jast-gw`, `.packages/`, `build/`, `/st-zephyr` rules. Then remove any now-stale root artifacts: `git rm --cached chatty.bin chatty.snapshot control_demo.bin control_demo.snapshot vin.bin vin.snapshot supervisor.snapshot supervisor.image firmware-esp32.envelope 2>/dev/null || true` (these are gitignored build outputs; ignore "did not match" errors).

- [ ] **Step 2: Write `nodus/CLAUDE.md`**

Create `nodus/CLAUDE.md` describing the node project: it is the on-device loader/supervisor ("the keeper") + node services, packaged as the `nodus` Toit package (`src/` library, `tests/`, `examples/`); it speaks the wire protocol defined canonically in the **porta** repo's `docs/PROTOCOL.md` (link); it is one of potentially several node implementations (e.g. future Smalltalk nodes); examples are built with `toit compile -s` + `toit tool snapshot-to-image -m32`; firmware via `host/build-envelope.sh`. Note the always-on-vin milestone as next.

- [ ] **Step 3: Write `nodus/README.md`**

One-paragraph project description + build/test quickstart (the test invocation reference above; `host/build-envelope.sh`).

- [ ] **Step 4: Trim `porta` `CLAUDE.md` to gateway scope**

Edit the root `CLAUDE.md`: remove the `device/`, `host/` layout bullets and the Toit-loader milestone narrative; describe `porta` as the gateway project (Go gateway + Toit gateway control plane + sqlite + deploy kit) that owns the wire protocol (`docs/PROTOCOL.md`) and controls heterogeneous nodes; cross-link the `nodus` repo (URL added in Phase 2).

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "refactor: gitignore globs; nodus README+CLAUDE; trim porta CLAUDE to gateway scope"
```

---

### Task 9: Author the canonical wire-protocol doc 🤖

**Files:**
- Create: `docs/PROTOCOL.md` (in the `porta` repo)

- [ ] **Step 1: Write `docs/PROTOCOL.md`**

Document the contract every node implementation must conform to, derived from `gateway/command.toit`, `nodus/src/node_command.toit`, and `nodus/src/report.toit`:
- **Commands** (northbound→node): each verb (`run` incl. the `lifecycle` field, `set`, console toggles, etc.) and its JSON arg schema.
- **Report / observed-state** (node→northbound): the report JSON shape, including per-app `runlevel`/`lifecycle`/`triggers` echo and observed-config.
- **TFTP delivery blob:** `[u32 size_le][u32 crc32_le][image bytes]`, CRC32-IEEE, read the 8-byte header then stream the image.
- A note that this is the authority for `nodus` and any future node implementation (e.g. Smalltalk).

- [ ] **Step 2: Verify it matches code**

Run: `grep -nE 'VERB-|"lifecycle"|"runlevel"|"triggers"' gateway/command.toit nodus/src/node_command.toit nodus/src/report.toit`
Cross-check each documented field appears in code. Fix mismatches in the doc.

- [ ] **Step 3: Commit**

```bash
git add docs/PROTOCOL.md && git commit -m "docs(porta): canonical wire-protocol spec (porta owns the contract)"
```

---

### Task 10: Boundary verification gate 🤖

**Files:** none (verification + final Phase-1 commit if anything was missed).

- [ ] **Step 1: Prove zero cross-imports**

```bash
cd /home/david/workspaceToit/porta
echo "nodus importing gateway:"; grep -rnE '^import .*gateway' nodus/ || echo "  (none)"
echo "gateway importing nodus/device:"; grep -rnE '^import .*(nodus|device)' gateway/ || echo "  (none)"
```

Expected: both `(none)`. Any hit is a boundary violation — resolve before proceeding.

- [ ] **Step 2: Full node regression**

```bash
for f in nodus/tests/*_test.toit nodus/examples/*/*_test.toit; do toit "$f" >/dev/null 2>&1 && echo "ok  $f" || echo "FAIL $f"; done
```

Expected: every line `ok`.

- [ ] **Step 3: All example images build**

```bash
for ex in vindriktning/vin chatty/chatty control-demo/control_demo hello/hello; do
  toit compile -s -o /tmp/b.snapshot "nodus/examples/$ex.toit" >/dev/null 2>&1 && echo "ok  $ex" || echo "FAIL $ex"
done
```

Expected: four `ok` lines.

- [ ] **Step 4: Supervisor envelope + gateway suites**

```bash
( cd nodus && bash host/build-envelope.sh >/dev/null 2>&1 && echo "envelope ok" || echo "envelope FAIL" )
for f in gateway/*_test.toit; do toit "$f" >/dev/null 2>&1 && echo "ok  $f" || echo "FAIL $f"; done
( cd gateway-go && go test ./... )
```

Expected: `envelope ok`; all gateway Toit suites `ok`; Go packages `ok`.

- [ ] **Step 5: Commit any cleanup, then mark Phase 1 done**

```bash
git add -A && git commit -m "refactor(nodus): boundary verified — no cross-imports, all suites/images/envelope green" --allow-empty
```

The branch `refactor/split-nodus` now contains a self-contained `nodus/` subtree with a proven-empty gateway↔nodus boundary, ready to extract.

---

## Phase 2 — Extract `nodus` into its own repo

Phase 2 is git/GitHub surgery. Merge `refactor/split-nodus` → `master` first (🧑, push-gated), then extract from `master`.

### Task 11: Merge the restructure to master 🧑

- [ ] **Step 1 (user-run):**

```bash
git checkout master && git merge --no-ff refactor/split-nodus -m "Merge refactor/split-nodus: node project carved into self-contained nodus/ subtree"
git push origin master
```

- [ ] **Step 2: Verify** — re-run the Task 10 verification loops on `master`. Expect all green.

---

### Task 12: Subtree-split `nodus/` with history 🤖

**Files:** none (produces a local branch).

- [ ] **Step 1: Split**

```bash
cd /home/david/workspaceToit/porta
git checkout master
git subtree split --prefix=nodus -b nodus-export
```

- [ ] **Step 2: Verify the export branch**

Run: `git log --oneline nodus-export | head` and `git ls-tree --name-only nodus-export`
Expected: history present; tree root shows `package.yaml`, `src/`, `tests/`, `examples/`, `host/`, `docs/`, `README.md`, `CLAUDE.md` (i.e. the `nodus/` prefix is stripped — it is now the repo root).

---

### Task 13: Create the `nodus` GitHub repo and push 🧑

- [ ] **Step 1 (user-run):** create the private repo and push the export branch as its default.

```bash
gh repo create nodus --private --description "Porta node — loader/supervisor + node services + examples (Toit)"
cd /home/david/workspaceToit/porta
git push git@github.com:davidg238/nodus.git nodus-export:master
```

- [ ] **Step 2: Verify** — `gh repo view davidg238/nodus` shows the repo with the pushed tree.

---

### Task 14: Remove `nodus/` from porta 🧑

- [ ] **Step 1 (user-run):**

```bash
cd /home/david/workspaceToit/porta
git checkout master
git rm -r nodus
git commit -m "refactor: nodus extracted to its own repo (github.com/davidg238/nodus); porta is gateway-only"
git push origin master
git branch -D nodus-export refactor/split-nodus
```

- [ ] **Step 2: Verify** — `ls` shows no `nodus/`; `gateway/`, `gateway-go/`, `deploy/`, `docs/` (incl. `PROTOCOL.md`) remain; gateway suites still green; the live gateway on `gw85224-01` is untouched (its code never moved).

---

### Task 15: Stand up the `nodus` working clone 🤖/🧑

**Files (in the new `nodus` repo):** `package.yaml`, CI workflow, `CLAUDE.md`/`README.md` finalize.

- [ ] **Step 1 (user-run): clone**

```bash
cd /home/david/workspaceToit && git clone git@github.com:davidg238/nodus.git
```

- [ ] **Step 2: Pin the SDK + fix the path-dep note** 🤖

In `nodus/package.yaml`, add the SDK environment pin and keep the machine-path caveat for `tftp`:

```yaml
name: nodus
description: Porta node — on-device loader/supervisor (the keeper) + node services.
environment:
  sdk: ^2.0.0-alpha.192
dependencies:
  # machine-specific absolute path (packages live under ~/workspaceToit/);
  # a clone on a different layout must adjust this path.
  tftp:
    path: /home/david/workspaceToit/tftp
```

Run `toit pkg install` at the repo root and confirm `toit compile -s -o /tmp/sup.snapshot src/supervisor.toit` succeeds.

- [ ] **Step 3: Add CI** 🤖

Add `.github/workflows/ci.yml` running the node host suites (`tests/*_test.toit`, `examples/*/*_test.toit`) on the pinned SDK (model on the existing porta gateway CI if present, else the toit-package skill's `resources/.github/workflows/ci.yml`).

- [ ] **Step 4: Finalize cross-links** 🤖

In `nodus/CLAUDE.md` and `README.md`, set the live URL to the porta `docs/PROTOCOL.md`. In the porta `CLAUDE.md`, set the `nodus` repo URL.

- [ ] **Step 5: Commit + push** 🧑

```bash
cd /home/david/workspaceToit/nodus
git add -A && git commit -m "chore: SDK pin, CI, protocol cross-link"
git push origin master
```

---

### Task 16: Update auto-memory 🤖

**Files:** memory under `/home/david/.claude/projects/-home-david-workspaceToit-porta/memory/`.

- [ ] **Step 1:** Add/update a memory recording the split: two repos (`porta` = gateway, `nodus` = node), `nodus` is a Toit package (`src`/`tests`/`examples`), porta owns the wire protocol (`docs/PROTOCOL.md`) for heterogeneous nodes (Smalltalk planned), always-on-vin is the next node milestone. Update `MEMORY.md` index line and the `porta-vindriktning-lifecycle` / `porta-github-remote` pointers. Commit is implicit (memory files are not part of repo).

---

## Self-Review

**Spec coverage:**
- *Two repos, no code dep, wire-only coupling* → Phase 2 (Tasks 12–14) + boundary gate Task 10. ✅
- *Phase 0 merge framework first* → Task 1. ✅
- *Phase 1 nodus package (src/tests/examples/host/docs), seams fixed, boundary verified* → Tasks 2–10. ✅
- *Import mechanics (relative in src; `nodus.X` in tests/examples; vendored vin driver)* → Tasks 2–5. ✅
- *Seams: build-envelope, .bin paths, .gitignore, test runner, CLAUDE.md* → Tasks 6, 8, 10. ✅
- *Wire contract = porta-owned PROTOCOL.md, heterogeneous/Smalltalk nodes* → Task 9. ✅
- *Phase 2 subtree extraction + repo setup + memory* → Tasks 11–16. ✅
- *Non-goals (gateway not restructured; always-on deferred; keep `_test.toit`)* → respected throughout (gateway untouched; vin stays run-once; no test renames). ✅

**Placeholder scan:** every step has exact commands/paths/content; doc-authoring steps (Tasks 8, 9, 15, 16) specify exact contents to write and a verification. No TBD/TODO. ✅

**Type/name consistency:** package name `nodus` used uniformly; import prefix `nodus.<module>` consistent across tests + examples; module set in `src/` matches the File Map; subtree prefix `nodus` matches the dir name; build pipeline (`toit compile -s` → `snapshot-to-image -m32`) identical everywhere. ✅
</content>
