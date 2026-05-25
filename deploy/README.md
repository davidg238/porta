# Deploying the porta gateway to gw85224-01

Runbook for running the porta Toit gateway as a Docker container on the
always-on box **gw85224-01**, for long-term soak of config self-heal.

> **Coexistence rule:** gw85224-01 also runs `jast-gw` (`:5683`), an OTBR
> container, and the ST compile-svc (`:5686`). Porta uses **`:6969`** and a
> separate db — it does **not** touch any of them. Do not stop them.

## Why a container

The prebuilt `toit-sqlite` binary links GLIBC up to 2.38; gw85224-01 (Debian 12)
ships glibc **2.36**, so the binary cannot run on the host, and the box lacks
`cmake`/`ninja`/`go` to rebuild it. `ubuntu:24.04` carries glibc 2.39, and gw
already runs Docker (OTBR), so a container is the low-friction fix.

The gateway snapshot bundles every Toit dependency, so the image needs only the
`toit-sqlite` runtime (`bin/` + sibling `lib/` SDK tree) plus `gateway.snapshot`.

## 1. Build the kit (on this dev box)

```bash
cd ~/workspaceToit/porta
./deploy/build-kit.sh        # → deploy/kit/  (~56 MB, gitignored)
```

This compiles a fresh `gateway.snapshot` from `gateway/gateway.toit` and stages
the `toit-sqlite` binary + `lib/` tree + `Dockerfile`.

## 2. Ship the kit and build the image on gw

```bash
rsync -a deploy/kit/ david@gw85224-01:~/porta-kit/
scp deploy/porta-gw.service david@gw85224-01:~/porta-kit/      # not in the build context
ssh david@gw85224-01 'cd ~/porta-kit && docker build -t porta-gw:latest .'
```

(Or build locally and `docker save porta-gw:latest | ssh david@gw85224-01 docker load`.)

## 3. Install the systemd unit and start

```bash
ssh david@gw85224-01
sudo mkdir -p /var/lib/porta                      # durable db lives here
sudo cp ~/porta-kit/porta-gw.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now porta-gw
```

The unit runs:

```
docker run --rm --name porta-gw --network=host -v /var/lib/porta:/data porta-gw:latest
```

`--network=host` is required: TFTP hands out ephemeral UDP ports, so the
gateway must own the host network namespace; `:6969` then binds the host.

## 4. Smoke-test over loopback / Tailscale

```bash
ssh david@gw85224-01
docker exec porta-gw /opt/porta/bin/toit-sqlite \
    run /opt/porta/gateway.snapshot -- --db=/data/porta.db scan
```

A clean empty store prints just the `DEVICE NAME LAST-SEEN STATUS` header.

## 5. Point a WiFi/Toit node at the gateway

Aim a Toit node at gw's LAN IP on `:6969` (or, last, stand gw up as a hostapd
AP — the user moves the dongle). Confirm a report lands (`scan` shows the node
online) before relying on the AP.

## 6. Watch the soak (the actual verification)

Config self-heal proves itself live here. Watch for the reconcile log line and a
growing `data_log`:

```bash
docker logs -f porta-gw 2>&1 | grep -E 'reconcile re-issued|report'
docker exec porta-gw /opt/porta/bin/toit-sqlite \
    run /opt/porta/gateway.snapshot -- --db=/data/porta.db monitor <node> --follow
```

## Verified locally (2026-05-25)

Image builds on ubuntu:24.04 (199 MB); daemon serves on UDP; CLI works against
the mounted volume; sqlite writes the full schema to `/data/porta.db` and
`PRAGMA integrity_check` = ok. The only thing not exercisable off-box is a real
WiFi node poll.
