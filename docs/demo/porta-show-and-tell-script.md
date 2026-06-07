# porta — show-and-tell narration script

Filled-in instance of `show-and-tell-template.md`. Audience: the Toit Discord
"show and tell". Goal: gauge interest. ~3.5 min screen recording (Kooha, 1080p).

> Tone: casual, fast, concrete, honest. Peer-to-peer, not a sales pitch.
> Bracketed lines are screen/action cues; the quoted lines are spoken narration.
> **Strongest live moment:** wait for a real `vin` PM2.5 telemetry row to land on
> screen mid-sentence (beat 3) rather than cutting around it.

**[0:00 — SCREEN: the fleet page at `:6970`, two nodes green]**
"Hey — quick show-and-tell. This is **porta**, a little self-hosted gateway I've
been building for managing a fleet of my own Toit nodes. Not Artemis, not
Jaguar — think of it as the *northbound* side: something that sits on your LAN,
queues commands to nodes, ships container images to them, and ingests their
telemetry. What you're looking at is live — two physical ESP32s on my network
right now, checking in."

**[0:25 — SCREEN: hover the check-in gauges]**
"The one idea behind it: porta owns **one wire protocol**, and any node that
speaks it shows up here. porta never imports the node's code — they're coupled
*only* over UDP and TFTP. So the fleet can be heterogeneous. Today these are Toit
nodes; I've got a Smalltalk one planned, and porta wouldn't know the difference."

**[0:50 — SCREEN: click into the `vin` node detail page]**
"Here's a node. This is a Vindriktning air-quality sensor running an always-on
Toit container. porta shows what it *observes* — the chip, the SDK version, the
containers actually installed with their CRCs — versus what I've *asked* for.
Those images get delivered over TFTP as raw relocated image bytes; the size and
CRC ride in the run command and the node verifies them on commit."

**[1:25 — SCREEN: a live telemetry row lands in the panel]**
"And it's reporting telemetry up — there, a PM2.5 reading just came in, every
sixty seconds or so. Any scalar the node wants to push lands in here,
timestamped."

**[1:50 — SCREEN: scroll to the two-column Logs / Prints panels]**
"This is the bit I think Toit folks will appreciate. Forwarded prints on one
side, logs on the other. And when a node panics—" **[hover a `panic` row, point
at `[decode ↗]`]** "—porta shows the raw base64 system message, but it does
*not* try to decode it, because porta has no toit toolchain in it at all.
Instead that's a link that hands the blob to the node's own dev tool to run
`jag decode` against the local snapshot. So the gateway stays totally
language-neutral, and you still get a readable stack trace. That decode handler
is the next thing I'm building on the node side."

**[2:35 — SCREEN: queue a command, e.g. a `set` or `reboot`, watch the badge]**
"Control goes the other way through a command queue — set a config value,
reboot, install a container. Each command gets a lifecycle: queued, delivered,
and for config, converged once the node echoes it back. There's a self-heal loop
that re-issues anything that drifts."

**[3:05 — SCREEN: the `docs/ARCHITECTURE.md` 3-plane diagram]**
"Under the hood it's three planes — commands down, images over TFTP, telemetry
up — a Go server with a SQLite store, an htmx dashboard, a CLI, and an HTTP API.
It's pre-1.0, it's a hobby project, and the node side lives in a separate repo
called nodus."

**[3:25 — SCREEN: back to the live fleet page]**
"That's it. I mostly want to know — is anyone else here doing fleet-y stuff with
their own Toit nodes and would something like this be useful? Or am I
reinventing Artemis for fun? Genuinely curious — drop a comment. Happy to share
the repo if there's interest."
