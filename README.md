# porta

porta is the northbound controller and authority for a fleet of heterogeneous
nodes: it owns the wire protocol, queues commands, delivers container images
over TFTP, and ingests telemetry.

**New here?** Read **`docs/GETTING-STARTED.md`** to run a gateway and point a node
at it, then **`docs/CLI.md`** for the command reference. For the system picture see
**`docs/ARCHITECTURE.md`**, then `docs/specs/` (approved designs) and `docs/plans/`
(implementation plans).

- `cmd/porta/` + `internal/` — Go gateway (mainline), module `github.com/davidg238/porta`
- `docs/GETTING-STARTED.md` — install, run the gateway, first steps
- `docs/CLI.md` — the `porta` command-line reference
- `docs/ARCHITECTURE.md` — the canonical system-architecture doc (the whole-system picture)
- `docs/PROTOCOL.md` — the canonical wire protocol all node implementations conform to
- `docs/DEVSDK.md` — the northbound dev-SDK contract for node-repo tooling

## License

- The porta gateway (everything in this repo except `devsdk/`) is licensed under the
  **GNU Affero General Public License v3.0** — see [`LICENSE`](LICENSE). Run it, modify
  it, self-host it freely; if you offer a modified porta to others over a network, offer
  them the source too. For uses the AGPL doesn't fit, contact Ekorau LLC about a
  commercial license.
- **`devsdk/`** is licensed under the **MIT License** — see
  [`devsdk/LICENSE`](devsdk/LICENSE) — so node-repo tooling in any language can import
  it without copyleft obligations.
- `docs/PROTOCOL.md` is a published specification: implementing the wire protocol
  carries no license obligation in either direction.

Related repos, all extracted from here on 2026-06-04 and coupled only over the wire:
a full Toit implementation of the gateway in **`gateway`** (`github.com/davidg238/gateway`);
the parked Smalltalk gateway / future ST-node tooling in **`nodus-st`**
(`github.com/davidg238/nodus-st`); and the node side in
**[`nodus`](https://github.com/davidg238/nodus)**.
