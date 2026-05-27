# Splitting porta into `porta` (gateway) + `nodus` (node) — design

**Status:** approved design (2026-05-26). Decision-complete; ready for an implementation plan.

## Context

`porta` today is one repo holding three things that share **no code**:

- the **gateway** — `gateway/` (Toit control plane + sqlite) and `gateway-go/` (the Go
  gateway), plus the `deploy/` kit (Dockerfile + systemd) that runs it live on
  `gw85224-01`;
- the **node** — `device/` (the on-device loader/supervisor "keeper" + node services),
  the `host/` firmware tooling (`build-envelope.sh`, `capture_sink.go`, `SDK_VERSION`),
  and roughly half of `docs/`;
- **examples** — `chatty`, `control_demo`, `hello`, `vin` payloads currently cluttering
  `device/` alongside the supervisor infra.

The gateway and the node each implement **their own side** of a wire protocol
(`gateway/command.toit` encodes; `device/node_command.toit` decodes; `report.toit`,
the TFTP delivery blob `[u32 size_le][u32 crc32_le][image]`). They are coupled **only by
that implicit contract** — they never import each other's code; they talk over UDP/TFTP.

That makes them two genuinely independent projects with different toolchains and
lifecycles (server-side Go+Toit / Docker vs. embedded Toit firmware + envelope builds),
which today are tangled in one tree.

Immediate trigger: the run-once lifecycle work surfaced that `device/` mixes L1
supervisor infrastructure with L2 example payloads, and the user wants `device/` focused.
That cleanup is the same job as separating the node project from the gateway project.

## Goal

Split `porta` into **two repos that talk only over the wire**:

- **`porta`** — the gateway project (keeps `gateway/`, `gateway-go/`, `deploy/`, the
  gateway-side docs, and the canonical wire-protocol doc).
- **`nodus`** — the node project (the node OS/supervisor + services, restructured as a
  proper Toit package, plus examples, `host/` tooling, and node-side docs).

Name rationale: `porta` is Latin for "gate/door"; `nodus` is Latin for "knot/node" — two
Latin-named projects facing each other across the wire.

Preserve the hardware-verified run-once **lifecycle framework** (it spans both sides) by
landing it on `master` *before* any carving.

## The split: what goes where

**`porta` (gateway repo) keeps**
- `gateway/` (Toit gateway package — already a clean package: `name: gateway`)
- `gateway-go/` (Go gateway)
- `deploy/` (Dockerfile, `porta-gw.service`)
- gateway-side `docs/`: `porta-toit-gateway-design`, `config-self-heal-*`,
  `d5-observed-config-echo-*`, `m2-telemetry-*`, `m2-2-down-path-config`,
  `tftp-engine-transport-handler-split-*`, `gateway-b1-*`, `gateway-b2-*`,
  `security-transport-evolution-design`
- the **canonical wire-protocol doc** (`docs/PROTOCOL.md` — see "Wire contract" below)
- a rewritten `CLAUDE.md` and `README.md` scoped to the gateway

**`nodus` (new repo) gets** (everything node-side, consolidated under `nodus/` in Phase 1)
- the node infra (today's `device/`), restructured as the `nodus` package
- the example payloads
- `host/` (`build-envelope.sh`, `capture_sink.go`, `SDK_VERSION`)
- node-side `docs/`: `toit-tftp-loader-design`, `porta-no-jaguar-supervisor-*`,
  `node-lifecycle-reliability-design`, `toit-tftp-loader-smoke-test` plan,
  `vin-run-once-lifecycle` plan
- a new `CLAUDE.md` and `README.md` scoped to the node

## Staging

The split is staged so the boundary is **proven before any git-history surgery**.

### Phase 0 — land the lifecycle framework

`--no-ff` merge `feat/vin-run-once-lifecycle` → `master`. The framework (gateway
`--lifecycle` flag + the device-side lifecycle plumbing + supervisor `wait`-with-cap) is
host-green and hardware-verified (A1 wait-on-run-once, A2 graceful cap). Merging first
means each side's framework code is on `master` in its rightful place before carving.

### Phase 1 — clean boundary, in-repo

Restructure `device/` → `nodus/` as a **canonical Toit package**, consolidating *all*
node-side material under `nodus/` so the eventual extraction is a single subtree:

```
porta/
├── nodus/                      ← becomes the nodus repo root at extraction
│   ├── package.yaml            (name: nodus; dep: tftp)
│   ├── package.lock
│   ├── src/                    L1 infra + service modules:
│   │                            supervisor, transport, goal_state, inventory,
│   │                            node_command, node_id, report, triggers,
│   │                            image_writer, flash_image, schedule_store, config_store,
│   │                            control_service, telemetry_service, telemetry_buffer,
│   │                            telemetry_codec
│   ├── tests/                  *_test.toit + package.yaml (path: ..)
│   ├── examples/
│   │   ├── vindriktning/        vin.toit + vindriktning.toit + olympic.toit
│   │   │                        + olympic_test.toit + package.yaml (path: ../..)
│   │   ├── chatty/              chatty.toit + package.yaml (path: ../..)
│   │   ├── control-demo/        control_demo.toit + package.yaml (path: ../..)
│   │   └── hello/               hello.toit (+ package.yaml only if it grows a dep)
│   ├── host/                   build-envelope.sh, capture_sink.go, SDK_VERSION
│   ├── docs/                   node-side specs/plans
│   ├── README.md
│   └── CLAUDE.md
├── gateway/  gateway-go/  deploy/  docs/(gateway-side)   ← stays = porta
```

Renaming `device/` → `nodus/` in this phase gives the package its identity early and
makes the Phase 2 extraction prefix `nodus`.

**Import mechanics** (per toit-package + toitlang/toit discussion #1133):
- Library modules live in `nodus/src/` and keep internal **relative** imports
  (`import .goal_state show ...`).
- Tests live in `nodus/tests/`, register the local package with
  `toit pkg install --local ..`, and import by **package name**:
  `import nodus.goal_state show ...` (~15 mechanical `import .X` → `import nodus.X` edits).
- Examples are **per-example sub-packages** under `nodus/examples/<name>/`, each with its
  own `package.yaml` declaring `dependencies: { nodus: { path: ../.. } }`, and import the
  device's southbound clients by package name:
  `import nodus.telemetry_service show TelemetryServiceClient` /
  `import nodus.control_service show ControlServiceClient`.
- `vin` keeps its **vendored** `vindriktning.toit` driver and `olympic.toit` helper inside
  `examples/vindriktning/` — these are vin-specific, not part of the node library API.

**Seams to fix in Phase 1:**
- `host/build-envelope.sh` compiles `nodus/src/supervisor.toit` (was `device/supervisor.toit`).
- Example `.bin`/`.snapshot` build commands and output locations (the per-example dirs).
- `.gitignore` artifact paths (the explicit `/chatty.bin`, `/vin.bin`, etc. rules → new
  per-example locations; envelope/supervisor artifacts unchanged at repo root or moved).
- The Toit host-test runner glob (now `nodus/tests` + `nodus/examples/*` instead of
  `device`).
- `CLAUDE.md` references.

**Verification gate (the boundary must demonstrably hold):**
- every `nodus` host suite green;
- every example `.bin` builds;
- the supervisor envelope builds (`host/build-envelope.sh`);
- the gateway suites still green;
- **zero imports cross the gateway↔nodus line** (grep verification).

### Phase 2 — extract `nodus`

- `git subtree split --prefix=nodus -b nodus-export` → a branch carrying `nodus/` with its
  history.
- Create the `nodus` GitHub repo (private, mirroring porta), push `nodus-export` as its
  default branch.
- Remove `nodus/` from `porta`; commit. porta is now gateway-only.
- In `nodus`: set up CI, pin the SDK version, and fix the machine-specific `tftp` path
  note in `package.yaml` (mirrors porta's existing path-dep caveat).
- Update both `CLAUDE.md`s and the auto-memory to reflect two repos.

## Wire contract

The command/report/blob protocol is the **only** thing spanning the projects after the
split, so drift is the main risk (change a field in `porta`, forget `nodus`).

Crucially, **`nodus` is only one node implementation.** The gateway is intended to control
*heterogeneous* nodes — e.g. planned Smalltalk-based nodes — that must speak the **same**
wire protocol. The protocol is therefore implementation-agnostic and outlives any single
node codebase, which is the decisive reason the gateway owns it: it is the one fixed point
every node implementation conforms to.

**Decision:** a canonical `docs/PROTOCOL.md` lives in **`porta`** — the gateway is the
northbound controller/authority that defines the command vocabulary and report schema for
*all* node implementations. `nodus` (and future nodes, e.g. Smalltalk) reference it by URL. It documents: command verbs + their JSON args (incl. the
`lifecycle` field just added), the report/observed-state shape, and the TFTP delivery blob
header (`[u32 size_le][u32 crc32_le][image bytes]`, CRC32-IEEE). It is documentation, not
enforcement; pre-1.0 that is an acceptable guard given the [[porta-no-legacy]] stance.

## Non-goals (scope discipline)

- **Not** restructuring `gateway/` into `src/`/`tests/`. Nothing path-depends on it, so it
  stays flat (optional future tidy; out of scope here).
- **Not** the always-on / `run-loop` milestone for vin. That is a separate brainstorm;
  vin ships here as the existing `boot × run-once` example (acknowledged imperfect on the
  bursty real PM1006 sensor — that is exactly what the always-on work will address).
- **Not** renaming `_test.toit` → the skill's `-test.toit`. Keep the established underscore
  convention to avoid churn across ~15 files.

## Risks & mitigations

- **Extraction loses history / breaks the gateway deploy.** Mitigated by Phase 1 proving
  the boundary in-repo first, and by `git subtree split` (history-preserving). The live
  gateway on `gw85224-01` is unaffected — its code (`gateway/`, `gateway-go/`, `deploy/`)
  never moves.
- **Path-dependency churn.** Examples + tests gain `package.yaml`/`package.lock` and
  machine-specific `path:` deps (same caveat porta already documents). The verification
  gate catches resolution failures.
- **Wire-contract drift across repos.** Mitigated (not eliminated) by the canonical
  `PROTOCOL.md`; full mitigation deferred to a later contract-test effort if it bites.

## Open items

None blocking. The always-on-vin milestone and any gateway-side `src/` tidy are tracked
separately.
</content>
</invoke>
