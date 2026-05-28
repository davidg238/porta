# porta Go core data plane (sub-project B1) — design

**Date:** 2026-05-27
**Status:** approved (design)
**Spec for:** B1, the first slice of sub-project B (Go protocol parity).

## Where this sits

This is part of the multi-repo renovation that consolidates porta onto its **Go
server** (see `docs/specs/2026-05-27-porta-go-mainline-restructure-design.md` and the
end-state there). Sub-project **A** (Go-mainline restructure) shipped. Sub-project **B**
is the critical path: bring the Go server up to the current canonical wire protocol
(`docs/PROTOCOL.md`) so it serves the live **nodus** node end-to-end, replacing the Toit
gateway (now parked at `examples/toit-gateway`, which is the behavior reference — verified
at parity).

The Go server's *current* identity is the Smalltalk/berry **"compile → push bytecode →
live-debug"** machine (verbs `load:`/`dbg:*`, `devices`/`debug_*` store, a colliding
`/commands` resource with a different encoding). The porta protocol is a *different*
machine sharing only plumbing. So B is largely a **greenfield porta implementation in
Go**, reusing the TFTP/sqlite/CLI frameworks while lifting the ST-specific code out.

### B decomposes into four slices (each its own spec→plan cycle)

- **B1 (THIS SPEC) — Park ST + porta core data plane.** Outcome: live nodus installs,
  runs, and reports against the Go server.
- **B2 — Config plane.** `set` verb, per-app config, observed-config echo, self-heal
  reconcile (issued_by, in-flight guard, ≥2× warn), `device set`/`get`.
- **B3 — Telemetry plane.** `data?id=` typed JSONL ingest (`value_type`), `set-console`
  verb, `monitor` CLI.
- **B4 — Operator surface refresh.** porta MCP tools + htmx UI for Toit nodes.

### Decisions locked during brainstorming

- **Park the ST machinery** (don't delete, don't coexist): it is preserved for a
  deliberate ST re-enable later. Mechanism in §1.
- **Polyglot seam = per-node `kind`, not command namespaces.** Payloads are opaque bytes
  keyed by crc (each node interprets its own image); the core verbs are generic
  node-control verbs shared across node kinds; language-specific behavior (debug verbs,
  build pipeline) keys off the node's `kind` when re-enabled. B1 adds the `kind` column
  as the seam but builds no namespace machinery.
- **Payloads are prebuilt `.bin` files** the operator supplies via `container install`;
  jag/SDK *building* is sub-project C.
- **Hardware checkpoint = cut over jolly-pine** to the Go server (ending the Toit-gateway
  config-self-heal soak, which is fine — self-heal is B2's concern).
- **The `data` resource is deferred entirely to B3** (telemetry is off by default and B1
  has no `set-console`, so no node will WRQ `data` during B1).

## 1. Architecture — split the module, build porta fresh

Two binaries in one Go module:

- **`cmd/st-devserver/`** — the parked ST/berry server (today's `cmd/porta` code). Its
  ST-only packages move under **`internal/st/`** (`debug`, `debugui`, `mcpserver`, the ST
  command encoding, the `devices`/`debug_*` store, the TCP `cli` listener). It must still
  **build and keep its existing tests green**; it is not developed further in B.
- **`cmd/porta/`** — the new porta-protocol server (the mainline), with fresh core
  packages: `internal/store` (porta schema), `internal/command` (verbs + codec),
  `internal/handler` (TFTP resource dispatch), plus CLI wiring.
- **Shared:** the generic TFTP transport (`internal/tftp` packet/UDP-server mechanics)
  stays shared by both binaries; the ST-specific `CommandToJSON` (verb+hex payload) moves
  to the ST side.

## 2. Store schema (sqlite — mirrors the Toit gateway, plus the `kind` seam)

- **`nodes`**: `id` TEXT PRIMARY KEY (12-hex lowercase MAC), `name` TEXT, `source_addr`
  TEXT, **`kind` TEXT NOT NULL DEFAULT 'toit'**, `first_seen` INTEGER, `last_seen` INTEGER
  (NULL until first contact), `poll_interval_s` INTEGER DEFAULT 30, `max_offline_s`
  INTEGER DEFAULT 300, `last_report_at` INTEGER, `observed_state` TEXT (JSON
  `{apps,config}` cached from the latest report).
- **`payloads`**: `crc` INTEGER PRIMARY KEY (CRC32-IEEE), `name` TEXT, `size` INTEGER,
  `image` BLOB. `INSERT OR REPLACE` keyed by crc.
- **`command_queue`**: `id` INTEGER PRIMARY KEY AUTOINCREMENT, `device_id` TEXT, `verb`
  TEXT, `args` TEXT (JSON), `issued_at` INTEGER, `issued_by` TEXT ('cli' |
  'gateway-reconcile'), `delivered_at` INTEGER (NULL → undelivered; set on RRQ
  transfer-complete).
- **`reports`**: `id` INTEGER PRIMARY KEY AUTOINCREMENT, `device_id` TEXT, `ts` INTEGER,
  `observed_state` TEXT (JSON `{apps,config}`), `health` TEXT (JSON). Append-only.
- **`data_log`**: `id` PK AUTOINCREMENT, `device_id` TEXT, `ts` INTEGER, `seq` INTEGER,
  `kind` TEXT, `name` TEXT, `value` NUMERIC, `text` TEXT, `value_type` TEXT. **Created in
  B1, populated in B3.** Index `idx_data_device_ts` on `(device_id, ts)`.

`touch-node` creates a node on first contact and updates `last_seen`/`source_addr` on
each contact; `ensure-node` guarantees a row without recording contact (pre-addressing by
MAC). Online iff `last_seen != null && (now - last_seen) <= max_offline_s`.

## 3. TFTP resource surface

Parse `base?k=v&k2=v2` (a bare key → empty string). Touch `last_seen` on any request
carrying `?id=`.

| Resource | Op | Behavior |
|----------|----|----------|
| `commands?id=` | RRQ | Serve the oldest undelivered command as flattened JSON. **Zero-byte body = queue empty** (every real command is ≥1 byte). Mark `delivered_at` **only** on transfer-complete `ok=true`; a drain (zero-byte) or failed transfer marks nothing. |
| `payload?id=&crc=` | RRQ | Serve the **raw image bytes** for `crc`; throw `STORAGE-FILE-NOT-FOUND` if no such crc. TFTP size = `payloads.size`. |
| `report?id=` | WRQ | Parse `{apps,config,health}`; cache `observed_state`+`last_report_at` on the node; append to `reports`. `config` is **stored but not reconciled** (B2). |
| `data?id=` | WRQ | **Deferred to B3.** |

Any WRQ that is not `report?id=` (incl. missing `id`, or `data` in B1) →
`STORAGE-ACCESS-DENIED`.

## 4. Commands + payload delivery

- **Verbs in B1: `run`, `stop`, `set-poll-interval`** (`set`→B2, `set-console`→B3). The
  `command_queue` stores any verb; B1 simply has no CLI to issue the others.
- Wire encoding: `{"verb": <verb>, <...args flattened...>}` (no nested `args`), JSON
  round-trip **scalar-type-faithful** (int stays int, float stays float). Commands are
  declarative/absolute/idempotent; last write wins per target.
- `run` args: `name` (req), `crc` (req), `size` (req), `triggers` (req, `{type:value}`
  map), `runlevel` (default 3), `lifecycle` (default `"run-once"`), `arguments` (default
  `[]`). `stop` args: `name`. `set-poll-interval` args: `interval`.
- Trigger map types: `boot`/`install`/`interval`/`gpio-high:<pin>`/`gpio-low:<pin>`/
  `gpio-touch:<pin>`; unrecognized key is rejected at enqueue.
- **Payload = prebuilt `.bin`**: `container install <app> <file.bin>` reads the file,
  computes **CRC32-IEEE** (Go stdlib `hash/crc32` IEEE table — byte-identical to the
  protocol and to jag's `X-Jaguar-CRC32`), `INSERT OR REPLACE`s the blob keyed by crc, and
  enqueues `run` with that `crc` and `size`. `--crc=<int>` may override the computed crc.

## 5. Report ingest

Ingest the full `apps`/`config`/`health` report shape. `apps`/`config`/`health` each
default to `{}` if absent (a node that does not implement `config` is tolerated). Cache
`{apps,config}` as `observed_state` on the node row, refresh `last_report_at`, and append
the report + health to `reports`. B1 acts on `apps` only (for `container list` /
`device show`); `config` is stored-but-inert until B2's reconcile loop.

## 6. CLI surface (`cmd/porta`)

Match the Toit gateway's verbs for operator muscle-memory. `<node>` is a name or MAC;
`<dur>` is a jag-style duration (e.g. `30s`, `5m`).

| Command | Args / flags | What it does |
|---------|--------------|--------------|
| `serve` | `--port` (default 6969) | Start the TFTP daemon serving the command queue + payloads from the store. Blocks until interrupted. |
| `scan` | `--include-never-seen` | List nodes: id, name, last-seen (relative), online/offline. Never-seen nodes hidden unless flagged. |
| `ping` | `-d <node>` | Report whether the node is online (`last_seen` within `max_offline_s`). |
| `device show` | `-d <node>` | Node details: id, name, last-seen, poll-interval, max-offline, cached `observed_state`, undelivered commands. |
| `device set-poll-interval` | `-d <node> <dur>` | Enqueue `set-poll-interval`; also cache `poll_interval_s` on the node row. |
| `device set-max-offline` | `-d <node> <dur>` | Update the node's offline threshold (gateway-side only; not a command to the node). |
| `device name` | `-d <node> <name>` | Override the auto-assigned friendly name. |
| `container install` | `-d <node> [--interval <dur>] [--trigger <spec>] [--runlevel <n>] [--lifecycle <mode>] [--crc <int>] <app> <file.bin>` | Register the image (CRC32-IEEE computed from the file, or `--crc` override) and enqueue `run` with `crc`+`size`. |
| `container uninstall` | `-d <node> <app>` | Enqueue `stop` for the app. |
| `container list` | `-d <node>` | List apps from the latest observed report (name, crc, runlevel). |
| `log` | `-d <node>` | Command audit history: id, verb, delivered (yes/pending), args. |

Deferred: `device set`/`get` → B2; `device set-console`, `monitor` → B3. Port the
deterministic adjective-noun auto-naming (`names.toit`) to Go so node names match across
the Toit and Go gateways. The subcommand structure replaces the current `flag`-only
`main` (library choice — likely `cobra` — is a plan-level detail).

## 7. Testing & hardware checkpoint

- **Go host tests** mirroring the Toit gateway's `*_test.toit` suites: command codec
  (encode/decode, scalar fidelity, trigger map, defaults), store CRUD (touch/ensure,
  payload register, command enqueue/next-undelivered/mark-delivered, report insert +
  observed_state cache), handler (drain + zero-byte sentinel + deliver-on-complete,
  `payload?crc` raw bytes + not-found, `report` ingest, WRQ rejection), and a CRC32-IEEE
  vector check.
- **Hardware:** run `cmd/porta serve` on the dev box; **repoint jolly-pine** to it; verify
  it **installs an image, runs it, and reports observed apps** end-to-end. (gw deployment
  of the Go binary — including the CGO/`go-sqlite3` glibc-2.36 question on gw85224-01 — is
  deferred to a later slice.)

## 8. Explicitly NOT in B1

Config plane + self-heal (B2) · typed `data` telemetry ingest + `set-console` + `monitor`
(B3) · porta MCP tools + htmx UI (B4) · jag/SDK image building (sub-project C) · deploying
the Go binary to gw85224-01.

## Subtleties a Go port is most likely to get wrong (carried from the reference)

- **Zero-byte body is a success sentinel (queue empty), not an error — and the failure is
  silent.** If a real error (unknown node, DB hiccup) returns an empty body instead of a
  TFTP **ERROR** packet, the node reads "queue drained" and stops polling — commands never
  arrive, with no crash to notice. Return an empty body *only* for a genuinely empty queue;
  surface real errors as TFTP ERROR packets. (This is the exact bug the old jast-gw had —
  it returned `nil` for both "no command" and error cases.)
- **Mark `delivered_at` only on a `commands`-RRQ transfer-complete with `ok=true`** — never
  when you *pop* the command. If you mark on pop and the transfer then fails, the gateway
  believes the node received a command it never got: silent command loss. `payload` RRQs
  and all WRQs never mark; failed transfers never mark.
- JSON scalar **type fidelity** end to end (float `21.5` must not become int `21`).
- `touch-node` (records contact) vs `ensure-node` (phantom row, no `last_seen`).
- `config` is a plane separate from `apps`/goal, even though both live in
  `observed_state`.
