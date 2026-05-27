# gateway — Porta Toit gateway (B1: TFTP-free core)

The host side of the Porta LAN gateway: a sqlite store, the command wire-codec,
and a `jag`-aligned CLI. This is **B1** — the daemon that serves nodes over
TFTP (`serve.toit`), the store-backed request handler, and the device-side
drain/apply/report changes are **B2** (and depend on Spec A's TFTP refactor).

See `docs/specs/2026-05-23-porta-toit-gateway-design.md` (Spec B) and
`docs/plans/2026-05-23-gateway-b1-tftp-free-core.md`.

## Toolchain

Runs on the host via the prebuilt `toit-sqlite` binary (the `sqlite` package
needs its bundled SDK):

    export TS=~/workspaceToit/sqlite/build/bin/toit-sqlite
    cd gateway && $TS pkg install

## CLI

    $TS gateway.toit --db porta.db <command>

| Command | Effect |
|---|---|
| `scan [--include-never-seen]` | list nodes + online/offline health |
| `ping -d <node>` | recently-seen check |
| `device show -d <node>` | last contact, observed state, queued commands |
| `device set-max-offline -d <node> <dur>` | offline threshold (config) |
| `device set-poll-interval -d <node> <dur>` | enqueue a poll-cadence change |
| `device name -d <node> <new-name>` | override the auto-name |
| `container install <name> <file> -d <node> [--crc N] [--interval <dur>] [--trigger t=v]… [--runlevel N]` | register a `.bin` image + enqueue run |
| `container uninstall <name> -d <node>` | enqueue stop |
| `container list -d <node>` | apps from the latest report |
| `log -d <node>` | command audit history |

`<node>` is a node name or its base-MAC hex. `.pod` and `.toit` inputs are
accepted by `container install` but report "scheduled for M3 / M4".

## Tests

    cd gateway
    for t in crc32_test duration_test command_test names_test store_test integration_test; do $TS "$t.toit"; done
