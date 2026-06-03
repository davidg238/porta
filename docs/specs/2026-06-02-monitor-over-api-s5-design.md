# S5 — `porta monitor` over the control-plane API — design

Part of the **control-plane-API** series (server-as-workhorse + network JSON API;
S1–S4 shipped). S5 re-points `porta monitor` — the telemetry / forwarded-console
tail — from reading the local store to reading **over the HTTP API**, so it works
against a remote gateway with no USB and no separate `jag monitor`. It is the last
read-ish command (besides `serve`) still db-backed.

## 1. Goal & non-goals

**Goal:** `porta monitor [-d <sel>] [--since 1h] [--kind log|metric] [--follow]`
reads a node's `data_log` over `GET /api/nodes/{sel}/telemetry` instead of opening
the store. Identical operator experience to today (windowed read; `--follow` polls
and tails), but remote-capable and over the single-writer server.

**Non-goals (explicitly deferred):**

- **No server-push / SSE.** Decided in brainstorm: nodes emit telemetry on their
  poll/wake cadence (tens of seconds to ~60s), so the data is inherently bursty and
  arrives well above a 2 s client poll. SSE would make the transport live while the
  data is not — real fan-out/backpressure/keepalive/reconnect infra for a benefit the
  node cadence erases. A windowed GET the CLI polls gives the identical experience.
  Revisit only on a concrete "feels laggy" need.
- **MCP #10/#11 stay out of scope.** The store layer is already correct (`QueryData`
  treats `until<=0` as unbounded; `QueryDataLimited` pushes `LIMIT` into SQL). Both
  issues are now mcpsrv-only routing touch-ups unrelated to S5's new endpoint; they
  remain their own follow-up.
- The full `device get` node-detail read stays db-backed (S2 deferral); not pulled in.

## 2. Cursor model (the one correctness decision)

Today's `--follow` advances a **timestamp watermark** (`since = last+1`), which has a
known boundary edge case: rows sharing a poll-tick second can be dropped or
double-counted (current spec §7 accepts it). S5 replaces it with an **exact rowid
cursor**:

- `data_log` is an ordinary rowid table; `InsertData` appends, so `rowid` is
  monotonic in insertion order — exactly "what arrived since I last looked."
- The **initial window** read is still time-based (`since = now - sinceS`), and each
  returned row carries its `rowid`. The client remembers `cursor = max(rowid)`.
- Each **`--follow` poll** requests rows with `rowid > cursor`, ordered by `rowid`,
  and advances the cursor. No ties, no boundary case — the edge case is eliminated,
  not carried over.

## 3. HTTP endpoint

`GET /api/nodes/{sel}/telemetry` on the existing B4a allowlisted listener, registered
in `apisrv.Handler.Register`, wrapped with `recoverer` (panic → 500 envelope, §6
parity). Selector resolved server-side via `resolveSel` (read-only; **no**
`EnsureNode`). Response is the standard `{ok,data,error}` envelope with
`data = {"rows": [...]}`.

Two modes on one endpoint, selected by whether `after` is present:

| Param    | Type        | Meaning                                                        |
|----------|-------------|---------------------------------------------------------------|
| `after`  | int (rowid) | **Cursor mode.** Return rows with `rowid > after`, ordered by `rowid`. When present, `since`/`until` are ignored. |
| `since`  | int (epoch) | **Window mode.** Lower bound `ts >= since`. Default 0.        |
| `until`  | int (epoch) | Window upper bound `ts <= until`; `0`/omitted = unbounded.    |
| `kind`   | string      | Optional `log`\|`metric` filter (both modes).                 |
| `limit`  | int         | Optional SQL row cap (both modes); `0`/omitted = no cap.      |

Malformed integer params → `400` envelope. Unknown selector → `404` (resolveSel).

Each row in `rows`:

```json
{ "rowid": 1234, "ts": 1717370000, "seq": 3, "kind": "metric",
  "name": "pm25", "value": 7, "text": "", "value_type": "int" }
```

`value` is the typed scalar (number for int/float/bool, omitted/null for string &
log rows where the payload is in `text`). `value_type` drives client-side rendering.

## 4. Store layer (`internal/store/data.go`)

- Add `Rowid int64` to `DataRow`. Have `QueryDataLimited` also `SELECT rowid` and
  populate `r.Rowid`. Additive and benign — existing consumers ignore the new field;
  the window response now carries rowids.
- New `QueryDataAfter(deviceID string, after int64, kind string, limit int)
  ([]DataRow, error)`:
  `SELECT rowid, ts, seq, … FROM data_log WHERE device_id=? AND rowid > ?
   [AND kind=?] ORDER BY rowid [LIMIT ?]`. Mirrors `QueryDataLimited`'s scan/normalize.

No schema change, no migration ([[porta-no-legacy]]).

## 5. apiserver handler (`internal/apisrv/`)

New `telemetry.go` with `handleTelemetry`:

1. `id, ok := h.resolveSel(w, sel)`; bail on `!ok`.
2. Parse `after`/`since`/`until`/`limit` ints (bad → `writeErr 400`), `kind` string.
3. If `after` was supplied → `st.QueryDataAfter(id, after, kind, limit)`; else
   `st.QueryDataLimited(id, since, until, kind, limit)`.
4. Map `[]store.DataRow` → `[]telemetryRow` DTO; `writeOK(w, map[string]any{"rows": out})`.

`telemetryRow` is a JSON DTO (`rowid,ts,seq,kind,name,value,text,value_type`) so the
wire shape is owned by apisrv, not store.

## 6. apiclient (`internal/apiclient/client.go`) — stays store-free

- Wire type `apiclient.DataRow` mirroring the row (incl. `Rowid`). `value` decoded
  with a `json.Number`-safe path coerced by `value_type` (reuse the S1 `coerceScalar`
  shape) so int/float/bool/string typing survives the round-trip; large int64s keep
  precision.
- `QueryTelemetryWindow(sel string, since, until int64, kind string, limit int)
  ([]DataRow, error)` and `QueryTelemetryAfter(sel string, after int64, kind string,
  limit int) ([]DataRow, error)`. Both build a `GET` with `url.Values`, call `c.do`,
  unmarshal `{"rows":[…]}`. Errors surface like the other client methods (transport →
  "is `porta serve` running?"; non-2xx/`ok:false` → server error string).

## 7. CLI (`internal/portacli/monitor.go`)

- Drop `openStore()`/`--db`; add `--server`/`$PORTA_SERVER` (default
  `localhost:6970`) like the mutating commands. `resolveNodeID(st, …)` is gone —
  the selector is sent raw; the server resolves it (a 404 surfaces as a clean error).
- `runMonitor` signature changes: replace `st *store.Store` with a small client
  interface (for test injection). Flow:
  1. Window read `QueryTelemetryWindow(sel, now-sinceS, now, kind, 0)`; print each via
     `telemetry.FormatLine`; set `cursor = max(rowid)` (0 if empty).
  2. If `!follow`, return.
  3. Ticker every 2 s: `QueryTelemetryAfter(sel, cursor, kind, 0)`; print; advance
     `cursor`. `ctx.Done()` → return `nil` on `context.Canceled` (Ctrl-C via
     `Execute`'s `NotifyContext`), else the ctx error. Unchanged UX.
- **Format parity:** `telemetry.FormatLine(store.DataRow)` is unchanged. portacli maps
  each `apiclient.DataRow → store.DataRow` (it imports both); identical output lines.

## 8. Error handling

- Unknown selector → server `404` → CLI prints the server error string.
- No `serve` running → transport error with the "is `porta serve` running?" hint.
- Bad params (shouldn't happen from the CLI) → `400`. Panics → `500` via `recoverer`.

## 9. Testing

- **store:** `QueryDataAfter` rowid filter + ordering + `kind` + `limit`; `Rowid`
  populated by the window query; empty-result case.
- **apisrv:** window mode, cursor mode (after-takes-precedence), `kind`/`limit`,
  malformed param → 400, unknown selector → 404, value-typing round-trip
  (int/float/bool/string/log row).
- **apiclient:** decode `{"rows":[…]}`, value typing per `value_type`, error
  propagation.
- **portacli:** `runMonitor` one-shot + follow with an injected fake client, fake
  clock + injectable tick; assert rowid dedup (no dup across polls) and
  ctx-cancel → nil.
- **e2e:** cobra `monitor --server` against a real `apisrv` over `httptest` (mirrors
  S2's `mutate_test`/`e2e_test`), one-shot path.

## 10. Out of scope / follow-ups

- SSE streaming (see §1) — revisit only on a concrete latency complaint.
- MCP #10/#11 mcpsrv routing touch-ups — separate follow-up.
- S6 (client-side panic decode) consumes this console-tail read next.
