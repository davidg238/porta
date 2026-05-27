# Design note: Node lifecycle & reliability (power modes + watchdog)

**Date:** 2026-05-24 — **extended 2026-05-25** (vindriktning brainstorm: the
per-container lifecycle layer + the `container.wait` scheduling primitive; see the
dated section below).
**Status:** Decisions captured (sibling to the M2 telemetry spec); **not yet
brainstormed to implementation depth** — this records what we settled and the open
items, ahead of its own design pass + plan.
**Sibling of:** `2026-05-24-m2-telemetry-design.md` (which stays power-mode-agnostic).

## Framing: the node as a small operating system (added 2026-05-25)

The clarifying lens for everything below: a node is a tiny OS in **three layers**.

- **L0 — kernel: the Toit VM.** The firmware `system` container (`critical`, `boot`).
  Owns the scheduler, GC, primitives, and the real `deep-sleep`/power syscalls. We run
  *on* it; we don't modify it.
- **L1 — init + system services: the supervisor.** The `boot` container that is the
  node's PID 1: brings up the link, drains commands, reconciles containers, owns the
  watchdog and the power decision, and hosts the always-present node services. Privileged
  *relative to apps* — but itself just a container on L0.
- **L2 — applications: user containers.** vin, chatty, control-demo. They do work,
  building on the standard library and any installed packages, and speak to the node
  only through L1's services.

**Soft privilege — the load-bearing caveat.** L1's "privilege" is by *capability and
convention*, **not** hardware enforcement: there is no MMU ring between L1 and L2; by
default an L2 container could import `net` or `gpio` itself. So this layering buys
**modularity, a contract, and a placement rule — not a protection/permission system.**
We deliberately do **not** build capability tokens or a permission model (YAGNI). The
win is a *placement rule* ("infra or app?") and a *contract* (the service interface).

### Lifecycles: task vs daemon

A container's lifecycle is **declared at install** — it cannot be inferred, since whether
`main` returns or loops forever is the halting problem.

- **task** (`run-once`): runs to completion and returns. L1 `wait`s for it (under a
  `with-timeout` cap), then proceeds. vin / chatty / control-demo.
- **daemon** (`run-loop`): never returns; a long-running service. L1 must **not** `wait`.

`task`/`daemon` is the conceptual pair (standard Unix meaning); `run-once`/`run-loop` is
the literal install flag. The flag is *not* `task`/`daemon` because **`Task` is already a
core Toit primitive** that lives *inside* a container (`Task.group`, see below) — reusing
it at container granularity would collide head-on with the task-vs-container distinction
this spec depends on; `run-once`/`run-loop` instead names the one mechanical fact the
supervisor branches on (does `main` return, or not).

```
   task   (run-once):  start ─▶ work ─▶ return ✔      L1 `wait`s (cap), then proceeds
   daemon (run-loop):  start ─▶ work ─▶ work ─▶ ⋯     never returns; L1 must not `wait`
```

### Communications: northbound / southbound, L1 as broker

L1 sits between two comms domains and **brokers** them.

> **What "northbound / southbound" means.** The terms come from **network management /
> SDN** — *not* the motherboard northbridge/southbridge. Picture the architecture drawn
> vertically with the **management authority at the top** and the **things being managed
> at the bottom**. An interface that faces **up, toward whatever controls you**, is
> *northbound*; one that faces **down, toward what you control**, is *southbound*. In SDN
> a controller exposes a *northbound* API to orchestration apps and a *southbound* API
> (e.g. OpenFlow) to the switches it programs — the controller is the broker in the
> middle. Here the **supervisor is that broker (an "agent")**: its northbound peer is the
> **gateway** (the controller), its southbound peers are the **L2 apps** (the local
> workloads it manages). The up/down intuition rhymes with northbridge/southbridge (core
> vs. periphery), but that's a separate chipset-topology metaphor; this is the
> controller/agent one.

- **Northbound — L1 ↔ gateway** (external, over TFTP/UDP): *desired* state (commands /
  config) comes **down** from the gateway; *observed* state (reports) and data go **up**.
- **Southbound — L1 ↔ L2** (internal, Toit service RPC on-device), two channels:
  - **config** (down, L1→L2): an app reads its declared parameters. *(Proposed rename:
    today's `ControlService` → `ConfigService`; "control" wrongly implies push and
    collides with the gateway control-plane.)*
  - **telemetry** (up, L2→L1): an app emits data + logs + health.

North/south names the *peer/interface*; up/down still lives inside each. L1's core job in
one line: **project northbound desired-config into southbound config; aggregate southbound
telemetry into northbound reports/data.** (The southbound boundary is convention, same
soft-privilege caveat as above — an app *should* use only config+telemetry, but isn't
forced to.)

```
          ┌─────────────────────────────────────────────────┐
          │                     GATEWAY                       │  the controller
          │             desired state   ·   reports           │
          └────────────────────────┬──────────────────────────┘
                                    │
              NORTHBOUND  (TFTP/UDP):   desired ↓     observed ↑
                                    │
   ╔════════════════════════════════╪════════════════════════════════╗
   ║ NODE                            │                                 ║
   ║   ┌──────────────────────────────┴──────────────────────────┐    ║
   ║   │ L1  SUPERVISOR   —   init / PID 1 · the broker           │    ║
   ║   │     link · drain · reconcile · watchdog · power          │    ║
   ║   │     ┌──────────────┐              ┌──────────────────┐   │    ║
   ║   │     │  config svc  │              │  telemetry svc   │   │    ║
   ║   │     └──────┬───────┘              └────────▲─────────┘   │    ║
   ║   └────────────┼───────────────────────────────┼────────────┘    ║
   ║                │     SOUTHBOUND (service RPC)    │                 ║
   ║         config ↓                                 ↑ telemetry      ║
   ║   ┌────────────┴───────────┐     ┌───────────────┴───────────┐    ║
   ║   │ L2   vin   (task)      │     │ L2   blink   (daemon)      │    ║
   ║   └────────────────────────┘     └────────────────────────────┘   ║
   ║   ──────────────────────────────────────────────────────────────  ║
   ║   L0   TOIT VM   —   scheduler · GC · deep-sleep syscall           ║
   ╚═══════════════════════════════════════════════════════════════════╝
```

### Node lifecycle is induced by what it hosts

The supervisor's own lifecycle — and therefore the node's power mode — is the **union of
its containers' demands**:

- **Only tasks installed →** L1 runs as a *task-like duty-cycle*: wake on its poll cadence
  (+ any GPIO triggers its tasks need), run the tasks, `wait` for them, **deep-sleep**.
- **Any daemon installed →** the daemon needs the node powered and L1's services resident
  to talk to *whenever it wants*; the node cannot deep-sleep through it. So **L1 itself
  must run as a daemon (always-on)**, doing periodic northbound comms while daemons stream
  telemetry into the bounded buffer between windows.

So *always-on is not a separate per-container knob* — it is **induced**: `any hosted
daemon ⇒ always-on node`.

**Decided — declared + validated power mode.** Power mode is an explicit node setting
(default duty-cycle), **not** derived. Reconciling a `run-loop` (daemon) container onto a
duty-cycle node is an **error** unless the node is (or is explicitly promoted to)
always-on — installing a daemon never *silently* flips a node, because a silent always-on
can wreck a battery node's life. Fail loud, promote on purpose. *(Resolves the
derived-vs-declared question; the matching "Open items" bullet is closed.)*

### Comms windows: intake before, egress after

For a **task-node**, frame each wake as two northbound windows bracketing task execution:

- **W1 — intake (open):** bring up the link, drain commands (latest desired/config),
  reconcile containers. Must be first, so tasks run against current config/images.
- *… run tasks (L2); L1 `wait`s under the cap …*
- **W2 — egress (close):** ship what this wake produced — task telemetry/data + a fresh
  report — then sleep.

```
   TASK-NODE wake   (link up only at the ends; node deep-sleeps between wakes)

  …sleep ─▶┌─ W1 intake ──┬───── tasks run ──────┬─ W2 egress ──┐─▶ deep-sleep…
           │ open link    │ L1 `wait`s (cap)     │ ship telem.  │
           │ drain cmds   │ vin: 8 frames →      │ + fresh rpt  │
           │ reconcile    │ olympic     (~8 s)   │ close link   │
           └──────────────┴──────────────────────┴──────────────┘
             desired ↓ (NB)                          observed ↑ (NB)
```

This improves **liveness/freshness by a full cycle**: a value a task computes reaches the
gateway *this* wake instead of next. Today this is half-built — the post-`OBSERVE`
telemetry flush is a nascent W2, but it is gated on `console-forward` and ships telemetry
only.

**Open — one link or two.** W1→W2 can keep a single link up across the task run (one
association; radio idle-powered during the read) or bring the link up twice (two
associations; radio off during the read). An energy tradeoff to **measure**, not decide on
paper.

A **daemon-node** has no "after all tasks complete": the always-on supervisor runs
*periodic* northbound windows on its poll cadence; daemons stream telemetry into the
bounded buffer between windows.

```
   DAEMON-NODE   (always-on): supervisor never sleeps; daemons stream into the buffer

   L2 daemon   ████████████████████████████████████████████████  runs continuously
    telemetry   ·    ·    ·    ·    ·    ·    ·    ·    ·    ·      → bounded buffer
   L1 windows  [W]──────────[W]──────────[W]──────────[W]────────  periodic (poll cadence)
                ↕            ↕            ↕            ↕
              gateway      gateway      gateway      gateway        (northbound each window)
```

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

A periodic deep-sleep task is *not* just a run-forever loop with the chip powered
down between iterations — it differs in two ways that change how you write the
payload:

1. **The scheduler owns the repeat scaffolding.** A continuous payload carries its
   own loop and its own pacing:

   ```
   <init-code>
   while true:
       <code>
       sleep --ms=60000
   ```

   Under deep-sleep, the wake/sleep *is* the loop — the supervisor lifts the
   `while true` and the `sleep` out of your program. The payload shrinks to the body:

   ```
   <init-code>
   <code>
   ```

   It runs once per wake and returns; the cadence lives in the supervisor's poll
   budget, not in the payload. (This is exactly why the run-once vs run-loop
   per-container lifecycle below has to be *declared* — the scheduler can't infer
   which shape it lifted.)

2. **There is no memory between runs.** Each wake is a fresh boot: all RAM is lost,
   so any state you want carried across executions must be written explicitly to
   Flash (NVS / the config + telemetry buffers). A continuous loop keeps its
   variables on the heap for free; a deep-sleep payload must persist or recompute.
   (This is the flip side of decision 5 below — the reboot-per-cycle clean slate
   that hides slow leaks is the same clean slate that erases your working state.)

   A subtler consequence: in a continuous program `<init-code>` sits structurally
   *outside* the loop, so it runs exactly once and "is this the first run?" is free —
   it's just program position. Under deep-sleep that boundary is gone: every wake
   re-runs `<init-code>` *and* `<code>`, so both are effectively *inside* the forever
   loop. Genuinely once-only work (seed defaults, create schema, first-boot
   calibration) can no longer rely on "I'm at the top of `main`" — the payload must
   record a first-run flag in Flash and branch on it to tell a cold first boot from
   an ordinary wake.

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
3. **Three layered reliability concerns — not two mode-exclusive watchdogs.**
   *(Revised after the 2026-05-26 watchdog spike — see "Watchdog spike" below.)*
   The earlier framing paired one watchdog per power mode; the spike showed they
   **layer** instead, because each guards a different scope:

   | Concern | Scope | Mechanism | Modes |
   |---|---|---|---|
   | **Payload liveness** | cross-container | `Container.wait` + `with-timeout` cap *(decided above)* | both |
   | **Supervisor internal-task liveness** | intra-process | vindriktning-style **software task-restart** watchdog — run each job as a task, `ping` to prove liveness, restart late/dead tasks | always-on only |
   | **Supervisor *wedge* recovery** | whole-chip | **`esp32` hardware watchdog** (`watchdog-init --ms` / `watchdog-reset` / `watchdog-deinit`) — if the supervisor never feeds it (WiFi/TFTP hang), the chip **resets** | both (backstop) |

   - The **hardware watchdog is a last-resort whole-chip backstop in *both* modes.**
     Deep-sleep: arm at wake sized to `(poll budget + cap + margin)`; reaching
     `deep-sleep` (deinit) before it fires is the success path, and a reset is just
     another wake — safe/idempotent. Always-on: feed it from the main loop, because
     if the whole supervisor *process* wedges, the software task-restart watchdog
     (which runs *inside* that process) can't fire — only a hardware reset can.
   - The **software task-restart watchdog is always-on-only:** it recovers a dead
     *task* within the supervisor process; the never-returning `run` loop *is* the
     always-on main loop. It cannot span to payload *containers* (same tasks-vs-
     containers boundary as the `wait` discussion) — payloads are covered by row 1.
   - Independent of all three: **`with-timeout` on every blocking call**
     (connect / fetch / read), which the code largely lacks today (`catch --trace`
     does not catch a *hang*).
4. **Aggregation is the app's job** (always-on samples ≫ report-rate). Bounded
   buffers everywhere (vindriktning bounds its deque for exactly this reason).
5. **Long-running reliability is first-class for always-on.** A deep-sleep node
   reboots each cycle (a clean slate that hides slow leaks); an always-on node runs
   for weeks — heap growth, **fragmentation** (esp. external byte arrays — see the
   security/transport note's neutering discussion), unbounded buffers. The watchdog +
   bounded buffers go from "nice" to load-bearing.

## Update 2026-05-25 — per-container lifecycle + the `wait` primitive (vindriktning brainstorm)

Trying to land **vindriktning** as a real payload exposed the layer *below* the
node-level power mode above: a node hosts **containers**, and each container has its
own lifecycle. The node-level deep-sleep/always-on decision turns out to be *induced*
by this per-container layer — so they need modelling together.

### The motivating payload

VINDRIKTNING = an IKEA air-quality unit: a **PM1006** particulate sensor on UART
(9600 baud). `~/workspaceToit/vindriktning/vindriktning.toit` is reusable as-is —
`Vindriktning rx-pin` opens the port; `.next` blocks for a valid 20-byte frame
(header `16 11 0b` + checksum) and `.air-quality` yields PM2.5 ppm. The frames arrive
~**1 / second**.

The old `vin_client.toit` is built for the *opposite* lifecycle from porta: it runs
**continuously** (a software watchdog drives a 60 s collect task + a 2 min MQTT-SN push,
with its own WiFi provisioning / NTP / LED). On porta almost all of that scaffolding is
the supervisor's job already. The porta payload is tiny: per wake, **read 8 frames,
compute the olympic score, `report` it via `TelemetryServiceClient` (like
`chatty`/`control-demo`), and return.** ("Olympic score" = trimmed mean: drop the
single highest and lowest of the 8, average the middle 6 — robust to sensor spikes.)

### The collision that started it

The supervisor today does: wake → poll → **start payload containers** → `sleep OBSERVE`
(**5 s**) → `deep-sleep` (which *kills* the payloads). The olympic value only reaches the
gateway via the post-`OBSERVE` telemetry flush. But 8 frames ≈ **8 s** > the 5 s window —
so a fixed `OBSERVE` cannot accommodate a payload whose runtime is data-dependent.

### The fix: the supervisor `wait`s on the container, not a fixed clock

Tasks vs containers matters here. A Toit **`Task.group`** groups *tasks inside one
process* (when the `--required` task ends, the rest are cancelled). But the supervisor
and vin are **separate containers** (separate processes that talk over the
TelemetryService RPC) — a task group **cannot** span them.

One layer up, the native primitive already exists. `system/containers.toit`:
`start id -> Container` (line 28) and **`Container.wait -> int`** (line 100, blocks until
the container exits, returns its exit code), plus `on-stopped`. The supervisor *already*
calls `containers.start a.id` in `start-installed` — it just **discards the handle** and
does a blind `sleep OBSERVE`.

**Decision:** keep the `Container` handles and **`wait` on the run-once payloads**, then
deep-sleep — no magic 5 s, no explicit "done" signal. The payload self-paces; the
supervisor sleeps when the work is actually finished. This is "last member dies → close
the group," done at the **container** granularity where these two things really live.

**A `with-timeout` cap is mandatory** — and it is *not* the same as a watchdog:

- `with-timeout` around `wait` = a **deadline on the supervisor's own wait**: "wait up to
  N s for the run-once payloads, then give up and deep-sleep." Graceful, local, no reboot.
  Handles the *expected* slow/silent-sensor case. Graceful must **not** mean silent: a cap
  hit is a node-health event — the supervisor **logs it locally and reports it northbound**
  (a typed `data_log` event + a flag folded into observed-state), so a repeatedly-capping
  container (dying sensor, wrong baud, unplugged) is visible/trendable via `device get` /
  `gateway monitor` rather than vanishing into a clean deep-sleep.
- A **watchdog** (the section above) = **liveness/recovery**: must be fed, else **hard
  reset**. Whole-node scope. It earns its keep only when the *supervisor itself* wedges
  (WiFi/TFTP hang) before it can reach `deep-sleep`. The SDK exposes both the *reset
  cause* (`esp32.RESET-TASK-WATCHDOG`) **and** a high-level hardware-watchdog feed API
  (`esp32.watchdog-init --ms` / `watchdog-reset` / `watchdog-deinit`) — so the supervisor
  wedge-watchdog needs no custom code (see "Watchdog spike" below). vindriktning's own
  software watchdog is a *different* layer: intra-process task-restart, not whole-chip
  reset. The two watchdog roles + the cap are complementary: `with-timeout` for the
  payload `wait`, software task-restart for always-on supervisor tasks, hardware reset
  for a wedged supervisor process.

### The second dimension: run-once vs run-loop (declared, not inferred)

Whether a container ever exits hinges on whether its `main` **returns** or sits in a
`while true` — which is *internal* to the payload and **undecidable from outside** (the
halting problem). The supervisor can observe "hasn't exited yet," never "will never
exit." So the lifecycle **must be declared at install**, not inferred from behaviour.

porta already declares install-time metadata (triggers, runlevel) carried in
`InstalledApp` / the goal, so a lifecycle field slots into the existing seam. Precedent
both ways: Toit firmware containers carry a **`critical`** flag ("keep the system alive
while this runs" — seen on the envelope's `system` container), and jaguar declares a
per-container **`timeout`** at install. "Declare run semantics at install" is established.

**Name:** **`run-once`** (the code returns; supervisor `wait`s then sleeps) vs
**`run-loop`** (never returns; supervisor must not `wait`). Crisp, parallel, and names
exactly what's declared. (`run-forever` is an intent-flavoured alternative for the
second.) Default = `run-once`.

### The matrix: existing triggers × lifecycle

Rows = the existing triggers from `triggers.toit` (the *when-to-start* axis). Columns =
the lifecycle. ✅ natural · ⚠️ possible-but-odd · ❌ doesn't fit.

| Trigger (when to start) | **run-once** — returns; supervisor `wait`s then sleeps | **run-loop** — never returns; supervisor can't `wait` |
|---|---|---|
| **boot** | ✅ canonical — chatty / control-demo / **vin**: run each wake, report, exit | ✅ the always-on service — comes up each boot, node *stays awake* to host it |
| **install** (once, on first install) | ✅ one-time setup/migration, then never again | ❌ contradictory — never restarts after a deep-sleep reboot; a "forever service" that starts only once |
| **interval=N** | ⚠️ periodic worker — but on a deep-sleep node `boot` ≈ this (wake cadence *is* the poll interval); fully meaningful only on an always-on node | ❌ redundant — a loop needs no interval to re-fire |
| **gpio-high / gpio-low** | ✅ event-driven: wake on pin → handle → exit → sleep | ⚠️ rare — start a forever loop on an edge |
| **gpio-touch** | ✅ wake on touch → handle → exit → sleep | ⚠️ rare |

What the matrix shows:

1. **run-once fills every row** — the natural default, pairs with every start condition.
   vin is `boot × run-once`.
2. **run-loop only really makes sense with `boot`.** Every other run-loop cell is
   degenerate. So "always-on" is not orthogonal to each trigger — it is essentially
   *`boot` + never-exits + node-stays-awake*, i.e. a **node mode**.
3. **Deep-sleep collapses part of the (Artemis-inherited) trigger vocabulary.** On a
   duty-cycled node `boot` already means "every wake," so `interval`/`install` are partly
   redundant with the sleep cadence — those triggers presume a long-running node.

### So: two dimensions, not three

- **D1 — Start condition** (trigger): *when* the container launches. *Already modelled.*
- **D2 — Lifecycle** (`run-once | run-loop`): *whether it exits* → determines the
  supervisor's disposition (`wait`-then-sleep vs run-in-background). *New, declared.*

The thing that *looks* like a third axis — "duty-cycled vs always-on node" (the power
mode in the top half of this doc) — is **not independent**. It is **induced** by D2: the
moment a node hosts a `run-loop` container, it cannot deep-sleep through it and flips to
always-on. Per the matrix, that lives almost entirely in the **`boot × run-loop`** cell.
This is the bridge between this update and the node-level power-mode decision above.

### Minimal change needed *now* (for vin, `boot × run-once`)

1. `start-installed` returns the started `Container` handles (+ each app's lifecycle).
2. The supervisor **`wait`s on the `run-once` handles** (under one `with-timeout`
   max-awake budget) instead of `sleep OBSERVE`; it does **not** `wait` on `run-loop`.
3. `container install` gains a declared **lifecycle** field (`run-once` default), carried
   in `InstalledApp` / the goal alongside triggers/runlevel.
4. vin payload: read 8 frames → olympic (trimmed) mean → `report` → return. Telemetry
   forwarding must be **on** (`device set-console on`) for the value to ship.

(Per-wake burst was chosen over a one-sample-per-wake NVS ring: it honours "report every
minute" and keeps the payload stateless; the cost is ~8 s awake/min, bounded by the cap.)

## Watchdog spike (2026-05-26)

De-risked the always-on/watchdog half before planning. Source: jaguar SDK **v1.64.0**
(`.../sdk/lib/toit/lib/esp32/esp32.toit`) + `~/workspaceToit/vindriktning/watchdog.toit`.

1. **Hardware watchdog feed API exists** (`esp32.toit:273-288`): `watchdog-init --ms`,
   `watchdog-reset`, `watchdog-deinit` — arm/feed/disarm a whole-chip reset timer. This
   *corrects* an earlier spec claim that the SDK had "no high-level feed API." The
   deep-sleep wedge-watchdog (decision 3) is therefore three SDK calls, no custom code.
2. **vindriktning's watchdog is intra-process task-restart** (70 lines): `add name code
   --period` → `run` cancels+restarts any task whose `ping` is overdue. No hardware, no
   chip reset; recovers a dead *task*. Groups tasks within **one process**, so it guards
   the always-on supervisor's own jobs but **cannot** span to payload containers (same
   boundary as `Container.wait`). → drove the **layered** revision of decision 3.
3. **Hardware-verified** on an ESP32 rev 1.0 node (`classic-minute`, jaguar v1.64.0):
   `watchdog-init --ms=3000` + never feeding → chip resets in ~3 s. Findings:
   - The primitive arms the ESP-IDF **Task Watchdog Timer (TWDT)**; on expiry it panics
     → software CPU reset → clean reboot (jaguar came back up unaided). A reset is just
     another boot — safe/idempotent, as decision 3 assumes.
   - **The reset is detectable from Toit:** despite the bootloader banner reading
     `rst:0xc (SW_CPU_RESET)`, `esp32.reset-reason` on the next boot returns
     **`6` = `RESET-TASK-WATCHDOG`** — so the supervisor can recognise + report a
     wedge-reset (feeds the same node-health path as the cap event).
   - Caveat to size the timer well above the *task-scheduling* granularity, not just the
     work budget: the TWDT watches task liveness, so `--ms` must exceed the longest
     legitimately-blocking stretch between Toit scheduler yields, or it false-trips.

## Open items (for this spec's own design pass)

- ~~Exact ESP32 hardware/RTC watchdog SDK API in Toit (confirm + prototype).~~
  **DONE — API + on-device reset both hardware-verified** (`esp32.watchdog-init/reset/
  deinit`; reset detectable as `reset-reason == RESET-TASK-WATCHDOG`). See "Watchdog spike".
- Where the power-mode setting lives in NVS and the command verb to set it
  (`set-power-mode`?), and how it interacts with `set-poll-interval`.
- **Lifecycle field plumbing:** CLI flag on `container install`, goal-map key,
  `InstalledApp` field. (How `run-loop` on a `deep-sleep` node is reconciled is now
  **decided**: reject unless explicitly promoted — see "declared + validated" above.)
- **`wait` + cap mechanics:** one shared max-awake budget vs per-app timeout; what to do
  with a `run-once` container that hits the cap (stop it? let deep-sleep kill it?). Cap
  hits are reported as a node-health event (decided above), and escalation **mirrors the
  shipped config-self-heal threshold**: `gateway monitor` / `device get` **warn at ≥2
  consecutive caps** (matching self-heal's "warns ≥2×"), the daemon logs each cap like it
  logs each re-issue. Still open: the exact `data_log` event shape + how the unhealthy
  flag rides observed-state, and what (if anything) the gateway does *past* the warn (stop
  reconciling the container? alert?) — self-heal's in-flight-guard/self-throttle pattern is
  the reference.
- Whether telemetry-flush cadence should decouple from command-poll cadence for
  always-on (noted in the M2 spec as an open question).
- Concurrency/mutex discipline for the always-on case (supervisor + remoting + payload
  tasks all alive for long stretches).
- Restructuring `supervisor.toit:main` into a mode-branching loop without disturbing
  the deep-sleep path's verified behavior.
