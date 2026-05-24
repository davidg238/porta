# Design note: Security posture & transport evolution

**Date:** 2026-05-24
**Status:** Principles captured (sibling to the M2 telemetry spec); **nothing built**.
Records the decided direction so future work doesn't relitigate it. LAN is **trusted**
for now (consistent with the gateway design's non-goals: "groups, signing, diff-OTA",
with **Artemis as the WAN tier** owning signing).
**Sibling of:** `2026-05-24-m2-telemetry-design.md`.

## The transport seam

`device/transport.toit` already defines a clean `GatewayClient` interface
(`fetch-bytes` / `fetch --to-writer` / `put` / `close`) with `TftpGatewayClient` as
one implementation. **Swappability lives in this interface, not in container topology.**
A future `CoapGatewayClient` or `EspnowGatewayClient` is a drop-in — independent of
where the socket process lives. M2 keeps transport in the supervisor and treats this
interface as *the* swap seam.

## Why not CoAP/DTLS (channel-level security)

- **Deep-sleep kills CoAP's main advantage.** CoAP's killer feature over TFTP is
  *observe* (server-push subscriptions) — but a node that deep-sleeps cannot hold a
  subscription. For this fleet CoAP reduces to "nicer request/response + block-wise
  transfer," which fits the existing `fetch-bytes`/`fetch`/`put` shape directly.
- **DTLS is a poor fit for a sleeping, multi-transport fleet.** A DTLS session
  re-handshakes every wake (expensive, stateful), and the TFTP TID/ephemeral-port
  shift breaks DTLS session binding (the same TID-race we already hit — `tftp#5`).
  "TFTP over DTLS" was never standardized for exactly these structural reasons.

## The decided direction: payload-level, transport-agnostic security

Secure the **payload**, not the channel. Crucially, for delivering **executable
code** the property that matters most is **authenticity/integrity (signing)**, not
confidentiality — a LAN attacker serving a forged image = arbitrary code execution on
the node. (Today's CRC32 is an anti-*corruption* check, **not** cryptographic —
trivially forgeable. The current guarantee is "the LAN is trusted.")

- **Sign the image** (and seal/sign reports if/when needed). A signature is
  **stateless** and **transport-agnostic** — it rides through TFTP, CoAP, *or* ESPnow
  unchanged, and costs nothing per wake (unlike a DTLS handshake). This composes
  perfectly with the `GatewayClient` seam + the ESPnow goal: **security lives above
  the transport**, making the TFTP-vs-CoAP-vs-ESPnow choice a security non-event.
- **Confidentiality** (RFC 8886-style "encrypt the config/payload to the device's
  public key, ship over dumb transport") is a *separate, lesser* concern for code
  delivery; available the same transport-agnostic way if a payload ever carries
  secrets (e.g. embedded WiFi creds). Note RFC 8886 is **Informational** and narrowly
  about network-device *config* provisioning — not a general firmware-security
  standard.

## Scope / ownership

- **Not now, not channel-level.** When security is needed it will be **payload-level
  (sign image + reports), transport-agnostic**, aligned with the **Artemis** boundary
  (the WAN tier that owns signing). File as a future security milestone.
- Porta stays the **trusted-LAN** tier until then.
```
