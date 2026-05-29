# porta B4b — MCP read surface: design

**Status:** approved design (brainstormed 2026-05-28). Last sub-project of the B4
operator surface. B4a (multi-listener serve) and B4c (htmx operator UI) already
shipped; this layers an MCP read surface on the same HTTP listener.

## 1. Context

After B4a (`internal/httpsrv`: CIDR-allowlisted HTTP listener, default
RFC1918+loopback, `--http-port 6970`, `--http-port 0` disables) and B4c
(`internal/web` mounted at `/` plus the shared cobra-free `internal/control`
read+write layer), the gateway has a browser operator console but no
machine/agent surface. B4b adds one: read-only MCP tools so an MCP client
(Claude Code / Claude Desktop) can inspect the fleet.

This is greenfield — there is no Toit-gateway parity port. The parked
`internal/st/mcpserver` (17 SSE tools for ST/berry nodes) is prior art only; its
synchronous `WaitForVerb` model does not apply to poll-based Toit nodes, and it
uses the deprecated SSE transport with hand-written `json.RawMessage` schemas.

## 2. Locked umbrella decisions (B4 brainstorm, 2026-05-28)

- MCP read-only first; write surface is a later sub-project.
- Streamable HTTP transport (not SSE, not stdio).
- Single `porta serve` process; mount on the B4a allowlisted HTTP listener.
- `kind` is exposed but not branched on (Toit-only today).

## 3. B4b decisions (this brainstorm)

- **Tool list: 6, full read parity** with the CLI/web read surface —
  `list_devices`, `device_status`, `device_get_config`, `container_list`,
  `query_telemetry`, `command_log`.
- **Result format: structured JSON + text summary.** Each tool returns a typed
  `Out` struct (the SDK derives an output schema) plus a one-line human-readable
  text summary.
- **Enablement: always-on at `/mcp`** whenever the HTTP listener is up
  (`--http-port ≠ 0`). No new flag, no new listener. Inherits the B4a CIDR
  allowlist and lifecycle.
- **`query_telemetry` bounds:** `limit` default 100, hard cap 1000; optional
  `since`/`until` (epoch seconds) and `kind` filter.

## 4. Goals / non-goals

**Goals:** expose the existing `internal/control` + `store` read surface as 6
MCP tools over Streamable HTTP at `/mcp`; thin adapters only; no new query
logic; no writes.

**Non-goals:** any write/mutate tool (`set`, `install`, `uninstall`, rename) —
deferred to a later sub-project; SSE/stdio transport; auth beyond the inherited
CIDR allowlist; `kind` branching; store-schema or wire-protocol changes; live
`monitor --follow` semantics (MCP is range/snapshot only).

## 5. Architecture

New package **`internal/mcpsrv`** (sibling to `internal/web`). One exported
entry point:

```go
func Register(mux *http.ServeMux, st *store.Store, now func() int64)
```

It constructs an `mcp.Server`, registers the 6 tools via the SDK generics API
`mcp.AddTool[In, Out]` (auto-derives JSON input/output schemas from typed Go
structs), wraps the server in an `mcp.StreamableHTTPHandler`, and mounts it at
`/mcp` on the same mux `internal/web` uses.

`cmd/porta serve` calls `mcpsrv.Register(...)` immediately after
`web.Register(...)`, unconditionally when the HTTP listener is created. CIDR
allowlist, bind address, and graceful-shutdown lifecycle come from B4a — B4b
adds no flags and no listener.

Every handler is a thin adapter:

1. parse the typed `In` struct (SDK-unmarshalled);
2. resolve the node via `control.ResolveNodeID` (accepts MAC **or** friendly
   name — same as CLI/web) where the tool takes a `device`;
3. call the existing `internal/control` / `store` read;
4. map to the typed `Out` struct and attach a one-line text summary.

Dependency `github.com/modelcontextprotocol/go-sdk v1.4.1` is already in
`go.mod` (transitively, via the parked `st-devserver`) — no new dependency.

## 6. The 6 tools

All tool names are snake_case. Inputs marked `device` accept a MAC (12 lowercase
hex) or a friendly name. `online` is computed as `(now - last_seen_s) ≤
max_offline`, the same rule the web fleet list uses. Time fields are epoch
seconds.

| Tool | Input | Backed by | Output (structured) |
|---|---|---|---|
| `list_devices` | *(none)* | `store.ListNodes` + `control.RelativeAge` | `[]{id, name, kind, source_addr, last_seen_s, age, online}` |
| `device_status` | `device` | `store.GetNode` + `store.UndeliveredCommands` | `{id, name, kind, source_addr, last_seen_s, age, online, observed_state (raw JSON string), undelivered_count}` |
| `device_get_config` | `device`, optional `app` | `control.DesiredVsObserved` | `[]{app, key, desired, observed, marker, reissue_count}` (all apps if `app` omitted) |
| `container_list` | `device` | `control.AppsFromObserved` | `[]{name, crc, runlevel}` |
| `query_telemetry` | `device`, optional `since`, `until`, `kind`, `limit` | `store.QueryData` (when `since`/`until` given) else `store.RecentData` | `[]{ts, seq, kind, name, value, text, value_type}`, most-recent-first |
| `command_log` | optional `device`, optional `limit` | `store.CommandLog` (when `device` set) else `store.RecentCommands` | `[]{id, device, verb, args, issued_by, issued_at, delivered_at}` |

Notes:

- `device_get_config` with no `app` returns rows for **all** apps; each row
  carries its `app`. "All apps" = the observed installed containers
  (`control.AppsFromObserved`), each fed through `control.DesiredVsObserved` —
  the same enumeration the web per-node detail config panel uses
  (`internal/web/pages.go`). (The CLI `device get` always takes an explicit app;
  there is no no-app CLI mode to mirror.) With an explicit `app`, only that
  app's rows are returned.
- `command_log` with no `device` is the fleet-wide audit (the B4c `/log` view,
  `RecentCommands`); with `device` it is that node's full command log
  (`CommandLog`).
- `query_telemetry` and `command_log` clamp `limit` into `[1, 1000]`; absent or
  `≤0` → default 100.
- `health` is intentionally **not** exposed: the `reports.health` column is
  write-only today (no `Node` field, no store getter, not shown by CLI/web).
  Surfacing it would need new query logic, which is out of scope here. A
  `latest_report_health` read can be added later if an operator wants it.

## 7. Error handling

Handlers never panic. Failures (unresolvable `device`, store errors) return an
error `CallToolResult` (`IsError: true`) carrying the message; the SDK marshals
it to the client. Out-of-range `limit` is clamped, not rejected. `since` later
than `until` yields an empty result, not an error.

## 8. Testing

1. **Per-handler unit tests** — in-memory store (existing test helper), seed
   nodes/reports/data/commands, call each handler, assert the typed `Out`
   fields. Cover: limit clamping, MAC-vs-name resolution, all-apps vs single-app
   config, online/offline boundary, unresolvable-device error, fleet-wide vs
   per-device `command_log`.
2. **One round-trip integration test** — wire the server to the SDK's in-memory
   client/server transport; list tools (assert all 6 present with correct input
   schemas); call one tool end-to-end to prove StreamableHTTP registration +
   schema derivation work.

## 9. Acceptance

With `porta serve --http-port 6970` running, an MCP client pointed at
`http://<gw>:6970/mcp` lists 6 tools, and `list_devices` / `query_telemetry`
return live fleet data. CIDR allowlist enforced (request from a non-allowed
source is rejected by the inherited B4a middleware).

## 10. Implementation tasks (for the plan; ~6)

1. `internal/mcpsrv` package skeleton + `Register` mounting an empty
   `StreamableHTTPHandler` at `/mcp`; wire into `cmd/porta serve`; round-trip
   integration test asserts the endpoint is reachable and lists 0 tools.
2. `list_devices` + `device_status` (typed In/Out, text summary, unit tests).
3. `device_get_config` + `container_list` (all-apps vs single-app; unit tests).
4. `query_telemetry` (since/until vs recent, kind filter, limit clamp; tests).
5. `command_log` (fleet-wide vs per-device, limit clamp; tests).
6. Integration test: list all 6 tools + assert input schemas; end-to-end call;
   final whole-impl review.

## 11. References

- `internal/control/{control.go,view.go}` — shared read helpers
  (`ResolveNodeID`, `RelativeAge`, `AppsFromObserved`, `DesiredVsObserved`).
- `internal/store/{store.go,data.go}` — read methods.
- `internal/httpsrv/server.go` — B4a listener + CIDR middleware.
- `internal/web/web.go` — mount pattern this mirrors.
- `internal/st/mcpserver/server.go` — parked prior art (SSE, not reused).
- `github.com/modelcontextprotocol/go-sdk v1.4.1` — MCP SDK (already a dep).
- `docs/brainstorms/2026-05-28-porta-b4-operator-surface-kickoff.md` — B4 kickoff
  (umbrella decisions, B4 decomposition).
