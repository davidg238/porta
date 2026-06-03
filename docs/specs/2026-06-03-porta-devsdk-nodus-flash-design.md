# porta dev-SDK + `nodus flash` toolchain — design

Date: 2026-06-03
Status: approved (brainstorm)
Supersedes the "Phase 2" sketch in the `/tools/toit` roadmap; absorbs sub-project C
("deepen toit/jag integration") and Phase 2 (`porta flash`) into one design spanning
**two repos** (porta + nodus).

## 1. Problem & framing

The language-aware functions a developer needs — **compile, relocate, and first-time
serial flash + provisioning** — physically cannot run on the porta gateway: they need
the source repos, the toolchain (`toit`/`jag`), and a USB cable to the device. A
headless `porta serve` (on a dev box or gw85224-01) has none of these. So those
functions belong on the **dev machine**, in a tool that is a *peer to `jag`*.

This sub-project moves the language toolchain out of the neutral gateway and gives it a
home in the node project, and adds the missing **primordial duty**: flashing a blank
ESP32 over serial USB for the first time and provisioning it (WiFi + gateway address)
so it can subsequently reach porta over the wire.

### Organizing principle

> **porta owns the *neutral contracts*. Each node repo owns *both ends of its
> language* — the firmware (southbound, conforms to the wire) and the dev tool
> (northbound, built on porta's dev-SDK). Dependencies point one way: node repos
> import porta; porta imports nothing from any node.**

```
        porta repo  ──────────────────────────────────┐
        (neutral: gw server + dev-SDK lib + contracts) │  imported by ▲
                                                        │  every node  │
   ┌────────────────────────┬───────────────────────────┘   repo's    │
   │ imports porta/devsdk    │ imports porta/devsdk             tool    │
   ▼                         ▼                                          │
 nodus repo               nodus-st repo (future)                        │
 (Toit firmware           (Smalltalk firmware                          │
  + toit dev-tool)         + ST dev-tool)  ──── both conform to ───────┘
                                                porta/docs/PROTOCOL.md
```

## 2. System architecture

```
  DEV MACHINE (bench)                                          LAN
 ┌───────────────────────────────────────────────┐
 │  jag (Toitware)                                 │
 │  ┌──────────────┐      ┌──────────────────┐    │     ┌──────────────────────────────┐
 │  │ nodus tool   │      │ nodus-st tool    │    │ HTTP│  porta  (Go server, NEUTRAL) │
 │  │ uses toit/jag│      │ uses ST transpiler│   │ API │   node-type INDEPENDENT      │
 │  │ Xtensa image │      │ ARM image        │    │◄───►│   ├── control-plane API      │
 │  └──────┬───────┘      └────────┬─────────┘    │     │   ├── command queue          │
 │         └──── imports ──────────┘              │     │   ├── store (SQLite)         │
 │           porta/devsdk (Go lib)               │     │   ├── TFTP image delivery    │
 │         (apiclient · opverbs · provision ·    │     │   ├── telemetry ingest       │
 │          flash orch · narrate · exec)         │     │   ├── MCP /mcp · htmx web UI │
 │  local repos: ~/nodus  ~/nodus-st (fut.)      │     │   └── owns PROTOCOL.md        │
 └───────┬───────────────────────┬───────────────┘     └───────────────┬──────────────┘
         │ serial USB (esptool)   │ DFU / J-Link                        │ UDP report/cmd
         │ first-time flash       │ first-time flash                    │ + TFTP image
         ▼ + provision            ▼ + provision                         ▼  (the wire)
   ┌──────────────┐         ┌──────────────┐               ┌────────────────────────────────┐
   │ ESP32 node   │  after  │ nRF52840 node│  after        │  heterogeneous fleet (the wire)│
   │ (Toit/nodus) │ ──────► │ (Smalltalk)  │ ───────────►  │  ├── ESP32 (Toit/nodus)        │
   └──────────────┘  flash  └──────────────┘  flash, wire  │  └── nRF52840 (Smalltalk)      │
                                                            └────────────────────────────────┘
```

The porta server stays **language-blind**: command queue, store, TFTP delivery,
telemetry ingest, API/MCP/UI. The `kind` column is the only node-type awareness and is
shown as a label, never branched on (B4 decision, preserved).

### gw priorities drive the extension model

The gateway's two priorities are **(1) always on** and **(2) LAN fleet management**.
"Always on" is decisive: any language-specific gw function therefore arrives as a
**sidecar process** (the MCP pattern), *never* compiled into the gw binary — so adding
or updating a language never rebuilds or restarts `porta serve`. Sidecars are **owned and
shipped by the node repos** (`nodus/ext`, `nodus-st/ext`), consistent with each node repo
owning both ends of its language. See §6.

## 3. Repo layouts

### 3.1 porta (neutral gateway + dev-SDK)

```
porta/                              module: github.com/davidg238/porta
├── cmd/porta/                      the NEUTRAL gw server binary (`porta serve`)
├── internal/                       gw-private (unchanged): store, command, handler, tftp,
│                                   apisrv, httpsrv, mcpsrv, web, control, telemetry, config
├── devsdk/                         NEW, PUBLIC (importable by node repos):
│   ├── apiclient/                  HTTP control-plane client (promoted from internal/apiclient)
│   ├── opverbs/                    neutral verbs: list · log · monitor (composable cobra cmds)
│   ├── provision/                  WiFi + gateway-addr firmware.config injection
│   ├── flash/                      flash ORCHESTRATION (transport-agnostic interface + jag wrap)
│   ├── narrate/                    apt-style narration (default tidy summary, -v transcript)
│   └── exec/                       Runner iface + ExecRunner (promoted from internal/toolchain)
├── docs/PROTOCOL.md                southbound contract (the wire)
└── docs/DEVSDK.md                  NEW: northbound contract (HTTP API shape + dev-SDK surface)
```

Carve rule: the **reusable** half of today's `internal/toolchain` + `internal/apiclient`
is *promoted* to public `devsdk/`. The **language-specific** half (Toit compile/relocate,
the `run` and `flash` verbs) leaves porta entirely (moves to nodus in C2/C3).

`internal/apiclient` consumers inside porta (the operator CLI) re-point to `devsdk/apiclient`.
The neutral operator verbs (`device list`, `log`, `monitor`) become `devsdk/opverbs` so any
frontend — including the `porta` binary's own client subcommands and the `nodus` tool —
gets them from one place.

### 3.2 nodus (Toit firmware + its dev-tool)

```
nodus/                              module: github.com/davidg238/nodus
├── src/                            Toit firmware: supervisor, report, config_store (the NODE)
├── examples/  host/                envelope recipe, SDK_VERSION (already present)
├── tool/   (Go)                    NEW: the Toit dev-CLI — imports porta/devsdk
│   ├── main.go                     frontend = neutral verbs (devsdk/opverbs) + Toit verbs
│   ├── build/                      toit compile + snapshot-to-image -m32 (Xtensa)
│   ├── flash/                      jag-flash wrap + envelope assembly + provision
│   └── run.go  flash.go            the language verbs
└── ext/    (Go, FUTURE)            kind="toit" gw sidecar (per-kind UI/decode), §6 — not built here
```

`nodus/tool` imports `porta/devsdk`. Firmware conforms to `porta/docs/PROTOCOL.md` by
spec, not by code. **porta never imports nodus.**

The binary is named after the node project: **`nodus run …`, `nodus flash …`.** This
resolves the naming question — the tool *is* its node project's namesake command, so it
need not be `porta-cli` (not neutral) nor contain "toit" (not owned).

### 3.3 nodus-st (future, identical pattern)

```
nodus-st/                           module: github.com/davidg238/nodus-st
├── src/                            Smalltalk firmware for nRF52840 (the NODE)
├── tool/   (Go)                    ST dev-CLI — imports porta/devsdk
│   ├── build/                      ST transpiler → ARM image
│   └── flash/                      DFU / J-Link flash + provision
└── ext/    (Go, FUTURE)            kind="smalltalk" gw sidecar (§6) — not built here
```

Out of scope for this sub-project — designed for, not built. The `devsdk` seam is
validated by having *one* real consumer (nodus); nodus-st is the documented second
consumer that proves the seam is language-neutral.

## 4. Phase plan (3 phases, 2 repos)

### Phase C1 — porta: carve out `devsdk/`  *(pure refactor + new public surface)*
- Promote `internal/apiclient` → `devsdk/apiclient`; promote reusable `internal/toolchain`
  bits (`exec`, narration) → `devsdk/exec`, `devsdk/narrate`.
- Add `devsdk/provision` (config-injection helpers) and `devsdk/flash` (transport-agnostic
  orchestration interface + the `jag flash` wrapper) as new scaffolding.
- Add `devsdk/opverbs` (neutral `list`/`log`/`monitor`) and re-point porta's own client.
- Write `docs/DEVSDK.md`.
- **Acceptance:** zero behavior change; all existing porta tests green; `porta serve`,
  `porta run` (still present until C2), `device …`, `log`, `monitor` all work as before.

### Phase C2 — nodus: birth the `nodus` tool, migrate `run`  *(parity move)*
- Create `nodus/tool` (Go) importing `porta/devsdk`.
- Move Toit-specific build/relocate + the `run` verb here → `nodus run app.toit -d <node>`.
- Wire neutral verbs from `devsdk/opverbs`.
- Remove `porta run` + the language bits from porta.
- Fold in the Phase-1 review nits: live `-v` streaming (Runner wires `cmd.Stdout/Stderr`
  when verbose), validate deploy opts (lifecycle/trigger syntax) *before* the multi-second
  compile.
- **Acceptance:** `nodus run` deploys exactly as `porta run` did (HW parity: compile +
  relocate + TFTP deliver + queue run); porta no longer references toit/jag.

### Phase C3 — nodus: `nodus flash` + firmware companion  *(the new capability)*
- **`nodus flash`** (purely local, no porta API calls):
  `nodus flash --port /dev/ttyUSB0 --chip esp32 --ssid <s> --psk <p> [--gateway host:port] [--sdk <v>] [-v]`
  1. resolve SDK (default = local `toit version`) and chip;
  2. fetch + cache the matching envelope from `toitlang/envelopes` keyed by `(chip, sdk)`;
  3. assemble the nodus boot-container envelope (compile supervisor → `-m32` image →
     `toit tool firmware container install`);
  4. inject `firmware.config`: WiFi creds + nested `porta.gateway` key (**injection
     mechanism = first-task spike**, see §7);
  5. flash over serial via wrapped `jag flash <envelope> --exclude-jaguar --port … [--wifi-*]`.
- **nodus firmware companion (Toit):** replace the hardcoded `GATEWAY-HOST`
  (`src/supervisor.toit:26`) with a `firmware.config["porta"]` read, falling back to the
  constant when the key is absent (so `jag run` / bench dev still works).
- **Acceptance (HW):** a blank ESP32 flashed with `nodus flash`, given only `--ssid/--psk
  /--gateway`, boots, joins WiFi, reaches porta at the provisioned address, and reports —
  appearing in `porta` / `nodus` `device list` with chip/sdk populated from its report.

## 5. `nodus flash` design detail

- **Approach: wrap, don't reimplement.** Consistent with the locked Approach-A decision,
  `nodus flash` orchestrates `jag flash` + `toit tool firmware` and narrates each step
  (apt-style tidy summary by default, full transcript under `-v`). It is not an esptool
  reimplementation. `host/build-envelope.sh` already proves the envelope recipe and the
  `jag flash <envelope> --exclude-jaguar --wifi-ssid … --port …` invocation.
- **SDK / envelope management (the "C" groundwork):** generalize the hardcoded
  `build-envelope.sh` download into a cache keyed by `(chip, sdk)` under the jaguar/toit
  cache dir; default SDK = local `toit version`; default chip = `esp32`. Re-download only
  on cache miss.
- **Chip selection:** `--chip {esp32|esp32s3|esp32c3|…}` selects which
  `firmware-<chip>.envelope` to fetch. All current ESP32 targets are 32-bit
  (`snapshot-to-image -m32`), confirmed empirically in `host/build-envelope.sh`.
- **Provisioning channel:** WiFi + gateway address both ride `firmware.config`. WiFi may be
  set via jag's existing `--wifi-ssid/--wifi-password`; the **nested `porta.gateway` key**
  is the new bit and shares the same JSON config channel (the `--config` round-trip noted
  in the bench-provision pivot).
- **No identity seeding:** `nodus flash` makes **no** porta API call. Node identity
  (chip/sdk) appears when the node first boots and reports; `nodus run` correctly blocks on
  "unknown identity" until that first check-in (accepted bootstrap wait). This keeps flash
  purely local and offline-capable.

## 6. gw extensions — sidecar model (seam defined, not built here)

### 6.0 Three homes — what is language-specific, and where it lives

"Language-specific gw function" is a **near-empty set by design**. Toit *verbs* are not gw
verbs at all. There are three distinct homes; only the third is ever a sidecar:

| Function | Home | Sidecar? |
|---|---|---|
| `run`, `flash`, compile, relocate, provision, **panic decode** | **dev machine** — the `nodus` CLI tool (built on `devsdk`) | No — a dev-machine binary, not a gw thing |
| command queue, store, TFTP delivery, telemetry ingest, API/MCP/htmx, the **wire command vocabulary** | **gw core** (`porta serve`) — neutral | No — none of it is language-specific |
| `kind`-aware **presentation/decode** in the gw UI (render a Toit panel/panic vs a Smalltalk one) | **gw sidecar** (`nodus/ext`, future) | Yes — *only this* |

Two clarifications this table makes explicit:

1. **"Toit-specific verbs" are not gw verbs.** The wire/command vocabulary in
   `PROTOCOL.md` is language-neutral — `run` carries image + size + CRC and the gw never
   cares that the image is Toit bytecode; a Smalltalk node honors the same verbs. The Toit
   verbs (`nodus run`, `nodus flash`) are **dev-machine CLI verbs in the `nodus` tool**;
   they never touch a sidecar.
2. **The gw is neutral by *physics*.** Compile/flash need source + toolchain + a USB cable
   (dev machine); panic decode needs the snapshot + `jag` (dev machine, S6). What is left
   that is both gw-resident *and* language-specific is only **presentation**.

### 6.1 Achieved end-state: a language-neutral LAN node manager

With the toolchain moved out, **the gw core implements zero language-specific function.**
porta is a language-neutral LAN node manager whose single shared fixed point is the neutral
`docs/PROTOCOL.md`, which heterogeneous nodes conform to. All language/hardware specifics
live in `nodus` / `nodus-st`, each depending on `porta/devsdk`. As of this design, **no
language-specific gw function is identified or desired** — the plain `kind` label suffices.

The sidecar below is therefore a **hypothetical escape hatch**, documented so that *if*
some kind-aware presentation ever surfaces there is a known, neutrality-preserving way to
add it — not a planned feature of this or any current sub-project.

**Chosen model — sidecar.** Driven by the gw's "always on" priority (§2): when a
language-specific gw function *is* needed, it ships as a **separate sidecar process**
(the MCP pattern), deployed beside `porta serve`, never compiled into the gw binary.
Rationale: a sidecar attaches/detaches and updates **without rebuilding or restarting the
always-on gw**; a composed-binary or compile-time-registry model would force a gw redeploy
per language, which fights priority (1). The data-driven and composed-binary alternatives
are explicitly **rejected** for this reason.

- **Ownership:** each sidecar lives in its node repo (`nodus/ext`, `nodus-st/ext`) and is
  installed/deployed from there — the node project owns its gw-side presence, same as it
  owns its firmware and dev tool.
- **Seam:** the sidecar interacts over a documented **local** interface, mirroring how
  porta already exposes/consumes MCP. Concrete shape (porta calls sidecar vs sidecar
  embeds via porta's API/MCP) is deferred to whenever the first real sidecar is specced.

**Decision: build no sidecar now.** Keep showing `kind` as a label; record the sidecar
contract intent in `DEVSDK.md`. This sub-project defines the *seam and the model*, not an
implementation — keeping the door open without scope.

## 7. Open spikes (resolve during implementation, not blocking the design)

1. **`firmware.config["porta"]` injection mechanism** — `toit tool firmware … config set`
   on the assembled envelope vs a `jag flash --config` flag. First task of C3; verify the
   round-trip (set → flash → `firmware.config` read on device).
2. **Toit `firmware.config` read API** in the supervisor companion — exact call to read the
   nested `porta.gateway` key, with constant fallback. Use the Toit skills.
3. **`SDKVersion` stderr pollution** — `toit version` parsing watched at HW smoke (carried
   from Phase-1 review nits).

## 8. Out of scope / deferred

- nodus-st tool and Smalltalk/nRF52840 path (designed-for, not built).
- gw extension implementations (seam only — §6).
- Containerizing `porta serve` for gw deploy (next roadmap item, separate brainstorm).
- `porta flash`-style chip auto-detect, watch/dev-loop, OTA `set-gateway` (bench-provision
  pivot keeps gw addr a flash-time value).

## 9. Cross-repo coordination

This sub-project lands changes in **both** porta and nodus. Suggested order: C1 (porta,
merge) → C2 (nodus tool born, porta loses `run` — coordinate so neither repo is broken at
a tag) → C3 (nodus flash + firmware companion). Each phase gets its own implementation plan.
