# Config Self-Heal Design

**Date:** 2026-05-24
**Status:** Approved (design) — spec written for review, not yet implemented.
**Context:** Fast-follow to D5 (observed-config echo). M2.2 marks a `set`
command **delivered on TFTP transfer-complete** (delivery = transfer, not
execution — the gateway's uniform delivery model). A node that drains a `set`
but fails to apply it (the pre-existing fixed-size-NVS crash, `cc157a3`, was one
such case) loses that config: the command is delivered, never re-served, and
nothing re-applies it. Goal/apps self-heal today because the supervisor
reconciles its inventory against the goal each wake; **config has no such
reconcile**. Filed as **P1 davidg238/porta#1**.

D5 just gave us the missing half: the node's report now echoes its applied
config into `observed_state.config`. This spec closes the loop on the **gateway
side** — diff desired vs observed config each report and re-issue any `set` that
was delivered but did not take. The **node firmware is unchanged**.

## Goal

After a node reports, the gateway compares the **desired** config (projected
from the `set` command log) against the **observed** config (the report echo,
via D5). For any `(app, key)` whose establishing `set` is **delivered** but
whose observed value still diverges, the gateway re-enqueues a fresh `set`
(`issued_by="gateway-reconcile"`). The next poll re-delivers and re-applies it.
A key that keeps diverging across repeated, self-throttled re-issues surfaces a
warning in `device get`.

## Non-goals

- **Scope is config-only.** Goal/apps reconciliation is a separate (and
  partially self-healing) plane; this spec does not touch it. But the diff seam
  (below) is built **generic** so goal/apps can feed it later for the cost of a
  second projection.
- **No node firmware change.** All reconcile logic is gateway-side. Approach A
  (reconcile-on-report) was chosen over node-side full-config snapshot (B,
  bigger, changes set-delta→snapshot semantics + NVS wear) and apply-ack (C,
  breaks the uniform "delivery = transfer-complete" model).
- **No DB schema change.** Re-issue is an ordinary `command_queue` insert; the
  warning count reads the existing `issued_by` column.
- **No `unset` verb / desired never shrinks.** An observed key with no desired
  `set` (only possible after a DB reset) is left alone.
- **No migration / back-compat shim** (pre-1.0, no deployments — see
  `porta-no-legacy`). Pre-D5 reports carry no observed config; those keys read
  as not-yet-converged and are guarded by the in-flight rule below.

## The reconcile seam (the generic core)

A new **pure, host-testable** function in `gateway/command.toit`, mirroring
`project-config` / `config-marker`:

```toit
/**
Diffs desired config (from the $command-log) against $observed config and returns
  the `Command`s to re-issue to self-heal divergence. For each divergent (app, key)
  it returns that key's *latest `set` log entry replayed verbatim* — the original
  command rebuilt via `Command verb args` from the stored row, not reconstructed
  from extracted scalars. A key is re-issued only when its latest `set` is already
  delivered (delivered_at != null) AND the observed value diverges (absent, or
  present-but-unequal). An undelivered latest `set` legitimately lags (in-flight)
  and is skipped. Observed keys with no desired `set` are left alone (desired never
  shrinks). The generic diff seam: goal/apps can later feed a different projection
  of the same shape.
*/
reconcile-config command-log/List observed/Map -> List:  // -> List<Command>
```

- `command-log` is `Store.command-log` output: a list of maps each carrying
  `verb`, decoded `args`, **`delivered_at`** and `issued_by` — all durable
  `command_queue` columns, already selected (`store.toit:173-177`). This is the
  enriched input D5's `project-config` discarded: per `(app, key)` we need the
  latest `set`'s value *and* whether it is delivered.
- `observed` is the report echo: `app → {key: value}` (D5's
  `observed_state.config`).
- Returns the `Command`s to re-issue — the **replayed latest `set` rows**, built
  with the primary constructor `Command entry["verb"] entry["args"]`
  (`command.toit:24`). Empty when everything is converged or in-flight. The caller
  enqueues each verbatim; no value re-extraction, no second construction path.

**Per `(app, key)` decision** (iterate desired keys only):

| latest `set` delivered? | observed vs desired | action |
|---|---|---|
| no (`delivered_at == null`) | any | **skip** (in-flight — will deliver next wake) |
| yes | equal | **skip** (converged) |
| yes | unequal or observed-absent | **re-issue** (replay the latest `set` row verbatim) |

"Latest" = last `set` for that `(app, key)` in command-log order (same
last-write-wins / declarative-absolute rule as `project-config`).

Replaying the stored row rather than rebuilding from `app/key/value` means the
re-issued `args` map is *identical* to the original — scalar type fidelity
(int/float/bool/string) is preserved by construction, not re-inference.

## In-flight guard (the crux)

The node drains → applies → reports in the **same wake**. So:

- A **delivered** `set` *not* reflected in that wake's report = a lost apply →
  re-issue.
- An **undelivered** `set` legitimately lags (it delivers next wake) → skip.

This is why the projection must carry `delivered_at`, not just the value. Right
after a `set` (delivered_at still null) the key is *in-flight*, not divergent —
without the guard the gateway would re-issue redundantly every report until the
node caught up.

## Self-throttle (free, falls out of the guard)

A re-issued `gateway-reconcile` `set` is itself a `set` in the command log with
`delivered_at = null`. So on the **next** report it is the latest `set` and is
*in-flight* → skipped. It only re-issues again once it has been delivered **and**
the observed value still diverges. Therefore:

> "`gateway-reconcile` re-issued this key ≥ 2×" provably means "two reconcile
> sets, each delivered, each failed to take" — a real apply crash-loop, not
> reconcile noise.

That is exactly the warning threshold (below). No retry counter or backoff state
is needed; the guard makes re-issue self-limiting to one per successful-but-still-
failed report.

## Wiring — reconcile-on-report (`gateway/handler.toit`)

In `ReportWriter_.close_`, **after** `store_.insert-report` commits, run the
reconcile for that node. The report is already persisted, so a reconcile failure
must never lose the report:

```toit
close_ -> none:
  obj := decode-json_ buffer_.bytes.to-string
  apps := obj.get "apps" --if-absent=: {:}
  config := obj.get "config" --if-absent=: {:}
  health := obj.get "health" --if-absent=: {:}
  store_.insert-report id_
      --observed-state=(encode-json_ {"apps": apps, "config": config})
      --health=(encode-json_ health)
      --now=now_
  // Self-heal — only for nodes that actually echo config (D5+). A report with no
  // "config" key is a pre-echo node: its observed config is unknown, not empty, so
  // reconciling would re-issue forever. A failure here must not lose the report.
  if obj.contains "config":
    catch --trace:
      reissues := reconcile-config (store_.command-log id_) config
      reissues.do: | cmd/Command |
        store_.enqueue-command id_ cmd --issued-by="gateway-reconcile" --now=now_
```

- `config` here is the observed blob just parsed from the report — the same map
  folded into `observed_state`. No extra read.
- The `obj.contains "config"` gate is the pre-echo distinction the
  `observed_state` fold deliberately collapses (it defaults absent→`{:}`): a node
  that omits `config` has *unknown* observed config, so reconcile must not run.
- `enqueue-command` / `command-log` / the `Command` constructor all already
  exist; this is pure wiring on top. **No schema change.**
- Re-issue is idempotent (sets are absolute / last-write-wins), so even a stray
  duplicate is harmless. The full command history persists in `command_queue`
  (never deleted on delivery — the M1 audit-log decision), so each re-issue is a
  durable, replayable row, not transient state.

## Warning surface (`gateway/gateway.toit` `cmd-device-get`)

`device get` already renders desired-vs-observed with `(drift)`/`(pending)`
markers. Add a warning line per key that is **still divergent** and has been
re-issued by reconcile **≥ 2×**:

- Count `command-log` entries for that `(app, key)` with
  `issued_by == "gateway-reconcile"` (a new small pure helper alongside
  `config-marker`, e.g. `reconcile-count command-log app key -> int`).
- If the key's `config-marker` is non-empty (`(drift)`/`(pending)`) **and** the
  count is ≥ 2:

  ```
  ⚠ thermostat.mode: self-healed 3× — node may be failing to apply
  ```

`issued_by` and `delivered_at` are durable `command_queue` columns, so this is a
pure read of the existing audit log — **no schema change**, no derived
"reconciled?" flag to keep in sync. One wiring note for the implementer:
`cmd-device-get` currently maps the log down to `Command` objects
(`(store.command-log id).map: Command e["verb"] e["args"]`), which drops the
metadata *for that local only*; the warning path keeps the **raw** log rows
(which already carry `issued_by`) for `reconcile-count`. Per self-throttle, the
count is a faithful crash-loop signal, not reconcile chatter.

## Edge cases

- **No report that wake** — reconcile is report-triggered, so it simply does not
  run; `delivered_at` stays set, and the key heals on the next successful report.
- **Observed-only key** (desired absent — only after a DB reset) — not iterated
  (we walk desired keys); left alone. No `unset` exists, so desired never
  shrinks.
- **Pre-D5 report** (report body omits `config`) — reconcile is **skipped**
  entirely (the `obj.contains "config"` gate). Observed config is *unknown*, not
  empty; without the gate every delivered key would look divergent and re-issue
  forever (and falsely warn). All live firmware is D5+, so this is the
  belt-and-suspenders path, not the common one. A D5 node legitimately running
  with no config sends `config: {}`, which *is* present and reconciles normally.
- **Reconcile throws** — caught with `--trace`; the report still commits, the
  failure is logged, and reconcile retries next wake.

## Data flow

```
node wake: drain set → apply NVS → PUT report (apps + config + health)
                                          │
                            ReportWriter_.close_
                                          │
                       store.insert-report  (observed_state)  ← committed first
                                          │
                       reconcile-config(command-log, observed.config)
                                          │
                  ┌───────────────────────┴───────────────────────┐
            delivered & divergent                          in-flight / converged
                  │                                                │
        enqueue Command.set                                     skip
        issued_by=gateway-reconcile
                  │
        next poll re-delivers ─────────────▶ (self-throttled: ≥2× ⇒ warning)
```

## Testing

**Pure `reconcile-config` units** (`gateway/command_test.toit`, mirror the
existing `project-config` / `config-marker` tests — each test a `main` with
`import expect show *`, run via `toit-sqlite run`):

- delivered + drift (observed unequal) → re-issue.
- delivered + pending (observed absent) → re-issue.
- undelivered + drift → **skip** (in-flight guard).
- converged (delivered + equal) → skip.
- observed-only key (no desired set) → skip.
- multi-app / multi-key — only the divergent delivered keys re-issue.
- scalar type fidelity — int/float/bool/string equal-after-round-trip do **not**
  re-issue (no false drift; same `==`-on-decoded-JSON basis as `config-marker`),
  and a re-issued `Command`'s `args` equals the original row's `args` (verbatim
  replay — the returned command is the latest `set` rebuilt, not reconstructed).
- self-throttle: a `gateway-reconcile` set with `delivered_at == null` as the
  latest set → skip (proves one-re-issue-per-failed-report).

**Daemon-wiring integration** (`gateway/integration_test.toit` or
`handler_test.toit`): a report with a divergent delivered config → exactly one
expected `set` enqueued; a second report before the reissue delivers → **no**
double-issue (self-throttle holds); a report body that **omits** `config` →
**no** reconcile (pre-echo gate).

**`reconcile-count` / warning unit** (`gateway/command_test.toit`): counts only
`gateway-reconcile` entries for the key; warning fires at ≥2× + still divergent.

**Hardware** (fwkd, build/flash recipe per `porta-toit-gateway` memory): force a
divergence on a live node (e.g. a `set` the node fails to apply), confirm it
heals across a wake; force repeated failure and confirm the `device get` warning
surfaces.
