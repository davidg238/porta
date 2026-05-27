# nodus

`nodus` (Latin "knot/node") is the **node project**: the on-device loader/supervisor
— "the keeper" — plus the node-side services, packaged as a Toit package. The keeper
polls the gateway over UDP/TFTP, installs the delivered container image, runs it per its
declared lifecycle, deep-sleeps, and re-polls.

`nodus` is **one** node implementation. The gateway controls *heterogeneous* nodes
(e.g. planned Smalltalk-based nodes) that all speak the same wire protocol. That protocol
is defined canonically in the **porta** repo at `docs/PROTOCOL.md` — the gateway is the
northbound authority that owns the command vocabulary, report schema, and the TFTP
delivery blob header. `nodus` conforms to it; it does not define it.

> porta repo (gateway + protocol): (repo URL set at extraction)
> Protocol: porta's `docs/PROTOCOL.md`.

## Layout (paths relative to the package root)

- `package.yaml` / `package.lock` — the `nodus` Toit package (depends on `tftp`).
- `src/` — the node library: L1 supervisor + transport infra and the service modules
  (supervisor, transport, goal_state, inventory, node_command, node_id, report, triggers,
  image_writer, flash_image, schedule_store, config_store, control_service,
  telemetry_service, telemetry_buffer, telemetry_codec). Modules use internal relative
  imports (`import .goal_state show ...`).
- `tests/` — `*_test.toit` host suites; import the library by package name
  (`import nodus.goal_state show ...`).
- `examples/` — per-example sub-packages (`chatty`, `control-demo`, `hello`,
  `vindriktning`), each with its own `package.yaml` declaring a `path: ../..` dep on
  `nodus`. `vindriktning` vendors its `vindriktning.toit` driver + `olympic.toit` helper.
- `host/` — firmware tooling: `build-envelope.sh`, `capture_sink.go`, `SDK_VERSION`.
- `docs/` — node-side specs and plans.

## Build & test (run from the package root)

- One host suite: `toit tests/<name>_test.toit`
- An example image (snapshot → relocated ESP32 image):
  `toit compile -s -o X.snapshot examples/<name>/<file>.toit && \
   toit tool snapshot-to-image -m32 --format=binary -o X.bin X.snapshot`
- Supervisor firmware envelope: `bash host/build-envelope.sh`

SDK is pinned at `v2.0.0-alpha.192` (see `host/SDK_VERSION`); payload images and device
firmware must be built with the *same* SDK (the image is relocated per chip and must match
the firmware's SDK version). Target board: classic ESP32 (Xtensa). Invoke the Toit skills
(toit-conventions, toit-package, toit-jag-dev, toit-envelope, toit-exe) before writing Toit.

## Next milestone

**Always-on vin.** vin currently ships as a `boot × run-once` example; the real PM1006
sensor emits frames in bursts, so a run-once 8-samples-per-wake cycle can't reliably
complete. The always-on (`run-loop`) direction is the next milestone.
