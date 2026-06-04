# Deploying porta (native `.deb`)

porta is a plain Go binary. Unlike the parked Toit gateway (whose `toit-sqlite`
needed glibc 2.38 and therefore a container), porta's binary needs only
**glibc ≥ 2.34**, which every current Linux — including gw85224-01 (Debian 12,
glibc 2.36) — satisfies. So porta installs **natively**, no container required.

## Build the package

```bash
./deploy/build-deb.sh                  # → deploy/dist/porta_0.1.0_amd64.deb
VERSION=0.1.0+gw ./deploy/build-deb.sh # custom version string
```

The script builds the CGO (dynamic) binary, stages a systemd unit + maintainer
scripts, and runs `dpkg-deb`. It prints the max glibc symbol the binary needs so
you can confirm it's ≤ the target's glibc before shipping.

## Install on a target (e.g. gw85224-01)

```bash
scp deploy/dist/porta_*_amd64.deb david@gw85224-01:
ssh david@gw85224-01 'sudo apt install -y ./porta_*_amd64.deb'
```

`apt install ./file.deb` (or `dpkg -i`) drops `/usr/bin/porta`, installs
`porta.service`, and the postinst **enables + starts** it. The service uses
systemd `DynamicUser` + `StateDirectory=porta`, so the durable sqlite store lives
at `/var/lib/porta/porta.db` under an unprivileged ephemeral user.

> **Coexistence:** porta binds only **UDP/TFTP :6969** and **HTTP :6970**. On
> gw85224-01 that does not collide with `jast-gw` (:5683), OTBR, or the ST
> compile-svc (:5686). Do not stop those.

## Verify

```bash
ssh david@gw85224-01
systemctl status porta            # active (running)
porta scan                        # talks to the local API on :6970
curl -s localhost:6970/healthz    # or open http://<gw-ip>:6970/ for the htmx console
```

Point a node at the gateway's LAN IP on `:6969` and confirm a report lands
(`porta scan` shows it online).

## Service management

```bash
sudo systemctl {status,restart,stop,start} porta
journalctl -u porta -f            # logs (reconcile lines, reports, telemetry)
```

## Upgrade / remove

```bash
sudo apt install -y ./porta_<newver>_amd64.deb   # upgrade in place; restarts the service
sudo apt remove porta                            # stops + disables; leaves /var/lib/porta
sudo apt purge porta && sudo rm -rf /var/lib/porta   # full wipe incl. the store
```
