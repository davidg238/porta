# porta

The **jast** gateway as its own project — split from `st-zephyr`. porta is the
northbound controller and authority for a fleet of heterogeneous nodes: it owns the
wire protocol, queues commands, delivers container images over TFTP, and ingests
telemetry.

Start with **`CLAUDE.md`** for orientation, then `docs/specs/` for the approved
designs and `docs/plans/` for the implementation plans.

- `gateway/` — Toit gateway control plane (sqlite-backed)
- `gateway-go/` — existing Go gateway (copied from `st-zephyr/tools/jast-gw`)
- `deploy/` — deploy kit (Dockerfile + systemd unit) for the live gateway
- `docs/PROTOCOL.md` — the canonical wire protocol all node implementations conform to
- `st-zephyr` — local symlink to the parent project (gitignored)

The node side lives in the separate **`nodus`** repo (repo URL set at extraction).
