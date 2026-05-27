# porta → Go-mainline restructure (sub-project A)

**Date:** 2026-05-27
**Status:** approved (design)
**Scope:** repo restructure only — no server behavior change.

## Larger context (the renovation this is the first slice of)

`porta` was the gateway carved out of `st-zephyr`. The Go dev-server originally
built for Smalltalk-on-Zephyr (read ST source → compile to `.bec` bytecode → ship
to a berry VM over UDP/TFTP, command-pattern, async monitoring, MCP + htmx UI)
generalizes: the *same* server, with different commands/payloads, also drives Toit
nodes. We proved the concept by rewriting that Go server in Toit here (which usefully
exercised the Toit `sqlite` + `tftp` libraries). But maintaining a *host* tool in Toit
— and re-implementing MCP + htmx in Toit — is an exercise, not a genuine benefit.

**Decision: consolidate on the Go server.** The agreed end-state is a symmetric
three-repo world:

- **porta** — the *one* Go server/gateway (mainline). Owns the canonical wire protocol
  (`docs/PROTOCOL.md`), serves heterogeneous nodes, and holds server-side language
  tooling under `/tools/toit` + `/tools/smalltalk`.
- **nodus-toit** — the Toit node (already its own repo; currently still named `nodus`).
- **nodus-st** — the Smalltalk/berry node (currently `st-zephyr`, to be stripped to
  node-only).

The two node repos share a `nodus-` prefix to signal they are sibling implementations
of the same node concept — the heterogeneous nodes porta's protocol exists to serve.
The renames are **deferred**, not part of any porta sub-project's file moves:

- `nodus` → `nodus-toit`: after the in-flight `feat/always-on-vin` work merges (renaming
  out from under that live working tree would break it). GitHub auto-redirects old URLs,
  so cross-links degrade gracefully in the meantime.
- `st-zephyr` → `nodus-st`: folded into sub-project D, which already renovates that repo
  to node-only.

This spec uses the target names throughout; references to the current `nodus` /
`st-zephyr` names remain valid until each rename lands.

Key cross-cutting decisions (locked during brainstorming):

- The Python ST→bytecode compile service (`st-zephyr/transpiler/`) **moves into porta
  `/tools/smalltalk`** — porta owns server-side language tooling.
- **Symmetric compile-and-deliver**: the Go server invokes jag/SDK to build/relocate
  Toit images *and* runs the ST transpiler. This takes on the SDK/chip-coupling risk
  (an image is relocated per chip and must match the device firmware's SDK version), so
  the design that adds Toit build (sub-project C) must pin/match SDK version per node.
- The parked Toit gateway is eventually a **living example** (telemetry-ingest slice,
  buildable + host-tested in CI) so it keeps dogfooding the Toit `sqlite`/`tftp` libs —
  but that trim is deferred (see below).

The renovation decomposes into four independently spec'd sub-projects:

- **A. Repo restructure (THIS SPEC)** — Go server → mainline at repo root; Toit gateway
  parked as a still-deployable example; docs/CI updated.
- **B. Go protocol parity** — bring the Go server up to current `docs/PROTOCOL.md`
  (new TFTP delivery: raw image + `size`/CRC32 in the `run` command; typed telemetry
  up-path; config down-path `device set`→NVS; observed-config echo; config self-heal),
  prove it serving live **nodus-toit** end-to-end, and refresh MCP + htmx for Toit nodes.
  *Critical path — the actual reason this is worth doing.*
- **C. Language tooling** — `/tools/smalltalk` (move transpiler in) + `/tools/toit`
  (jag/SDK build), wired into the symmetric compile-and-deliver flow, with per-node SDK
  matching.
- **D. st-zephyr → node-only (`nodus-st`)** — strip server/tooling/transpiler; leave
  the Smalltalk/berry node, and rename the repo `st-zephyr` → `nodus-st`.

Each gets its own spec → plan → implementation cycle. **This spec covers A only.**

## A — Goal

Establish porta's new shape — the Go server as the mainline at repo root, the Toit
gateway parked as a still-deployable example — **without changing any server
behavior**. Pure restructure. The live gw85224-01 config-self-heal soak (node
`jolly-pine` → `:6969`) keeps running throughout; the Toit gateway stays deployable.

## A — Changes

### 1. Move the Go server to repo root (idiomatic Go layout)

- `gateway-go/{cli, debug, debugui, gateway, helpers, mcpserver, store, tftp}` → `internal/`.
- `gateway-go/main.go` → `cmd/porta/main.go`.
- `go.mod`/`go.sum` → repo root; module path `github.com/davidg238/jast-gw` →
  **`github.com/davidg238/porta`**.
- Rewrite the 8 import prefixes (`davidg238/jast-gw/<pkg>` → `davidg238/porta/internal/<pkg>`)
  across the 12 referencing files (mechanical).
- Resulting repo root: `cmd/ internal/ docs/ examples/ deploy/ go.mod go.sum README.md CLAUDE.md`
  (plus `tools/` later in C).
- **Acceptance:** `go build ./...` and `go test ./...` green from repo root.

### 2. Park the Toit gateway → `examples/toit-gateway`

- `gateway/` (whole thing, intact and deployable) → `examples/toit-gateway/`.
- Update `deploy/build-kit.sh`: the compile path `$REPO/gateway` → `$REPO/examples/toit-gateway`.
  Nothing else in `deploy/` changes; the running container on gw85224-01 is untouched.
- **Acceptance:** `deploy/build-kit.sh` produces a working `gateway.snapshot` from the new
  path; the Toit host test suite passes from `examples/toit-gateway`.

### 3. Clean root + housekeeping

- `rm` the untracked build artifacts at repo root (`*.bin`, `*.snapshot`, `*.image`,
  `*.envelope` — already gitignored; cosmetic local cleanup).
- Update `CLAUDE.md` Layout section: Go server is the mainline at repo root
  (`cmd/porta`, `internal/`); the Toit gateway now lives at `examples/toit-gateway`;
  note `/tools/{toit,smalltalk}` are *planned* (sub-project C), not present yet.
- Update `README.md` to match.

### 4. Minimal CI (net-new — none exists today)

- Add `.github/workflows/ci.yml`:
  - Go job: `go build ./...` + `go test ./...` from repo root.
  - Toit job: `toit test` (or the project's host-test invocation) over
    `examples/toit-gateway`, so the parked example stays honestly buildable.

## A — Explicitly NOT in scope (deferred)

- Telemetry-slice **trim** of `examples/toit-gateway` (it moves intact and deployable
  now; the trim to a pure telemetry-ingest living example happens in a later step).
- Go protocol parity (B), `/tools/` language tooling (C), st-zephyr strip (D),
  MCP/htmx refresh (part of B).

## A — Risks & rollback

Lowest-risk slice in the renovation: no behavior change, the live deployment is
untouched, and every change is `git mv` + import-path sed + a `build-kit.sh` one-liner —
fully reversible. The only failure mode is a broken build, caught directly by the
step 1 and step 2 acceptance checks.
