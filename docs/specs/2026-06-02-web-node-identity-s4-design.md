# S4 — Web node identity (chip/SDK) visibility (Design)

**Status:** Approved 2026-06-02. Part of the server-as-workhorse / CLI-as-client
re-architecture (S1–S6). Independent of S1–S3 (purely a read-display change in the
operator web UI).

## 1. Goal

Surface each node's self-reported `chip` and `sdk` in the operator web UI's per-node
detail header. **Identity display only — no match badge.** The authoritative
"build SDK vs node SDK" check stays in `porta run` (S3), where the local build SDK
(`toit version`) actually lives; the gateway hosting the web UI has no `toit`/`jag`/SDK
and therefore nothing authoritative to match against.

## 2. Background / why no badge

The decomposition originally sketched a "✓/✗ SDK-match badge", but the web UI runs
inside `porta serve` on the gateway, which has no local toolchain. There is no
authoritative reference SDK on the gateway to compare a node against, so a match
badge would be inventing a semantic the gateway can't own. Decision (brainstorm
2026-06-02): show the reported identity, full stop. Fleet-consistency or
configured-target-SDK badges were considered and explicitly deferred (§6).

The data is already present: `store.Node` carries `Chip` and `Sdk` (the `/tools/toit`
Phase-1 columns, populated from the report by `handler.writeReport`). Nothing on the
store, protocol, or server side changes.

## 3. Design

### 3.1 View-model (`internal/web/pages.go`)

Add two string fields to `detailVM`:

```go
Chip string
Sdk  string
```

Populate them in the `detailVM(n *store.Node)` builder (the existing `return detailVM{...}`
literal) from the node row:

```go
Chip: n.Chip,
Sdk:  n.Sdk,
```

No other view-model changes; no new store reads (the `*store.Node` already has both).

### 3.2 Template (`internal/web/templates/node.html`, `node-header` partial)

Extend the subtitle line. Current:

```html
<p class="subtitle">{{.Kind}} · {{.IP}} · eui {{.EUI}} · poll {{.PollIntv}} · <a href="/telemetry?node={{.ID}}">Telemetry →</a></p>
```

New — insert chip/sdk before the Telemetry link, with a graceful fallback for a node
that has not yet reported identity (empty `Chip`/`Sdk`):

```html
<p class="subtitle">{{.Kind}} · {{.IP}} · eui {{.EUI}} · poll {{.PollIntv}} · {{if .Chip}}{{.Chip}}{{else}}chip ?{{end}} · sdk {{if .Sdk}}{{.Sdk}}{{else}}—{{end}} · <a href="/telemetry?node={{.ID}}">Telemetry →</a></p>
```

- A reporting node shows e.g. `esp32 · sdk v2.0.0-alpha.192`.
- A node that has not reported identity shows `chip ? · sdk —`.

The `node-header` partial is one of the existing 2s-polled sections (`hx-get="/n/{{.ID}}/header"`),
so it refreshes automatically once a node reports identity — no extra wiring, no new route.

## 4. Data flow

Report → `handler.writeReport` → `store.UpdateNodeIdentity` (already shipped, Phase-1)
→ `store.Node.Chip/Sdk` → `web.detailVM` → `node-header` template → polled into the
page every 2s. S4 only adds the last two hops (view-model fields + template render).

## 5. Testing

In `internal/web` (mirroring the existing partial-render tests):

- Render the `node-header` partial for a node with `Chip="esp32"`, `Sdk="v2.0.0-alpha.192"`
  → assert both `"esp32"` and `"v2.0.0-alpha.192"` appear in the output.
- Render for a node with empty `Chip`/`Sdk` → assert the `"sdk —"` fallback renders
  (and the `"chip ?"` fallback), and the render does not error.

Host-only; no device, no real toit. (If the existing tests render the full `node`
page rather than the `node-header` partial directly, follow that convention instead
and assert on the full-page output.)

## 6. Scope / non-goals

- **In scope:** two `detailVM` fields + the `node-header` subtitle edit + the render test.
- **Out of scope (deferred):** fleet-list (`/`) SDK column; CLI (`porta device status`/scan)
  and MCP (`device_status`) identity parity; any ✓/✗ SDK-match badge; a
  fleet-consistency/outlier indicator; a configured-target-SDK (`--expected-sdk`)
  setting. None are needed for identity visibility and each can be added later
  independently.
