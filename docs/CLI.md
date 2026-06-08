# porta CLI reference

`porta` is one binary. `porta serve` runs the gateway daemon (the only command
that opens the database); **every other command is a thin client** that talks to
a running server over its HTTP API. New to porta? Start with
[`GETTING-STARTED.md`](GETTING-STARTED.md).

```
porta serve                                   run the gateway daemon
porta scan                                    list nodes
porta ping            -d <node>               is a node online?
porta log             -d <node>               command audit history
porta monitor         -d <node> [-f]          telemetry tail
porta device show     -d <node>               node details
porta device get      -d <node> <app> [key]   desired vs observed config
porta device set      -d <node> <app> <key> <value>
porta device set-forward -d <node> --print … --log … --telemetry …
porta device reboot   -d <node>
porta container list      -d <node>
porta container install   -d <node> <name> <file.bin> [--trigger … --interval … --lifecycle …]
porta container uninstall -d <node> <name>
```

## Global flags

| Flag | Default | Notes |
|------|---------|-------|
| `--server <url>` | `$PORTA_SERVER`, else `http://localhost:6970` | Base URL of the running gateway. Used by every command **except** `serve`. |
| `--db <path>` | `porta.db` | SQLite path. Only `serve` opens the store; ignored by client commands. |

`-d` / `--device` takes a **node name or MAC** and is required by every per-node
command. Durations (`<dur>`) accept Go-style strings: `30s`, `5m`, `1h`.

---

## serve

Run the gateway: the UDP/TFTP listener plus the optional HTTP operator surface
(web dashboard + JSON API + read-only MCP).

```
porta serve [--port 6969] [--http-port 6970] [--http-bind 0.0.0.0] [--http-allow-cidr …]
```

| Flag | Default | Notes |
|------|---------|-------|
| `--port` | `6969` | UDP/TFTP port (node traffic). |
| `--http-port` | `6970` | Operator HTTP port; `0` disables the HTTP surface. |
| `--http-bind` | `0.0.0.0` | HTTP bind address. |
| `--http-allow-cidr` | RFC1918 + loopback + Tailscale `100.64.0.0/10` | Repeatable allow-list for the HTTP listener; empty = serve any peer. |

```bash
porta serve --db /var/lib/porta/porta.db          # what the systemd unit runs
```

## scan

List nodes with last-seen age and online/offline status.

```
porta scan [--include-never-seen]
```

`--include-never-seen` also shows nodes that were named/created but have never
checked in.

```bash
porta scan
# 30aea41a6208  fwkb              12s ago      online
# 7c9ebdd8f58c  vin               48s ago      online
```

## ping

```
porta ping -d <node>
```

```bash
porta ping -d vin            # vin (7c9ebdd8f58c): online
```

## log

Command audit history for a node (id, verb, delivered?, args), oldest first.

```
porta log -d <node>
```

## monitor

Print a node's telemetry over the API; `--follow` tails new rows (exact id
cursor, Ctrl-C to stop).

```
porta monitor -d <node> [--since 1h] [--kind log|metric|panic] [-f]
```

| Flag | Default | Notes |
|------|---------|-------|
| `--since` | `1h` | Look-back window: `30m`, `1h`, … |
| `--kind` | (all) | Filter to `log`, `metric`, or `panic`. |
| `-f`, `--follow` | off | Poll the server and tail new rows. |

```bash
porta monitor -d vin --kind metric -f
# jun-07 10:52:03  metric  pm25=16.0
```

---

## device show

```
porta device show -d <node>
```

Prints id, name, kind, source address, last-seen, the node's check-in cadence,
the derived offline window (3×cadence), last reset reason, the raw observed
state, and the undelivered command count.

## device get

Show desired-vs-observed config for an app, or a single key. Flags a self-heal
warning when a key has been re-issued ≥2×.

```
porta device get -d <node> <app> [key]
```

```bash
porta device get -d vin sampler
# 7c9ebdd8f58c: config for sampler
#   KEY       DESIRED  OBSERVED
#   interval  60       60
```

## device set

Enqueue a per-app config write. The value's scalar type (int/float/bool/string)
is inferred from the text.

```
porta device set -d <node> <app> <key> <value>
```

```bash
porta device set -d vin sampler interval 60
# 7c9ebdd8f58c: enqueued set sampler.interval=60 (command #12)
```

## device set-forward

Set the **complete** per-stream forwarding policy. `set-forward` is *absolute* —
every stream you don't enable is turned off — so `--print`, `--log`, and
`--telemetry` are all required.

```
porta device set-forward -d <node> --print on|off --log on|off --telemetry on|off [--log-level …]
```

| Flag | Notes |
|------|-------|
| `--print` | Forward the print stream (`on`/`off`). Required. |
| `--log` | Forward the log stream (`on`/`off`). Required. |
| `--telemetry` | Forward the telemetry/metric stream (`on`/`off`). Required. |
| `--log-level` | Minimum log level: `trace`,`debug`,`info`,`warn`,`error`,`fatal` (node default `warn`). |

```bash
porta device set-forward -d vin --print on --log on --telemetry on --log-level info
```

## device reboot

Enqueue a reboot; applied at the end of the node's next poll. No convergence to
confirm.

```
porta device reboot -d <node>
```

## Node configuration (mode, cadence, name) — not a porta command

A node **owns** its configuration; porta originates none of it. Power mode
(`set-mode`), check-in cadence, and the node name (`set-name`) are set from the
**node's own dev CLI** (e.g. `nodus mode` / `nodus rename`), which enqueues the
command through porta's queue and polls porta's persisted `node_config` echo to
confirm convergence. porta **transports** these verbs and **displays** the
node's effective config read-only (`porta device show`, the web node page); it
exposes no command, flag, or form that originates a config change. The node's
offline window is **derived** (`3 × cadence`), not a settable `max-offline`.

---

## container list

List apps from the node's latest observed report (name, CRC, runlevel).

```
porta container list -d <node>
```

## container install

Register a prebuilt image (`.bin` only) and enqueue a run. With no trigger and
no interval the image is registered but not started.

```
porta container install -d <node> <name> <file.bin> [flags]
```

| Flag | Default | Notes |
|------|---------|-------|
| `--trigger` | (none) | Trigger spec: `boot`, `gpio-high=21`, … Repeatable. |
| `--interval` | (none) | Interval trigger, e.g. `30s`. |
| `--runlevel` | `3` | Runlevel. |
| `--lifecycle` | `run-once` | `run-once` or `run-loop`. |

```bash
porta container install -d vin sampler ./sampler.bin --trigger boot --lifecycle run-loop
# 7c9ebdd8f58c: registered sampler (38912 B); enqueued run (command #13)
```

## container uninstall

Enqueue a stop for an app.

```
porta container uninstall -d <node> <name>
```

---

## See also
- [`GETTING-STARTED.md`](GETTING-STARTED.md) — install, run, and first steps.
- [`ARCHITECTURE.md`](ARCHITECTURE.md) — the system picture.
- [`PROTOCOL.md`](PROTOCOL.md) — the wire protocol nodes conform to.
- The node side ([`nodus`](https://github.com/davidg238/nodus)) is what flashes a
  node and points it at the gateway.
