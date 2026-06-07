# Show-and-tell walkthrough template

A reusable skeleton for a short screen-recorded walkthrough of a porta-family
project (porta, nodus, future nodus-st), pitched at the **Toit Discord
"show and tell"** — peer developers who know containers, snapshots, jag,
envelopes, services. Goal: **gauge interest**, not sell.

Keep all videos in this family looking and sounding the same:

## Look & feel (shared across porta / nodus / …)
- **Tool:** Kooha (Linux screen recorder), 1080p.
- **Length:** 3–4 minutes of narration. Hard ceiling ~5 min — it's a teaser.
- **Layout:** terminal + browser side by side where it helps; full-screen the
  thing being demoed.
- **Tone:** casual, fast, concrete, honest. Peer-to-peer ("a thing I built"),
  not a product pitch. Admit what it's *not* and that it's pre-1.0/hobby.
- **Vocabulary:** assume Toit fluency — say "container", "snapshot", "envelope",
  "`jag decode`", "SDK version", "relocated image". Don't explain ESP32 basics.
- **Captions:** optional bracketed lower-thirds for the one or two terms a
  newcomer might miss.
- **Always end on the same beat:** show it's *live/real*, then a single explicit
  ask for interest + offer to share the repo.

## Story arc (fill in per project)
Each video follows the same six beats. Replace the bracketed bits.

1. **Hook + one-sentence what-is-it** `[0:00, over the most alive screen]`
   "Quick show-and-tell. This is **[NAME]**, a [one line: what it is, who it's
   for]. What you're looking at is live — [the real thing on screen]."

2. **The one idea** `[~0:25]`
   The single thesis that makes it interesting to *this* audience.
   - porta: "one wire protocol, language-neutral controller, heterogeneous fleet."
   - nodus: "[the node-side thesis — e.g. a generic Toit firmware + a dev tool
     that flashes, runs, and decodes panics, conforming to porta's protocol]."

3. **The core demo** `[~0:50, the longest beat]`
   Show the main loop working on real hardware. Let something *live* happen on
   screen mid-sentence (a report lands, a flash completes, a value converges).

4. **The money shot** `[~1:50]`
   The one detail this audience will nerd out on.
   - porta: panic row → `[decode ↗]` link → language-neutral hand-off.
   - nodus: "[e.g. `nodus decode` turning a base64 panic into a real stack
     trace via the local snapshot cache — the jag-decode pain everyone knows]."

5. **Architecture / where it fits** `[~3:00, one diagram]`
   The block diagram + the repo topology (porta + nodus, coupled only over the
   wire). Name the stack in one breath.

6. **The ask** `[~3:25, back to a live screen]`
   Honest framing of status (pre-1.0, hobby, sibling repo), then ONE question:
   "Is anyone else doing [this kind of thing]? Would something like this be
   useful, or am I reinventing [the obvious commercial tool] for fun? Drop a
   comment — happy to share the repo."

## Per-project fill-in checklist
- [ ] NAME + one-sentence what-is-it
- [ ] The one idea (beat 2)
- [ ] Which live hardware/state you'll show, and the live moment to wait for
- [ ] The money-shot detail (beat 4)
- [ ] The diagram to end on
- [ ] The comparison tool to name in the ask (porta: Artemis; nodus: Jaguar/jag)
- [ ] Re-record trigger: update when the UI/CLI changes (these move weekly)

## Reference
- porta's filled-in script: `docs/demo/porta-show-and-tell-script.md`; porta's
  diagram is `docs/ARCHITECTURE.md`.
- nodus keeps its filled-in copy in the nodus repo
  (`nodus/docs/demo/nodus-show-and-tell-script.md`), following this skeleton, so
  the two videos feel like one series.
