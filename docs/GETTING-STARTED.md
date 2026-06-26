# Getting started with porta

porta is a self-hosted, language-neutral LAN gateway for a fleet of nodes: it
owns a wire protocol, queues commands, delivers container images over TFTP, and
ingests telemetry. This guide gets a gateway running and a node talking to it,
then points at the [CLI reference](CLI.md). For the big picture first, read
[`ARCHITECTURE.md`](ARCHITECTURE.md).

porta is one process with two listeners:

- **`:6969/udp`** — TFTP: node check-ins, command delivery, image transfer.
- **`:6970/tcp`** — the operator surface: web dashboard, JSON API, read-only MCP.

---

## 1. Run a gateway

### Option A — install the `.deb` (recommended for a real box)

For a Debian/Ubuntu host (needs glibc ≥ 2.34, which any current release has):

```bash
# build the package (on a box with Go + a C toolchain; porta uses CGO sqlite)
VERSION=0.5.4 ./deploy/build-deb.sh        # → deploy/dist/porta_0.5.4_amd64.deb

# install on the target
sudo apt install -y ./porta_0.5.4_amd64.deb
```

The package installs a systemd unit that runs `porta serve` as a `DynamicUser`
with its database under `StateDirectory`:

```bash
systemctl status porta        # active (running), enabled
# listens on :6969/udp + :6970/tcp; db at /var/lib/porta/porta.db
```

> **Schema upgrades:** porta uses `CREATE TABLE IF NOT EXISTS` with no migrations
> (pre-1.0, crash-and-fix). A release that *adds* columns needs the db recreated:
> `sudo systemctl stop porta && sudo rm -f /var/lib/porta/porta.db* && sudo systemctl start porta`.
> Pure UI/web releases do not. (On a `DynamicUser` host the real path is
> `/var/lib/private/porta/porta.db`.)

### Option B — run the binary directly (quickest for a look)

porta links SQLite via CGO, so building needs `CGO_ENABLED=1` (the default) and a
C compiler:

```bash
go build ./cmd/porta
./porta serve --db ./porta.db
# porta: serving TFTP on udp :6969 (db=./porta.db)
# porta: serving HTTP on 0.0.0.0:6970
```

Run the whole test suite while you're here:

```bash
go test ./...
```

---

## 2. Confirm it's up

```bash
# web dashboard
open http://localhost:6970/                 # fleet list

# JSON API
curl -s http://localhost:6970/api/nodes     # {"ok":true,"data":{"nodes":[...]}}

# CLI (a client over the API; --server defaults to http://localhost:6970)
porta scan                                  # empty fleet prints nothing, exit 0
```

The HTTP listener is allow-listed to RFC1918 + loopback + Tailscale CGNAT by
default, so it's reachable from your LAN and over Tailscale, but not the public
internet. Adjust with `serve --http-allow-cidr`.

---

## 3. Point a node at it

A node is provisioned with the gateway's address at flash time (the same touch
that sets WiFi). For the reference Toit node, that's the
[`nodus`](https://github.com/davidg238/nodus) repo — flash a board and give it
`<gateway-host>:6969`. Once it boots and joins WiFi it polls the gateway, shows up
in `porta scan` / the dashboard, and starts reporting its chip and SDK.

porta itself is language-neutral: any node that conforms to
[`PROTOCOL.md`](PROTOCOL.md) works the same way.

---

## 4. First operations

Once a node is online (here, named `vin`):

```bash
porta scan                                   # see it online
porta device show -d vin                      # identity, observed state, counts

# turn on telemetry/log/print forwarding (absolute — all three required)
porta device set-forward -d vin --print on --log on --telemetry on --log-level info

# watch telemetry stream
porta monitor -d vin -f

# push a config value and watch it converge
porta device set -d vin sampler interval 60
porta device get -d vin sampler               # desired vs observed

# deploy a prebuilt image
porta container install -d vin sampler ./sampler.bin --trigger boot --lifecycle run-loop
porta container list -d vin
```

Full command details: [CLI reference](CLI.md).

---

## 5. The web dashboard

`http://<host>:6970/` is a read-only operator console (polling htmx, no JS app):

- **Fleet page** — every node with a check-in gauge.
- **Node detail** — identity, desired-vs-observed config, recent commands with
  lifecycle badges, containers, and two console panels (Prints / Logs). Panic
  rows carry a `[decode ↗]` link that hands the trace to the node's dev tool.
- **Telemetry** and **Command Log** pages.
- **Status page** — porta build identity + uptime, per-transport volume
  (wifi/thread/espnow nodes, packets, bytes), report ok/rejected counts, and
  sqlite store metrics (on-disk size, pages, per-table rows, data_log span).
  The same surface is available as JSON at `GET /api/status` (and a slimmer
  subset at `/health`).

---

## Building from source / contributing

```bash
git clone https://github.com/davidg238/porta && cd porta
go build ./...        # needs CGO (C toolchain) for the sqlite driver
go test ./...
```

Repo layout: `cmd/porta/` is the entrypoint; `internal/` holds the gateway
(`store`, `command`, `handler`, `tftp`, `apisrv`, `web`, `mcpsrv`, `portacli`);
`devsdk/` is the public Go surface for node-repo tooling. Design docs live in
`docs/` (`ARCHITECTURE`, `PROTOCOL`, `DEVSDK`) with approved designs in
`docs/specs/` and implementation plans in `docs/plans/`.

The wire protocol ([`PROTOCOL.md`](PROTOCOL.md)) is the one fixed point every
node conforms to — change it deliberately.
