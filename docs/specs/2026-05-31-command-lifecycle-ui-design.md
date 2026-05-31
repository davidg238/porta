# Command lifecycle badges + operator-UI reorg — design

**Date:** 2026-05-31
**Status:** Approved (brainstorm complete)
**Scope:** `internal/web`, `internal/control`, `internal/store` (Go gateway operator surface only)
**No change to:** wire protocol, `docs/PROTOCOL.md`, DB schema, node firmware, telemetry ingestion (B3)

## Problem

The node-detail operator page (`/n/<id>`) has three rough edges the operator hit in use:

1. **Action verbs are inconsistent and static.** Some buttons say `queue` (set, console,
   poll-interval, install, uninstall), others say `set` (max-offline, rename). The split is
   *correct* — `queue` actions go into the command queue for delivery to the node on its next
   check-in; `set` actions are applied immediately in the gateway's own `nodes` row — but the UI
   never shows a queued command's *progress*. The "Pending commands" section lists only
   undelivered rows, which vanish the instant the node pulls them, so the operator never sees a
   command move from queued → delivered → applied.

2. **Gateway-local vs node-directed actions are visually intermixed.** `max-offline` and `rename`
   change gateway state (no node round-trip) but sit in the same "Actions" block as the queued
   node commands, obscuring where each action actually takes effect.

3. **Telemetry clutters the node page and renders confusingly.** Each node report writes *two*
   `data_log` rows at the same timestamp — a `kind='metric'` row (`pm25`) and a `kind='log'` row
   whose content lives in the `text` column. The node page's "Telemetry · last 10" table renders
   only `name`/`value`/`value_type`, so every `log` row shows as a blank line and "last 10" is
   really "last 5 readings × 2 rows." Telemetry is also a *freebie* (not core gateway function)
   and may be removed from porta later, so it should be cleanly excisable.

## Goals

- Show each queued command's **delivery lifecycle** on the node page, derived entirely from data
  the gateway already stores — no protocol/schema/firmware change, no node ACK/NACK.
- Visually separate **gateway-local** actions (max-offline, rename) from **node-directed** queued
  commands, by moving the gateway-local ones into the banner behind an edit toggle.
- Move telemetry off the node page onto a **global `/telemetry` page** that shows metrics only
  (no blank `log` rows), and keep that page self-contained so telemetry can be excised in one
  cleanly-bounded change later.

## Non-goals

- No node ACK/NACK. A true per-verb `failed` state is **out of scope** (the protocol can't produce
  it without a firmware/wire change). We surface only states derivable gateway-side.
- No convergence detection for non-`set` verbs. `delivered` is terminal for everything except
  `set` (only `set` has an observed-state echo to reconcile against).
- No removal of `data_log` ingestion. This is a display-layer change only.

## Design

### 1. Command lifecycle model (derived, gateway-only)

A command's state is **computed at render time** from rows already in the store — no new columns.

| State | Derivation | Applies to |
|---|---|---|
| `queued` | `delivered_at IS NULL` and `now − issued_at < max_offline_s` | all verbs |
| `delivered` | `delivered_at IS NOT NULL`, and not (yet) `converged` | all verbs |
| `converged` | `verb == "set"` **and** the command's `(app,key)→value` now matches the node's observed config echo | `set` only |
| `expired` | `delivered_at IS NULL` and `now − issued_at ≥ max_offline_s` | all verbs |

- **`delivered` is terminal** for every verb except `set`. The other verbs (console, poll-interval,
  install/run, uninstall/stop) have no observed-state to reconcile against, so the lifecycle stops
  at `delivered`.
- **`converged`** reuses the existing config reconcile path: the node echoes applied config in its
  report (D5), which the gateway already folds into `observed_state`. The lifecycle compares the
  command's desired `(app,key)→value` against that observed config using the existing
  `internal/config` scalar-equality logic (`EqualScalars`). When they match, the `set` command is
  `converged`.

**Expiry window = `node.max_offline_s`.** If a command sits unpulled longer than the node's own
"I'd consider it offline" threshold, it is `expired`. Documented caveat: the node pulls **one
command per check-in, oldest-first** (`NextUndelivered`), so a command queued deep behind others
can read `expired` while legitimately waiting its turn. This is acceptable for an operator view
(queuing 10+ commands at once is rare) and is noted in the UI/spec rather than engineered around.

**Computation site — `internal/control`:** a pure function

```
LifecycleOf(cmd store.Command, observedConfig map[app]map[key]value, now int64) State
```

- Input: the command row, the node's parsed observed config (already available where the config
  view is built), and `now`. `max_offline_s` comes from the node row.
- Output: one of `queued | delivered | converged | expired`.
- Pure and table-test friendly: every state + the expiry boundary + converged-via-config-match are
  unit-tested in isolation. Lives in a new `internal/control/lifecycle.go`.

### 2. Node-detail page: "Recent commands" replaces "Pending commands"

- The `node-pending` section (undelivered-only) is **replaced** by `node-recent`: the **last 10
  commands for this node by issue time** (delivered or not), each with a lifecycle **badge**.
- Refreshes on the existing **2s poll** (`hx-get` → `outerHTML` swap), so a command visibly advances
  `queued → delivered → converged` and old ones age out as new commands push past N=10.
- New additive store read `RecentCommandsForDevice(deviceID string, limit int) ([]Command, error)`
  — sibling to the existing `UndeliveredCommands` / `RecentCommands`; orders by `id DESC LIMIT ?`.
- **All action forms** that currently `hx-target="#pending"` (set, console, poll-interval,
  container install, container uninstall) **retarget to `#recent`**, so a just-queued command
  appears immediately as `queued`.
- Badge styling reuses existing color classes: `queued`=amber, `delivered`=blue, `converged`=green,
  `expired`=red. One small CSS block in `internal/web/assets`.

### 3. Banner: gateway-local actions behind an edit toggle

- A native `<details><summary>edit</summary> … </details>` block holding the `max-offline` and
  `rename` forms, placed in the banner **as a sibling of `#hdr`, _outside_ the 2s-polled region**.
  - Rationale: `#hdr` refreshes every 2s with `outerHTML`; if the toggle/forms lived inside it, the
    poll would collapse the `<details>` mid-edit. Keeping them in a non-polled sibling avoids that.
- The two forms keep their existing `hx-post` endpoints and `hx-target="#hdr"`, so submitting a
  rename / max-offline change refreshes the header (name, gauge thresholds) as it does today.
- Zero JavaScript; works on mobile (native disclosure widget).
- The "Actions" section now contains **only node-directed (queued) commands**, making the
  gateway-local-vs-node-directed distinction visually explicit.

### 4. Global `/telemetry` page (metrics-only)

- New top-nav entry: `porta · Nodes · Telemetry · Command Log` (one link added in `base.html`).
- New page `telemetry.html` + handler `handleTelemetry` in `pages.go` + a polled partial in
  `partials.go`. Table columns: `time · node · name · value · type`.
- **Metrics-only:** the backing store reads filter `kind='metric'`, which is what removes the blank
  `log` rows. `log`-kind rows are not shown on this page.
- Optional per-node filter via `?node=<id>`.
- New store reads:
  - `RecentMetrics(limit int) ([]Metric, error)` — across all nodes, `kind='metric'`, `ts DESC`.
  - `RecentMetricsForDevice(deviceID string, limit int) ([]Metric, error)` — same, filtered by node.
  - (A single method with an optional device filter is acceptable if cleaner.)
- Polls on the same cadence as other partials.

### 5. Node-detail page: telemetry removed, link added

- **Remove** the `node-telemetry` template section, its `/n/<id>/telemetry` partial route, and its
  handler.
- Add a **"Telemetry →"** link in the node header pointing to `/telemetry?node=<id>`.

### 6. Excise-ability of telemetry

When telemetry is later removed from porta, the operator-surface footprint to delete is bounded:

1. `internal/web/templates/telemetry.html` (delete)
2. `handleTelemetry` + telemetry partial in `pages.go` / `partials.go` (delete)
3. The `/telemetry` and `/partials/telemetry` route registrations in `web.go` (delete)
4. The "Telemetry" nav link in `base.html` (delete)
5. The "Telemetry →" link in `node.html` (delete — one line)
6. `RecentMetrics` / `RecentMetricsForDevice` in `store.go` (delete if unused elsewhere)

`data_log` ingestion (B3) and its schema are **not** part of this footprint and remain untouched.

## Files touched

| File | Change |
|---|---|
| `internal/control/lifecycle.go` | **new** — `LifecycleOf` pure function + `State` type |
| `internal/control/view.go` | recent-commands view rows (verb/args/badge), telemetry view rows |
| `internal/store/store.go` | **new reads** `RecentCommandsForDevice`, `RecentMetrics`, `RecentMetricsForDevice` |
| `internal/web/templates/node.html` | drop `node-telemetry`; `node-pending`→`node-recent` with badges; gateway-settings `<details>` sibling of `#hdr`; "Telemetry →" link; action forms retarget `#pending`→`#recent` |
| `internal/web/templates/telemetry.html` | **new** — global metrics-only telemetry page + partial |
| `internal/web/templates/base.html` | add "Telemetry" nav link |
| `internal/web/pages.go` | `handleTelemetry`; node page wiring (recent commands replaces pending) |
| `internal/web/partials.go` | node recent-commands partial; telemetry partial; remove node-telemetry partial |
| `internal/web/web.go` | register `/telemetry`, `/partials/telemetry`; remove `/n/<id>/telemetry` partial route |
| `internal/web/assets/` | badge CSS classes |

## Testing (TDD)

- **`internal/control`** — `LifecycleOf` table tests: `queued`, `delivered`, `expired` (boundary at
  `max_offline_s`), `converged` (config match), and the non-`set`-stops-at-`delivered` rule.
- **`internal/store`** — `RecentCommandsForDevice`, `RecentMetrics`, `RecentMetricsForDevice`
  (ordering, limit, `kind='metric'` filter excludes `log` rows).
- **`internal/web`** — render tests: recent-commands section shows correct badge per state; action
  forms target `#recent`; banner `<details>` renders the two gateway forms outside `#hdr`;
  `/telemetry` page renders metrics only (no blank rows) and honors `?node=` filter; node page no
  longer renders a telemetry section but does render the "Telemetry →" link.

## Open items / accepted caveats

- **`expired` false-positive** for commands queued deep behind others (one-per-check-in,
  oldest-first). Accepted; noted in UI copy if space allows.
- **No `failed` state.** Requires a future protocol ACK/NACK extension (separate cross-repo effort).
  The lifecycle model and UI are shaped so a real per-command status could slot in later without
  rework, but that work is explicitly out of scope here.
