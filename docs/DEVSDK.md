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
running?" hint. `apiclient.New(baseURL)` takes the server base URL **explicitly**
— there is no defaulting in the package itself. The `$PORTA_SERVER`-or-
`http://localhost:6970` default is a CLI convention (see `internal/portacli`)
that a node tool may choose to mirror.

## `firmware.config["porta"]` provisioning contract

A node finds its gateway from its firmware config at the key `porta`, with dotted
sub-keys under that group (mirroring how `wifi` nests):

    firmware.config["porta"] = {"gateway.host": <string>, "gateway.port": <int>}

`devsdk/provision` fixes this shape (`Gateway.PortaConfig()`, the `PortaConfigKey`
/ `GatewayHostKey` / `GatewayPortKey` constants, `ParseGateway`). The key strings
must match the nodus supervisor's `gateway_config.toit` reader exactly.
A node-repo flash tool injects it at first flash; the node's supervisor reads
it (falling back to a compiled-in default for bench `jag run`). WiFi is **not**
part of this contract — node tools provision WiFi via their own flasher (e.g.
jag's `--wifi-ssid/--wifi-password`). The *injection mechanism* is the node
tool's concern; `devsdk` fixes only the shape.

## `nodus://decode` URL scheme (panic decode link)

porta's web Logs panel renders a `[decode ↗]` link on each `panic` telemetry row.
The link is the porta→nodus tooling contract:

    nodus://decode?node=<node-id>&blob=<url-encoded base64 panic message>

- `node` — the porta node id (hex EUI), for labelling/fallback lookups.
- `blob` — the raw base64 panic message from the `data_log` row, URL-encoded
  (base64's `+ / =` are percent-encoded).

porta only **emits** this link. The node-repo dev tool registers an OS handler for
the `nodus` scheme (Linux: a `.desktop` file with
`MimeType=x-scheme-handler/nodus;` and `Exec=nodus decode %u`, then
`xdg-mime default …`) and implements `nodus decode <url>`: parse `blob`, run
`jag decode` against the local snapshot cache (jag resolves the snapshot by the
program uuid embedded in the message), and show the decoded trace locally
(e.g. a popup with copy-to-clipboard). Nothing is written back to porta.

## Neutrality

The porta gateway implements **zero** language- or hardware-specific function.
All language/hardware specifics (compile, relocate, flash, decode, per-kind
presentation) live in node repos. Any future language-specific *gateway*
function arrives as a node-repo-owned **sidecar** process, never compiled into
the gateway binary (see design spec §6).
