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

Related repos, all extracted from here on 2026-06-04 and coupled only over the wire:
a full Toit implementation of the gateway in **`gateway`** (`github.com/davidg238/gateway`);
the parked Smalltalk gateway / future ST-node tooling in **`nodus-st`**
(`github.com/davidg238/nodus-st`); and the node side in
**[`nodus`](https://github.com/davidg238/nodus)**.

#### Links
* [Logging MQTT Sensor Data to SQLite DataBase With Python](http://www.steves-internet-guide.com/logging-mqtt-sensor-data-to-sql-database-with-python/)
