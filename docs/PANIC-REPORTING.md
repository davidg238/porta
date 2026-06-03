# Panic reporting (node → gateway)

> Canonical, porta-owned contract for how a node reports an **uncaught payload
> exception** ("panic") so the gateway operator can read a symbolicated stack
> trace. Like [`PROTOCOL.md`](PROTOCOL.md), this is implementation-agnostic: the
> **wire contract (§1) is normative for every node**; the Toit guidance (§3) is a
> recommended implementation for `nodus`-style nodes. A future Smalltalk node need
> only honour §1.

## Background

When a Toit payload throws an exception that is never caught, the node's system
process emits an encoded **trace** — a binary "system message" normally printed
to the serial line as a base64 string. That base64 is unreadable on its own; it
is symbolicated by `jag decode <blob>`, which resolves it against the program's
`.snapshot` (matched by the program UUID embedded in the trace). On the gateway
side, the operator has the snapshot (porta retains it at deploy time, see §4), so
`jag decode` produces a real stack trace.

The problem this contract solves: that trace is printed to **serial**, which does
not cross the network. So a panic on a remote node is invisible to the operator
unless the node deliberately *forwards* it. This doc defines that forwarding.

## 1. Wire contract (normative)

A node that forwards telemetry MUST report each uncaught payload exception as a
single telemetry entry on the existing `data?id=<mac>` path
([`PROTOCOL.md` §6](PROTOCOL.md)), with:

| Key | Value |
|-----|-------|
| `kind` | `"panic"` |
| `text` | base64 (RFC 4648, standard alphabet) of the **raw trace bytes**, byte-for-byte as the system emitted them — no wrapping, no prefix, no alteration. |

```json
{"kind": "panic", "text": "QlpoOTFBWSZTW…"}
```

Requirements:

- **Do not transform the trace bytes.** `text` must base64-decode back to the
  exact bytes `jag decode` expects; the embedded program UUID is what selects the
  snapshot, so any mutation breaks decoding.
- **One entry per panic.** Other keys (`name`, `value`, `ts`, `seq`) follow the
  normal §6 defaults; only `kind` and `text` are meaningful for a panic.
- Reporting is gated by the same telemetry-forwarding toggle as all other
  telemetry (`set-console`, [`PROTOCOL.md` §2.4](PROTOCOL.md)). With forwarding
  off, panics are not shipped (they still print to serial — see §3).
- Forwarding is **best-effort**, subject to the node's telemetry buffer bounds: a
  panic entry MAY be dropped under buffer pressure like any other entry. It MUST
  NOT block or destabilize the node, and MUST NOT replace the node's existing
  serial trace behavior (§3).

`kind` is free-form on the gateway: it stores the value verbatim, so `"panic"` is
additive — no gateway schema or ingest change is required.

## 2. What the gateway does with it

For context (not a node requirement):

- Ingest stores the entry as a `data_log` row with `kind="panic"`, `text=<blob>`.
- `porta monitor -d <node> --follow` recognizes `kind:"panic"` rows and runs
  `jag decode <text>` inline, printing a `‼ PANIC` header followed by the decoded
  stack trace. If the snapshot is not present on the operator's machine, it prints
  the raw blob plus a hint to decode it where the image was built.
- Decoding is **client-side**: it works on the machine that built/deployed the
  image (which retained the snapshot, §4). The gateway server stores no snapshots
  and stays language-agnostic.

## 3. Recommended Toit implementation (informative, for `nodus`-style nodes)

Toit routes every process's trace to a registered **`TraceService`**
(`system.api.trace`, selector uuid `41c6019e-ca48-4847-9673-0869355da76a`,
major 0 / minor 2). A node forwards panics by registering a provider for it:

1. Implement `TraceServiceProvider` with
   `handle-trace message/ByteArray -> ByteArray?`.
2. In `handle-trace`:
   - `text := base64.encode message`
   - add `{"kind": "panic", "text": text}` to the telemetry buffer that
     `data?id=` is shipped from.
   - **return `message`** (not `null`). Returning the message lets the system's
     built-in handler still print the trace to **serial**, so local USB debugging
     is unchanged. Returning `null` would suppress that — don't.
   - wrap the body so that if forwarding itself throws, you still
     `return message` (fall back to default serial handling); never swallow a
     trace silently.
3. **Register the provider before any payload can run**, and in the **same
   process that owns the telemetry buffer**, so `handle-trace` can append to that
   buffer directly. (In `nodus` this is the spawned remoting process that already
   installs `TelemetryServiceProvider` before payloads start.)

The system routes traces from **all** processes to the registered service, so
payload panics in other processes are captured. A panic in the forwarding process
itself (or before registration) falls back to serial only — acceptable.

## 4. Snapshot provenance (gateway side, for reference)

`jag decode` needs the program's `.snapshot` in jag's decode cache
(`~/.local/state/toit/snapshots/<uuid>.snapshot`). porta's `porta run` retains it
there at deploy time: after compiling and relocating, it reads the snapshot's
UUID (`toit tool snapshot uuid`) and copies the snapshot into that cache. So any
image deployed via `porta run` from a given machine is decodable on that machine.
This is gateway-operator tooling; it imposes nothing on the node.

## 5. Conformance summary

A node that forwards telemetry SHOULD report uncaught payload exceptions as
`kind:"panic"` entries per §1. A node MAY omit this (panics then remain
serial-only, as before). The Toit mechanism in §3 is recommended but not
required: any node that produces a §1-shaped entry whose `text` decodes to a
`jag decode`-readable trace conforms.
