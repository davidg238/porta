# Panic decode link (porta → nodus) — design

Date: 2026-06-07
Status: approved (brainstorm)
Scope: porta web UI only. Nodus-side handler is a documented handoff, not part of this spec.

## Problem

Node Toit panics arrive as telemetry rows with `kind:"panic"` whose payload is an
opaque base64 system message. Decoding it into a readable stack trace requires
`jag decode` plus the matching program `.snapshot` — context that lives **only on
the dev box that built the image**, never on porta (porta is language-neutral and
hosts zero toolchain). Today the porta Logs panel shows the raw blob and there is
no path from "I see a panic in the browser" to "I see the decoded trace."

During development the person reading panics is, by assumption, sitting at the box
that built the image. So the cheapest useful step is a **click-to-decode link**:
porta renders a clickable affordance on each panic row that hands the blob off to a
local `nodus` handler, which decodes against the local snapshot cache and shows the
result locally.

This is option (b) from the prior discussion (custom URL scheme, decode on the
authoring box). It deliberately does **not** centralize snapshots or write decoded
text back into porta — those are later, separate options.

## In scope (porta)

1. Render a `[decode ↗]` link on `panic` rows in the Logs panel.
2. Encode the blob into a `nodus://decode?…` URL.
3. Document the URL scheme as the porta→nodus tooling contract.
4. Tests.

## Out of scope

- The nodus-side URL-scheme handler + `nodus decode` implementation (separate repo;
  see Handoff below).
- Snapshot selection mechanics (entirely a nodus/`jag` concern).
- Write-back of decoded text into porta / showing decoded traces in the porta web UI
  for all viewers (the deferred "sidecar / annotation" option).
- Snapshot upload or centralization.
- Any change to the Prints panel, `telemetry.FormatLine`, `porta monitor` (CLI), the
  wire protocol (`docs/PROTOCOL.md`), or the DB schema.

## The affordance & URL contract

A `panic` row in the Logs panel renders as:

```
jun-07 10:50:01  panic   [decode ↗]  WyJNVBVVYU1UQdjI…zg7Fb
```

- `[decode ↗]` is an `<a>` placed **between** the `panic` column and the still-visible
  raw blob. The raw blob stays on the line (faithful to the raw-logs preference).
- The href is the shared porta→nodus contract:

  ```
  nodus://decode?node=<node-id>&blob=<url-encoded base64 blob>
  ```

  Built in Go with proper query-encoding (base64's `+ / =` percent-encoded), then
  emitted into the attribute by `html/template` (correct attribute-context escaping).

- **Minimal payload** (`node` + `blob` only), by YAGNI. No `sdk`/`crc` hint: `jag
  decode` resolves the matching snapshot from its local cache via the program uuid
  embedded in the panic message, and `node` is enough for the handler to label output
  or, if ever needed, query porta's API for the node's observed sdk/crc as a fallback.
  Hints can be added later without breaking existing links.

Why inline (vs a reference the handler fetches back from porta): no new porta API,
the blob is already in the row, and the dev box is on the same LAN/at the build box.
Caveat: a very large blob could exceed an OS URL-handler length limit; on Linux dev
boxes (`%u` via an XDG `.desktop` handler) that ceiling is high, so this is acceptable
for the initial step.

## Implementation shape

Approach A (chosen): structure only panic rows in the console view model. Smallest
blast radius; `telemetry.FormatLine` and the CLI monitor path are untouched (a
terminal can't click a link).

- **View model** (`internal/web/node_console.go`): the Logs path emits `[]consoleLine`
  instead of `[]string`. Each line is either:
  - a plain pre-formatted string (every non-panic row, via the unchanged
    `telemetry.FormatLine`), carried in `Text`; or
  - a structured panic line with:
    - `Pre` = `telemetry.FormatTS(ts) + "  " + fmt.Sprintf("%-7s ", "panic")`
      (reuses the exported `FormatTS`; byte-identical column spacing to `FormatLine`),
    - `DecodeHref` = the `nodus://decode?…` URL,
    - `Blob` = the raw base64 text.
  Non-panic rows leave `DecodeHref` empty.

- **Template** (`internal/web/templates/node_console.html`, `node-logs` define only):
  inside the existing `<pre class="console">`, render each line as

  ```
  {{if .DecodeHref}}{{.Pre}}<a href="{{.DecodeHref}}">[decode ↗]</a>  {{.Blob}}{{else}}{{.Text}}{{end}}
  ```

  followed by a newline. An `<a>` renders correctly inside `<pre>`.

- **Prints panel unchanged** — it carries only `print` rows, so its path/template keep
  the flat-string form. The `node-prints`/`node-logs` defines stay separate; only
  `node-logs` (and the Logs branch of the render helper) gains the conditional. If the
  shared render helper couples the two awkwardly, split the Logs render into its own
  small builder (decided at implementation time).

## Testing (`internal/web`, table-driven)

- A `panic` row in the Logs partial renders `[decode ↗]` wrapped in
  `<a href="nodus://decode?node=…&blob=…">`, with the raw blob still present and the
  link positioned between the `panic` column and the blob.
- The href's `blob` param is URL-encoded: feed a blob containing `+ / =`, assert it
  percent-encodes and round-trips back to the original via `url.ParseQuery`.
- A non-panic `log` row renders exactly as before — no link.
- The Prints panel renders no link.
- Existing Logs/Prints tests stay green.

## Docs

Document the `nodus://decode?node=&blob=` URL scheme as the porta→nodus tooling
contract in `docs/DEVSDK.md` (the tooling-side peer doc). One short subsection: the
scheme, params, encoding, and that porta only *emits* it (the handler is nodus's).

## Handoff — nodus side (separate repo, not this spec)

For the feature to work end-to-end, the nodus dev tool must:

1. Register a custom URL-scheme handler on the dev box. On Linux: ship/install a
   `.desktop` file with `MimeType=x-scheme-handler/nodus;` and `Exec=nodus decode %u`,
   then `xdg-mime default nodus.desktop x-scheme-handler/nodus`. (macOS: `Info.plist`
   `CFBundleURLTypes`; Windows: registry — out of scope, dev boxes are Linux.)
2. Implement `nodus decode <url>`: parse `node` and `blob` from the URL, base64 is the
   panic message, run `jag decode` against the local snapshot cache (jag resolves the
   snapshot by the program uuid embedded in the message).
3. **Local display UX:** show the decoded trace locally — a popup/window (or terminal)
   on the dev box, with a copy-to-clipboard button. No write-back to porta; the
   decoded text never leaves the dev box.

The browser shows a one-time "Open nodus?" confirmation on first click.
