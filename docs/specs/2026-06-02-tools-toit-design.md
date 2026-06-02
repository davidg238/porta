# `/tools/toit` — trainer-wheels toolchain for porta nodes

**Status:** approved design (2026-06-02). Supersedes the manual "bring-your-own
`.bin`" workflow for delivering payloads, and folds in the flash-time
gateway-address provisioning ([[porta-gw-discovery-backlog]] bench-provision
pivot).

## 1. Purpose

Today, getting code onto a porta node is a multi-step manual chore: you compile
and relocate a container image with raw Toit tooling, then hand the resulting
`.bin` to `porta container install <name> <file.bin>` (which literally rejects
anything that is not a prebuilt `.bin` — `internal/portacli/mutate.go:28`). The
compile→relocate step, the SDK/chip matching, and the firmware provisioning are
all on the operator.

`/tools/toit` closes that gap by wrapping the existing Toitware tools (`toit`,
`toit tool firmware`, `toit tool snapshot-to-image`, `jag`) in a guided,
**transparent** layer — "trainer wheels," not a black box. It narrates every
underlying command so the operator learns the real toolchain (the apt-installer
"expand for details" model), helps choose SDK version and target chip, warns of
conflicts, and pulls the right prebuilt firmware envelopes from
[`toitlang/envelopes`](https://github.com/toitlang/envelopes).

**Non-goals.** Reimplementing the Toit compiler/relocator (we shell out and stay
compatible with standard Toit/Artemis artifacts — envelopes, snapshots,
relocated images). Depending on Artemis. A watch/dev-loop (`jag watch` analog) —
explicitly deferred. The zero-touch broadcast-discovery design — parked
([[porta-gw-discovery-backlog]]).

## 2. Surface

Two new `porta` CLI verbs, engine logic in a new `internal/toolchain` package
(aligning with the planned `/tools/toit`); the cobra commands stay thin.

- **`porta run <app.toit> -d <node>`** — the `jag run` analog: compile →
  relocate for the node's chip/SDK → upload payload → prompt for how to run it →
  enqueue `run`. **Phase 1.**
- **`porta flash -d <node>`** — provision: build the nodus envelope for a chosen
  chip/SDK from `toitlang/envelopes`, inject `firmware.config["porta"]`
  (gateway address) + WiFi, flash the device, seed its identity. **Phase 2.**

The existing **`container install <name> <file.bin>`** stays unchanged as the
low-level escape hatch for an already-relocated image.

## 3. The narration engine (`internal/toolchain`)

Every external invocation goes through one `Executor`:

1. **Announce** a human-readable step label *and the exact argv*
   (`→ toit compile --snapshot -o /tmp/app.snapshot app.toit`).
2. **Run** the child process.
3. On success print `✓ <step> (<timing>)`; on failure print `✗ <step>`, the
   captured stderr, **and the exact command to rerun by hand**. With `-v` /
   `--verbose`, stream the child's stdout/stderr live instead of capturing.

Each step is a structured record `{label, argv, output, status, duration}`. The
default tidy summary and the `-v` full transcript are two renderings of the same
records, so a future interactive TUI pane (apt-style expandable detail) renders
the identical data with no engine change.

The `Executor` takes an **injectable command runner** (an interface, real
implementation = `os/exec`); tests substitute a fake runner.

## 4. `porta run` flow

1. Resolve `-d` → node row. Read its **chip + SDK** (§6).
2. **Identity guard.** Unknown identity → *block* with guidance ("node aabb…
   hasn't reported its firmware identity yet — wait for a check-in or flash it
   via `porta flash`"). Known → build target = the node's chip/SDK.
3. **Conflict guard.** A `--chip` / `--sdk` override that disagrees with the
   node's reported identity → refuse with the mismatch spelled out (§5).
4. Ensure the `(chip, SDK)` firmware envelope is cached; otherwise **fetch it
   from `toitlang/envelopes`** (narrated download) into a local cache, mirroring
   how jag caches its envelope (`~/.cache/jaguar/<sdk>/envelopes/`).
5. `toit compile --snapshot -o <tmp>/app.snapshot app.toit` (narrated).
6. Relocate the snapshot into a container image (`.bin`) for the node's chip via
   `toit tool snapshot-to-image` (exact flags pinned during implementation —
   this is the step the operator does by hand today). The relocation is bound to
   the node's chip word-size and SDK so the image matches the device firmware.
7. Compute CRC32 (existing `command.CRC32`).
8. **Prompt** for lifecycle (`run-once` / `run-loop`) and triggers
   (`boot` / `gpio-*` / interval). **Flags** supply `--name` (default = file
   stem), `--runlevel`, `--power-mode`, `--args`. Any prompt answerable by a
   flag may be supplied non-interactively (scriptable).
9. Existing `control.Install` uploads the payload and enqueues `run`; if
   `--power-mode` is set, enqueue `set-power-mode` too. Node fetches on next
   poll.

## 5. Conflict guardrails

| Condition | Action |
|-----------|--------|
| chip override ≠ node's reported chip | **refuse** — image will not boot |
| envelope/SDK ≠ node's reported SDK | **refuse** — SDK coupling, image will not run |
| node identity unknown (never reported, never seeded) | **block** with guidance |
| node offline / asleep (stale last-seen) | **proceed** (queue is durable) but inform the operator the node will pick it up on next wake |

The two **refuse** cases are overridable with an explicit `--force` (for the
advanced operator who knows the relocation is compatible); the default is to
stop. The SDK/chip-match check is the headline guardrail — it turns the project's
known #1 risk (per-chip relocation must match device firmware SDK) into a
checked precondition instead of a silent boot failure.

## 6. Node identity (the one protocol change)

The node is the TFTP **client**; the gateway only ever responds and cannot
initiate a request to a node. So identity is **pushed, not pulled**: it rides on
the `report` the node already sends every cycle.

- **Wire (additive):** the nodus `report` body gains `chip` (e.g. `"esp32"`,
  `"esp32c6"`, `"esp32s3"`) and `firmware.sdk` (e.g. `"v2.0.0-alpha.192"`). Both
  are readable on-device at runtime. Documented in `docs/PROTOCOL.md`.
- **Store:** new `nodes` columns `chip TEXT`, `sdk TEXT`; report ingestion writes
  them (self-healing — corrects automatically if a device is reflashed).
- **Seed:** `porta flash` (Phase 2) writes chip/SDK onto the `nodes` row at flash
  time (it inherently knows them — the operator picked the envelope), so a
  porta-flashed node has identity from minute zero rather than after first
  report.
- Identity is **eventually-consistent**: for a node flashed outside porta, it
  arrives on the first report (~one poll interval), during which `porta run`
  blocks rather than guess.

**Why no "raw jag" path exists:** plain `jag flash` yields a Jaguar dev device
that knows nothing of porta's command vocabulary, run-once/run-loop lifecycle,
report schema, or `config_store` — all of which live in the **nodus supervisor
firmware**. Being a porta node *requires* the nodus envelope, and nodus always
reports its identity. `porta flash` is the blessed wrapper that builds that
envelope and seeds identity early, but the artifacts are standard Toit, so a
hand-rolled nodus flash still produces a working node (identity then arrives via
report). Running nodus is the only path; porta-flash is not lock-in.

## 7. `porta flash` provision (Phase 2)

Folds in the bench-provision decision ([[porta-gw-discovery-backlog]]). Builds on
the same envelope cache and narration engine.

1. Choose target chip + SDK (prompted with the available `toitlang/envelopes`
   releases; trainer-wheels guidance and conflict warnings).
2. Fetch/cache the base `firmware-<chip>.envelope`.
3. `toit tool firmware container install --trigger boot supervisor
   <nodus.snapshot>` to add the nodus supervisor as the boot container.
4. Inject `firmware.config` at flash time from a generated `device.json`:

   ```json
   { "wifi":  { "wifi.ssid": "…", "wifi.password": "…" },
     "porta": { "gateway.host": "192.168.0.175", "gateway.port": 6969 } }
   ```

   via `toit tool firmware -e nodus.envelope flash --config device.json --port …`
   (the same `--config` channel WiFi already uses; confirmed: envelope
   properties == runtime `firmware.config`, round-trip verified).
5. Seed the `nodes` row with chip/SDK (+ gateway address).

**Runtime side (nodus, separate repo):** `supervisor.toit` replaces the hardcoded
`GATEWAY-HOST ::= "192.168.0.175"` constant with a read of
`firmware.config["porta"]` → `gateway.host`/`gateway.port`, falling back to the
constant when the key is absent (so `jag run` dev flow is untouched). Nested
`"porta"` key, mirroring how `wifi` nests. This is a nodus-repo change tracked
alongside Phase 2.

## 8. Error handling

- Wrapped call non-zero exit → stop; show captured stderr + the exact command,
  copy-pasteable to rerun by hand (trainer-wheels).
- `toit` / `jag` not on PATH → clear setup message.
- Envelope fetch failure (network, or no release for the chip/SDK) → clear
  message listing available chips/SDKs.
- Interrupted (Ctrl-C) mid-pipeline before enqueue → nothing is delivered (the
  payload upload + run enqueue is the last step); temp artifacts cleaned up.

## 9. Testing

- **Engine/orchestration (host, no real toit):** inject a fake command runner;
  assert the command sequence, narration records, conflict refusals
  (chip/SDK/unknown-identity), prompt→args mapping, and envelope-cache
  resolution. The bulk of coverage lives here.
- **Identity:** store columns round-trip; report ingestion updates chip/SDK;
  Toit reference parity for the additive report fields.
- **Real toolchain:** an opt-in integration test (build tag) and a HW smoke
  (`porta run` a tiny app to a live node, observe it run) — not in the default
  `go test ./...` path.

## 10. Decomposition & phasing

- **Phase 1 (this spec's primary deliverable):** `internal/toolchain` (narration
  engine + injectable runner + envelope cache/fetch + compile/relocate +
  identity resolution + conflict guard); the additive **report identity fields**
  (nodus + `docs/PROTOCOL.md` + `nodes` columns + ingestion); the **`porta run`**
  cobra command (prompts + flags). Self-sufficient: identity comes from the
  report.
- **Phase 2:** the **`porta flash`** provision command + the nodus
  `firmware.config["porta"]` read + early identity seed.

Each phase gets its own implementation plan.

## 11. Open implementation details to pin (not design risks)

- Exact `toit tool snapshot-to-image` relocation flags for each chip word-size.
- `toitlang/envelopes` release-asset URL scheme and the cache layout/key
  (`(chip, sdk)` → envelope path).
- The on-device API for reading `chip` and the firmware SDK version in nodus.
- Whether the envelope cache reuses jag's cache dir or porta owns its own.
