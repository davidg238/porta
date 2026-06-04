# porta

The **porta** gateway — split from `st-zephyr`. porta is the northbound controller and
authority for a fleet of heterogeneous nodes: it owns the wire protocol, queues
commands, delivers container images over TFTP, and ingests telemetry.

Start with **`CLAUDE.md`** for orientation, then `docs/specs/` for the approved
designs and `docs/plans/` for the implementation plans.

- `cmd/porta/` + `internal/` — Go gateway (mainline), module `github.com/davidg238/porta`
- `deploy/compile-svc/` — systemd unit for the ST compile service (used by the parked `st-devserver`)
- `docs/PROTOCOL.md` — the canonical wire protocol all node implementations conform to
- `st-zephyr` — local symlink to the parent project (gitignored)

A full Toit implementation of the gateway lives in the separate **`gateway`** repo
(`github.com/davidg238/gateway`), extracted from here on 2026-06-04. The node side lives
in the separate **`nodus`** repo (repo URL set at extraction).

#### Links
* [Logging MQTT Sensor Data to SQLite DataBase With Python](http://www.steves-internet-guide.com/logging-mqtt-sensor-data-to-sql-database-with-python/)
