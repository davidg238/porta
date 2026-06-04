# Neutral `reset` reason in the report, surfaced by porta

**Status:** approved (brainstorm 2026-06-04)
**Coordinates with:** nodus PR #7 / nodus issue #4 ("include reset-reason in the health report")

## Problem

A node's per-wake `health` report carries no reset cause, so porta cannot tell a
HW-watchdog reset, panic, or brownout from a normal boot or deep-sleep wake. On
2026-06-04 HW fault-injection (fwkb, check 4) a forced supervisor loop-hang triggered
a task-watchdog reset (`esp32.reset-reason == 6`); the node rebooted but the report
conveyed nothing — porta saw at best an uptime reset.

nodus PR #7 proposed adding `health.reset_reason` as the **raw esp32 `RESET-*` enum
int**. porta owns the wire protocol and is a **neutral, language/hardware-agnostic**
control plane for a *heterogeneous* fleet (Toit/esp32 on WiFi today; Smalltalk
`nodus-st` on nRF52840/Zephyr over Thread, proven). A raw esp32 enum on the wire would
make porta either embed esp32 semantics (to label it) or display an integer it can't
interpret — and would mislabel a Zephyr node, whose reset codes differ. So the wire
value is defined as a **neutral category**, mapped by each node from its own platform
codes.

## Wire protocol (canonical — `docs/PROTOCOL.md`)

The report `health` block carries a neutral reset **category string** plus an optional
raw platform code:

```jsonc
"health": {
  "uptime_us": 1000000,
  "wakes": 7,
  "reset": "watchdog",   // neutral category — REQUIRED once a node implements this
  "reset_code": 6        // OPTIONAL raw platform code (diagnostic; porta never interprets)
}
```

### Canonical vocabulary

The only values a conforming node may send for `reset`:

| Category     | Meaning                                   | Fault? |
|--------------|-------------------------------------------|--------|
| `power-on`   | cold / power-on reset                     | no     |
| `deep-sleep` | wake from deep sleep (normal duty wake)   | no     |
| `software`   | software-requested reboot                 | no     |
| `external`   | external / reset-pin                      | no     |
| `watchdog`   | watchdog timeout (task or HW)             | **yes**|
| `panic`      | software panic / exception                | **yes**|
| `brownout`   | supply-voltage dip                        | **yes**|
| `unknown`    | unmapped / unavailable                    | no     |

Each node maps its own platform codes onto this set (esp32 `RESET-*` → here; Zephyr
does its own). `reset_code` is opaque to porta — surfaced only for diagnostics.

`reset`/`reset_code` are additive and optional on the wire: a report without them is
valid (porta keeps the last known value), matching the chip/sdk identity treatment.

## porta ingest + store

Mirrors the existing chip/sdk node-identity precedent (`UpdateNodeIdentity`):

- **Schema:** add `nodes.last_reset TEXT` and `nodes.last_reset_code INTEGER`. Per
  `porta-no-legacy` (pre-1.0, no migrations) the DB is recreated, not `ALTER`'d — the
  same way the chip/sdk columns were added.
- **Ingest (`internal/handler/handler.go:writeReport`):** the `health` blob is already
  archived verbatim in `reports`. Additionally parse `health.reset` (string) and
  `health.reset_code` (int, optional) and call a new
  `store.UpdateNodeReset(id, reset, code)` — `COALESCE`-guarded so a report missing the
  field never clobbers the stored value.

## Surfacing

### Node detail (chip/sdk path)

`store.Node` gains `LastReset string` / `LastResetCode sql.NullInt64`, flowing into:

- web `detailVM` (`internal/web/pages.go`) + the detail template,
- API `nodeDetail` JSON (`internal/apisrv/nodes.go`),
- CLI `device show` (over the API).

Rendered as **`watchdog (6)`** — category plus raw code when present; category alone
when no code. porta does **no** enum mapping: it displays the node's neutral string
verbatim. A node that has never reported `reset` shows nothing / `—`.

### Telemetry event on fault reset

In `writeReport`, when the incoming category is in the **fault set**
(`{watchdog, panic, brownout}`) **and differs from the node's stored `last_reset`**,
insert exactly one `data_log` row *before* updating the column:

```
kind="reset", name=<category>, value=<reset_code or NULL>, text=<category>, value_type=("int" when code present, else "")
```

(`kind="reset"` is the tag the telemetry tail / queries select on; `value_type`
follows the existing `data_log` convention of describing the stored `value`'s type.)

It then appears in `porta monitor` and the audit trail.

- **Change-detection dedup:** emit only on a category transition, not on every poll. A
  deep-sleep node reports `deep-sleep` on normal wakes and flips to a fault category
  only the wake after a real fault, so each fault is logged once per occurrence.
- **Fault set** is a small porta constant — this is *policy* ("which categories are
  noteworthy"), not platform semantics, so it is neutral for porta to own.
- **Known limitation (accepted, YAGNI):** two identical fault reboots with no
  successful normal report in between are logged only once. Documented, not engineered
  around.

## nodus handoff

The wire shape differs from PR #7's raw int, so porta (protocol owner) drives the
change. Handoff (comment on nodus PR #7): emit `health.reset` as the neutral category
string — node maps `esp32.reset-reason` (`1→power-on`, `4→panic`, `6→watchdog`,
`7→watchdog`, `8→deep-sleep`, brownout code → `brownout`, else `unknown`) — plus
optional `health.reset_code` carrying the raw int. `build-report` stays a pure,
host-testable function (the strings/int are passed in, as `uptime_us`/`wakes` are).

## Testing

- **Store:** `UpdateNodeReset` COALESCE behavior (empty arg never clobbers); round-trip
  read of `last_reset` / `last_reset_code`.
- **Handler:** report with a fault `reset` inserts exactly one `data_log` row; an
  immediately-repeated identical report inserts none; a `fault → normal → fault`
  sequence inserts again; a report missing `reset` no-ops both column and event; a
  non-fault category (`power-on`, `deep-sleep`) never emits an event.
- **Render:** `device show` / web detail / API JSON show `category (code)` when a code
  is present and `category` alone when not; absent → blank/`—`.
- **Docs:** `docs/PROTOCOL.md` report-shape section updated; existing report-shape
  tests updated for the new health keys.

## Out of scope

- Fleet-list reset badge (considered; not chosen — node detail + telemetry event cover
  the need).
- porta interpreting `reset_code` or carrying any platform enum table.
- Historical reset timeline beyond the single `last_reset` column + the `data_log`
  events already captured.
