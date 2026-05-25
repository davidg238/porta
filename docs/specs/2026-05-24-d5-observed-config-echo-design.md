# D5 — Observed-Config Echo Design

**Date:** 2026-05-24
**Status:** Implemented — host-verified 2026-05-24. Hardware echo confirmation
pending a poll-wake on the device (observed config refreshes on the next report).
**Context:** Fast-follow to M2.2 down-path (config/setpoints). M2.2 shipped
`device set <app> k=v` → command queue → per-app NVS config blob → app reads its
own config via `ControlService`. `device get` was deliberately *desired-only*
(projected from the `set` command log; design decision D5). This spec adds the
**observed** side so an operator can confirm a `set` actually landed.

## Goal

The device report carries the config it has actually applied (its NVS `config`
blob). The gateway stores it alongside observed apps. `device get` shows
**desired vs observed** per key, with a `(drift)` / `(pending)` marker. This is
the mirror of how `container-list` already echoes observed apps from the report.

## Non-goals

- No new gateway table or column — observed config rides in the existing
  `observed_state` TEXT blob.
- No change to the device-side read path (`ControlServiceProvider` / app reads).
- No DB migration / back-compat shim (pre-1.0, no deployments — see
  `porta-no-legacy`). Old reports simply carry no `config`; `device get` renders
  those as `(pending)`.

## Data flow (4 touch points)

```
operator sets desired          device applies & echoes observed
        │                                    │
   set command                         build-report
        │                                    │
   command_queue ──poll──▶ supervisor ──▶ report?id= (apps + config + health)
        │                                    │
   project-config                      ReportWriter_ → observed_state blob
        │                                    │
        └──────────▶ device get ◀────────────┘
                   (desired vs observed table)
```

### 1. Device `build-report` (`device/report.toit`)

Gains a `--config/Map` parameter and emits a `"config"` sibling next to
`"apps"`/`"health"`:

```json
{"apps": {…}, "config": {"<app>": {"<key>": <value>}}, "health": {…}}
```

`config` is the whole NVS config blob (all apps, all keys), bounded by config
size — the same shape `load-config` returns. Emit it even when empty (`{}`) for
a uniform body; the gateway tolerates a missing key for old nodes.

Supervisor passes `load-config bucket` at the existing report call site
(`device/supervisor.toit:143`):

```toit
body := build-report inventory --config=(load-config bucket) --uptime-us=clock-us --wakes=store.wakes
```

### 2. Gateway `ReportWriter_` (`gateway/handler.toit`)

In `close_`, pull `config` out of the report body and fold it into the
`observed_state` blob it already builds:

```toit
config := obj.get "config" --if-absent=: {:}
store_.insert-report id_
    --observed-state=(encode-json_ {"apps": apps, "config": config})
    --health=(encode-json_ health)
    --now=now_
```

`Store.insert-report` and the `reports` / `nodes.observed_state` columns are
unchanged (already TEXT).

### 3. Gateway `cmd-device-get` (`gateway/gateway.toit`)

Already computes `desired` from `project-config (store.command-log id)`. Now also
read the node's `observed_state`, decode it, and pull `config[app]` (`{:}` if the
node is missing, has no report, or the report predates this feature). Render a
two-column table over the **union** of desired and observed keys:

```
aabbccddeeff: config for thermostat
  KEY        DESIRED   OBSERVED
  setpoint   21.5      21.5
  mode       eco       heat      (drift)
  hysteresis 0.5       --        (pending)
```

Marker rules, per key:
- `(drift)` — desired and observed both present and **unequal**.
- `(pending)` — desired present, observed absent (rendered `--`).
- *(none)* — desired and observed present and equal.
- observed present, desired absent — show with desired `--`, no marker
  (abnormal; only after a DB reset since `set` commands accumulate and the
  command-log projection is the full desired set).

Empty value cell renders as `--`. The single-key form
`device get <app> <key>` shows the one row (same desired/observed/marker).
If both desired and observed are empty for the app: `$id: $app has no config`.

Value comparison uses Toit `==` on the JSON-decoded scalars. Both sides decode
from JSON the same way (desired from command-log args stored as JSON; observed
from the device report), so an int stays int and a float stays float —
equal sets compare equal.

### 4. Device read path — unchanged

`ControlServiceProvider` and app `client.get` are untouched. Observed config is
simply the NVS blob already maintained by the `set` drain, surfaced upward in the
report.

## Convergence timing

Observed config refreshes only on a **poll** wake (when the report is PUT).
Immediately after a `set`, `device get` shows `(pending)` until the node's next
poll drains the command, writes NVS, and reports back. This lag is inherent and
correct: it is exactly what makes the echo a real convergence signal rather than
a restatement of the desired log.

## Testing (host suites, mirror existing patterns)

- **`device/report_test.toit`** — `build-report --config=…` includes the `config`
  object; empty config emits `{}`.
- **`gateway/handler_test.toit`** — `ReportWriter_` round-trips a report body's
  `config` into `observed_state`; a body with no `config` yields an empty config
  in the stored blob.
- **`device get` rendering test** (in `gateway/` alongside the command tests) —
  covers the four cases: agreement (no marker), `(drift)`, `(pending)`, and
  observed-without-desired.

No `toit test` on this SDK — each test is a `main` with `import expect show *`,
run via `toit run`.
