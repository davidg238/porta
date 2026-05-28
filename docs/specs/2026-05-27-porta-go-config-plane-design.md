# porta Go core — B2 config plane (design)

**Status:** approved, ready for implementation plan
**Sub-project:** B2 of the Go-mainline renovation (B1 shipped `@cf2e958`)
**Charter (parity port):** the `set` verb, per-app config, observed-config echo,
self-heal reconcile (issued_by tagging, in-flight guard, ≥2× drift warning), and
`device set`/`device get` CLI — at parity with the reference impl in
`examples/toit-gateway` (see prior Toit-side specs
`docs/specs/2026-05-24-config-self-heal-design.md` and
`docs/specs/2026-05-24-d5-observed-config-echo-design.md`).

This spec covers the **Go core** (`cmd/porta` + `internal/…`) only. Node
firmware lives in nodus and is unchanged; the parked Toit gateway in
`examples/toit-gateway` is the behavioral reference and stays at parity.

## 1. What B2 does *not* change

- **No schema migration.** B1 already created `command_queue.issued_by` /
  `delivered_at`, `nodes.observed_state`, and the `reports` table. B2 only
  starts *using* the `config` sub-blob already stored inside `observed_state`
  (`handler.go:137`).
- **No wire shape change.** `set` is one more verb in the existing flat-JSON
  command encoding; the report body's `config` field is already parsed and
  persisted.
- **No node firmware change.** Parity behavior is already shipped on the nodus
  side under the Toit gateway. The Go gateway is the only thing being updated.

## 2. Wire contracts (canonical in `docs/PROTOCOL.md` §2.5, §3 — unchanged)

**`set` (down-path).** Flat top-level JSON; args spliced verbatim:
```json
{"verb":"set","app":"sampler","key":"interval","value":30}
```
`value` is one JSON scalar: int, float, bool, or string. The scalar's JSON type
is preserved end-to-end (CLI → store `args` text → wire → node → observed
echo). Last-write-wins per `(app, key)`.

**Observed echo (up-path).** Report body already contains:
```json
{"apps":{…},"config":{"sampler":{"interval":30}},"health":{…}}
```
The `config` field may be absent on pre-D5 nodes; treat as `{}`.

## 3. Architecture: package layout

The reconcile logic lives in a **new `internal/config` package** (option A from
the brainstorm), keeping the existing dependency layering intact:

```
internal/
  command/    verbs + wire encoding         (no new deps)
  store/      sqlite data layer             (no new deps)
  config/     NEW — pure reconcile/projection/inference (imports command, store)
  handler/    TFTP dispatch                 (imports config)
  portacli/   cobra CLI                     (imports config)
```

Rationale: `internal/command` stays dependency-light (it must not import
`store`); `internal/store` stays a pure data layer (no algorithmic logic); the
new `internal/config` is a small, independently-testable algorithmic seam.

## 4. New code surface

### 4.1 `internal/command/command.go` — `set` constructor

Add one constructor alongside the existing `Run`/`Stop`/`SetPollInterval`:
```go
// Set builds a set command. value must be one of: int64, float64, bool, string.
// Caller is responsible for scalar inference (use config.InferScalar).
func Set(app, key string, value any) (Command, error)
```
Marshals `{"app":…,"key":…,"value":…}` via `json.Marshal`. Numeric types
preserve their JSON form (`int64` → `30`, `float64` → `30.5`); the existing
`EncodeWire` already splices args via `json.RawMessage`, so the scalar type
survives the wire round-trip.

### 4.2 `internal/config/` — new package

Four pure functions; no SQL, no I/O, no globals. Each file ≤120 LoC.

**`infer.go`** — `InferScalar(s string) any`. Matches the reference's
`infer-scalar`:
- `"true"` / `"false"` → `bool`
- string is integer-shaped (regex `^-?[0-9]+$`, parses to `int64`) → `int64`
- string is float-shaped (parses to `float64`) → `float64`
- else → `string` (the original `s`)

**`project.go`** — `ProjectDesired(cmds []store.Command) map[string]map[string]any`.
Walks `cmds` in order; for each row where `Verb == "set"`, JSON-decodes
`Args` with `json.Decoder.UseNumber()` and records `desired[app][key] =
value`. Decoded scalars are therefore one of: `json.Number`, `bool`, or
`string` — never `float64` (that's what makes the false-drift guard in §5.1
work). Last write wins; non-`set` verbs are skipped.

Equivalent helper `ProjectDesiredForApp(cmds, app)` returns just one app's map.

**`reconcile.go`** — `Reconcile(cmds []store.Command, observedConfig map[string]map[string]any) []Reissue`.
The `Reissue` type carries the original `store.Command.Args` string verbatim
(see §5.1) so the caller re-enqueues byte-identical args:
```go
type Reissue struct {
    Verb string // always "set"
    Args string // verbatim from the source row's ArgsJSON
    App  string // for logging only
    Key  string
}
```
Algorithm (parity with reference):
1. Build `latest[app][key] = sourceRow` over `cmds` for `verb=="set"`.
2. For each `(app, key) → row`:
   - `row.DeliveredAt.Valid == false` → skip (in-flight; also self-throttle).
   - `observed[app][key]` present and **equal under `equalScalars`** → skip
     (converged).
   - Else → append a `Reissue{row.Verb, row.Args, app, key}`.
3. Observed-only keys (no `desired` entry) are not iterated → never re-issued
   (parity: B2 has no `unset` verb).

`equalScalars(a, b any) bool` handles the false-drift trap (§5.1).

**`count.go`** — `ReconcileCount(cmds []store.Command, app, key string) int`.
Counts log rows where `verb=="set" && issued_by=="gateway-reconcile" &&
args.app==app && args.key==key`. Used by `device get` to print the ≥2× warning.

### 4.3 `internal/handler/handler.go` — reconcile hook

After the existing `InsertReport` call in `Write`, run reconcile best-effort:
```go
if err := h.store.InsertReport(id, observed, health, h.now()); err != nil {
    return err
}
h.reconcileAfterReport(id, field("config")) // logs internally, never errors out
return nil
```
`reconcileAfterReport` decodes `config` (using `UseNumber()`); calls
`config.Reconcile`; for each `Reissue` calls `store.EnqueueCommand(id, verb,
args, "gateway-reconcile", now)` and logs
`porta: reconcile re-issued <app>.<key> for <id> (observed diverged)`.

Wrapped in `defer recover()` + error log on the SQL path: **reconcile failure
never fails the report write** (parity with reference's `catch --trace`).

The `Handler` struct grows a `log func(format string, args ...any)` injection
point (defaults to `log.Printf`) so tests can capture the reconcile log lines.

### 4.4 `internal/portacli/` — `device set` / `device get`

Both follow the existing `mutate.go` / `inspect.go` idioms (`deviceFlag`,
`resolveNodeID`, `openStore`, `nowSec`); both attached to `newDeviceCmd()`.

**`porta device set -d <node> <app> <key> <value>`** (new in `mutate.go`):
- `EnsureNode`, `config.InferScalar(value)`, `command.Set`, enqueue with
  `issued_by="cli"`.
- Output: `<id>: enqueued set <app>.<key>=<value> (command #<n>)`.

**`porta device get -d <node> <app> [key]`** (new in `inspect.go`):
- Reads `nodes.observed_state`, decodes `{apps, config}` with
  `json.Decoder.UseNumber()`, picks the app's config map (empty if absent).
- Projects desired from `store.CommandLog` via `config.ProjectDesiredForApp`.
- **Single-key form**: prints
  `<id>: <app>.<key> desired=<d> observed=<o> [marker]` where `marker` ∈
  `(drift)` | `(pending)` | empty (converged). Missing values render as `--`.
- **Multi-key form** (no `key`): prints a `KEY / DESIRED / OBSERVED` table over
  the union of desired ∪ observed keys, with the same markers.
- **Warning footer**: for each still-divergent key with `ReconcileCount ≥ 2`,
  prints
  `<id>: ⚠ <app>.<key>: self-healed N× — node may be failing to apply`.

## 5. Implementation details that matter

### 5.1 The false-drift trap (Go-specific)

Desired comes from CLI string (e.g. `"30"` → `int64(30)`); observed comes from
the report JSON (default `float64(30)` under `encoding/json`). A naive `==` on
`any` returns false → spurious re-issue forever. **Mitigation, applied
everywhere reconcile/`device get` compares scalars:**

1. The report's `config` blob is decoded with `json.Decoder.UseNumber()` so
   numeric observed values are `json.Number` (a canonical string form).
2. The stored command's `args.value` is decoded the same way.
3. `equalScalars(a, b any) bool`:
   - Both `json.Number`: equal iff their canonical numeric strings match
     **or** they parse to the same `float64` (handles `30` vs `30.0`; the
     string-match short-circuit avoids float64's 53-bit precision loss on
     large `int64` keys).
   - Both `bool` or both `string` → direct `==`.
   - Cross-type (e.g. `string` vs `json.Number`) → `false`.

**Re-issue payload byte-identity.** When reconcile re-enqueues, it replays
`store.Command.Args` *verbatim* (no re-marshalling). This guarantees the wire
bytes on the retry equal the bytes on the original send — no chance of
silently flipping `30` ↔ `30.0` between attempts.

### 5.2 Self-throttle

Re-issued `gateway-reconcile` rows are enqueued with `delivered_at = NULL`. On
the next report:
- If that re-issue hasn't delivered yet → skip via the in-flight guard. **One
  re-issue per failed report.**
- If it *has* delivered and the node still reports the wrong value →
  re-issued again, `ReconcileCount` increments. ≥2 triggers the warning.

This emerges from the algorithm; no per-key cooldown timer.

### 5.3 Failure semantics

Reconcile runs *after* `InsertReport` succeeds. Every error path inside
`reconcileAfterReport` logs and returns; `defer recover()` catches panics. The
TFTP `Write` always succeeds on a parseable report regardless of reconcile
outcome. Parity with `examples/toit-gateway/handler.toit:121-136`'s
`catch --trace`.

### 5.4 Ordering inside one report

If a single report shows two divergent keys, both get one re-issue each (the
log walk is single-pass; each `(app,key)` produces at most one `Reissue`).
Order is `latest`-map iteration order, which is non-deterministic in Go —
acceptable because the wire sends them sequentially anyway and the node
applies them last-write-wins by key.

## 6. Testing strategy

### 6.1 Unit (new files under `internal/config/`)

- `infer_test.go` — `InferScalar` per scalar kind + edge cases (`"+30"`,
  `"3e2"`, empty, leading-zero strings).
- `project_test.go` — `ProjectDesired`: last-write-wins, multi-app, non-`set`
  verbs ignored, empty log.
- `reconcile_test.go` — the case table:

| # | `delivered_at` | observed | re-issue? |
|---|---|---|---|
| 1 | non-nil | equal | no (converged) |
| 2 | non-nil | differs | **yes** (drift) |
| 3 | non-nil | absent | **yes** (pending) |
| 4 | nil | (any) | no (in-flight / self-throttle) |
| 5 | non-nil | int 30 vs float 30 | no (false-drift guard) |
| 6 | non-nil | bool true vs bool true | no |
| 7 | n/a | observed-only | no (no `unset`) |

- `count_test.go` — `ReconcileCount` filters; ignores `cli`-issued rows.

### 6.2 Integration (`internal/handler/handler_test.go`, extended)

End-to-end with a real in-memory store:
1. Enqueue `cli` `set sampler.interval=30`; mark delivered.
2. POST a report whose `config={"sampler":{"interval":25}}`.
3. Assert `NextUndelivered` returns a `gateway-reconcile` row with
   `Args == ` *the original `cli` row's `Args`* (byte-identical).
4. POST a second drifted report → assert **exactly one** new re-issue (the
   first is still pending → self-throttle).
5. Mark the re-issue delivered, POST a third drifted report → second
   re-issue appears; `ReconcileCount == 2`.

### 6.3 CLI (`internal/portacli/config_test.go`, new)

Follows the `mutate_test.go` / `inspect_test.go` pattern: in-memory store,
`cmd.SetArgs(...)`, captured stdout. Covers `set` enqueue message, `get`
single-key with each marker, `get` multi-key table, and the warning footer.

### 6.4 Acceptance gate (matches B1's bar)

- `go build ./...`, `go vet ./...`, `go test -race ./...` all green.
- `examples/toit-gateway` build + host tests still green (must remain at
  parity, not get superseded).
- Hardware checkpoint on the spare ESP32 (`witty-jaguar`): install a
  control-demo image; issue a real `device set`; observe applied config in
  next report; reinstall the app to force drift; verify the gateway-reconcile
  re-issue lands and `device get` reports `(drift)` then converges.

## 7. Out of scope (explicit)

- **`unset` verb.** Stays a v1.0 backlog item; not in the parity reference.
- **Default-device ergonomic** (skip `-d <node>` after `device select <node>`).
  Designed as the *immediate follow-up* sub-project after B2; cross-cuts every
  `-d`-bearing command and is orthogonal to the config plane.
- **Multi-key atomicity.** A node applying two keys may report one without the
  other; reconcile handles this naturally (one stays divergent, one converges).
  No transactional `set` group is introduced.
- **Per-key cooldown / rate-limit.** The self-throttle is implicit; no timer
  state.
- **`gateway monitor`-style live tail.** B3 (telemetry plane).

## 8. Files added or changed

```
internal/command/command.go      (+ Set constructor)
internal/command/command_test.go (+ Set tests)
internal/config/                 NEW package
  infer.go         + infer_test.go
  project.go       + project_test.go
  reconcile.go     + reconcile_test.go
  count.go         + count_test.go
internal/handler/handler.go      (reconcile hook in Write)
internal/handler/handler_test.go (+ reconcile/self-throttle scenarios)
internal/portacli/mutate.go      (+ device set)
internal/portacli/inspect.go     (+ device get; wire to newDeviceCmd)
internal/portacli/config_test.go NEW
docs/PROTOCOL.md                 (no change — set verb + config echo already
                                  specified)
```

## 9. References

- `examples/toit-gateway/command.toit` — `Command`, `project-config`,
  `reconcile-config`, `reconcile-count`, `infer-scalar` (reference impl).
- `examples/toit-gateway/handler.toit:121-136` — reconcile-on-report hook
  (reference behavior).
- `examples/toit-gateway/gateway.toit:286-335` — `device set` / `device get`
  reference CLI.
- `docs/specs/2026-05-24-config-self-heal-design.md` — original (Toit-side)
  reconcile design.
- `docs/specs/2026-05-24-d5-observed-config-echo-design.md` — original
  observed-config echo design.
- `docs/PROTOCOL.md` §2.5, §3 — canonical wire protocol.

## 10. Implementation deviations from §5 (review-driven hardenings)

Three correctness fixes were applied during code review; they replace the
algorithms documented above, which had latent edge cases. Listed here so
future readers don't trust an inaccurate spec.

1. **`InferScalar`** (§5 referred to a single `^-?[0-9]+$` integer regex):
   shipped code uses `^[+-]?(0|[1-9][0-9]*)$` AND a second `leadingZeroInt`
   guard on the float path, because `strconv.ParseFloat("007", 64)` succeeds
   and returns `7.0` — without the second guard, `"007"` would be inferred as
   `float64(7)` instead of staying a string. (T1 review.)

2. **`EqualScalars`** (§5.1 documented "canonical-text OR float64"): shipped
   code is "canonical-text → int64-if-both-parse → float64". The plan's
   original algorithm was unsafe at the 2^53 boundary, where 9007199254740993
   and 9007199254740992 both round to the same float64 and would compare
   equal under a float-only fallback. Int64-first keeps it precision-safe
   for any int64 config key. (T3 review.)

3. **`reconcileAfterReport` null guard** (§5.3 didn't address the
   `"config":null` case): wire-legal JSON `null` decodes successfully to a
   `nil` Go map, which a naive implementation would treat as an empty
   observed and re-issue every desired key on every report (storm). Shipped
   code returns early when the decoded observed is `nil`, matching the
   "node didn't send config" semantics. (T7 review.)

Asymmetry to be aware of: when the report body OMITS the `config` key
entirely, `field("config")` defaults to `"{}"` and reconcile runs with an
empty observed (re-issues every desired key, throttled to one per cycle by
the in-flight guard). This matches the Toit reference behavior. Only the
explicit `null` payload is skipped.
