# Design note: Node lifecycle & reliability (power modes + watchdog)

**Date:** 2026-05-24
**Status:** Decisions captured (sibling to the M2 telemetry spec); **not yet
brainstormed to implementation depth** — this records what we settled and the open
items, ahead of its own design pass + plan.
**Sibling of:** `2026-05-24-m2-telemetry-design.md` (which stays power-mode-agnostic).

## Why this exists

Reviewing `~/workspaceToit/vindriktning` (real air-quality hardware) surfaced a gap:
our supervisor is **deep-sleep-only**, but a real fleet has **always-on** nodes too
(vindriktning is USB-powered: continuous sampling + windowed aggregation + periodic
push + a never-returning watchdog loop). The architecture must handle **both**, and
the watchdog flavor *follows* the power mode — so they are one cohesive concern.

## The unifying reframe

Deep-sleep vs always-on differ **only in what the node does *between* poll cycles.**
The cycle itself is identical:

```
bring up link → drain commands → reconcile → flush telemetry buffer → report
```

- **deep-sleep node:** `esp32.deep-sleep` → reboot is the loop. (today's supervisor)
- **always-on node:** don't sleep — leave payload tasks running; `sleep` the
  supervisor task until the next poll is due; loop.

Because the cycle is shared, the telemetry/command/report machinery is mode-agnostic.
The fork is localized to (a) the supervisor's between-cycle behavior and (b) the
watchdog flavor.

On ESP32 the two modes are **mutually exclusive** (deep-sleep powers everything down;
you cannot keep a container running). So power mode is a clean node-level binary — no
messy middle. (Light-sleep / ULP are out of scope.)

## Decisions captured

1. **Node-level power mode** — `deep-sleep | always-on`, **command-configurable**,
   **NVS-persisted**, **default `deep-sleep`**. The supervisor's `main` branches on it.
2. **Add the always-on branch *beside* the deep-sleep branch, not woven through it.**
   The M1-verified deep-sleep path must stay behaviorally identical and have its
   hardware verification re-run unchanged.
3. **Two watchdog flavors, selected by mode:**
   - **deep-sleep:** a **hardware/RTC watchdog** armed at wake, sized to
     `(poll budget + OBSERVE + margin)`; if the supervisor doesn't reach `deep-sleep`
     in time (WiFi/TFTP/sensor hang), the chip **resets** — which is just another
     wake: safe and idempotent. Plus **`with-timeout` on every blocking call**
     (connect / fetch / read), which the code largely lacks today (`catch --trace`
     does not catch a *hang*).
   - **always-on:** vindriktning's **task-restart watchdog** — run each job as a
     task, `ping` to prove liveness, restart late/dead tasks; the never-returning
     `run` loop *is* the always-on main loop.
4. **Aggregation is the app's job** (always-on samples ≫ report-rate). Bounded
   buffers everywhere (vindriktning bounds its deque for exactly this reason).
5. **Long-running reliability is first-class for always-on.** A deep-sleep node
   reboots each cycle (a clean slate that hides slow leaks); an always-on node runs
   for weeks — heap growth, **fragmentation** (esp. external byte arrays — see the
   security/transport note's neutering discussion), unbounded buffers. The watchdog +
   bounded buffers go from "nice" to load-bearing.

## Open items (for this spec's own design pass)

- Exact ESP32 hardware/RTC watchdog SDK API in Toit (confirm + prototype).
- Where the power-mode setting lives in NVS and the command verb to set it
  (`set-power-mode`?), and how it interacts with `set-poll-interval`.
- Whether telemetry-flush cadence should decouple from command-poll cadence for
  always-on (noted in the M2 spec as an open question).
- Concurrency/mutex discipline for the always-on case (supervisor + remoting + payload
  tasks all alive for long stretches).
- Restructuring `supervisor.toit:main` into a mode-branching loop without disturbing
  the deep-sleep path's verified behavior.
```
