# porta §4 removal — handoff brief

**Task:** Drop the Toit-specific dev-machine CLI from `porta` now that the `nodus`
tool owns it.

This was deliberately sequenced **after** `nodus run` / `monitor` / `panic` were
proven at parity — that is now **done and HW-verified** (nodus C3 merged to
`origin/master` @ `eb0c12c`; `flash` + `run` + `monitor` + `panic` all live-tested on
real hardware 2026-06-03). So removing porta's versions leaves **no capability gap**,
provided dev users switch to the `nodus` binary for run/monitor/panic/flash.

---

## ⚠ Critical distinction — do NOT confuse these

| Thing | What it is | Action |
|---|---|---|
| `run` **wire verb** (`docs/PROTOCOL.md` §2.1, `VERB-RUN`) | gateway→node install/run command; every node (incl. nodus's supervisor) depends on it | **KEEP UNTOUCHED** |
| `porta run` **CLI subcommand** (`internal/portacli/run.go`) | dev-machine compile+deploy convenience | **REMOVE** |
| `kind:"panic"` **telemetry contract** (PROTOCOL.md) | neutral wire shape any node honours | **KEEP** |
| `porta panic` **CLI browser** + jag-decode hook | dev-machine panic viewer | **REMOVE** |

A vague "remove run/panic" risks gutting the protocol. Remove only the **CLI** + the
**toolchain** + the **decode rendering**; never the wire verbs or report schema.

---

## Scope (all under `~/workspaceToit/porta`)

1. **`internal/portacli/root.go`** — in `NewRootCmd`'s `AddCommand(...)`, delete the
   lines `newRunCmd(),` and `newPanicCmd(),`. KEEP
   `serve`/`scan`/`ping`/`device`/`container`/`log`/`monitor`.

2. **Delete files** (CLI + their tests):
   - `internal/portacli/run.go`, `run_test.go`
   - `internal/portacli/panic.go`, `panic_test.go`
   - `internal/portacli/decode.go`, `decode_test.go` — jag-decode rendering; used only
     by `panic.go` + `monitor.go`'s hook.
   - `internal/toolchain/` — the **entire dir** (`build.go`, `sdk.go`, `retain.go` +
     `_test.go`). Only `run.go` imports it (verified).

3. **`internal/portacli/monitor.go`** — *surgery*, keep the command, remove the
   panic-decode hook:
   - drop the `dec panicDecoder` param / `newJagDecoder()` / `renderPanic()` usage,
   - drop the `--no-decode` flag,
   - make `printMonitorRow` print `kind:"panic"` rows raw (no decode) — or keep printing
     via `telemetry.FormatLine`. Goal: porta `monitor` stays a neutral telemetry tail
     with **zero** dependency on `decode.go`.
   - Update `monitor_test.go` accordingly (remove decode-hook assertions).
   - NOTE: this is the one file you **edit** rather than delete, and it is **coupled** to
     deleting `decode.go` — do them together.

4. **Test fallout** — check `e2e_test.go` / `config_test.go` and any other `*_test.go`
   for references to the removed `run`/`panic`/`decode`/`toolchain` symbols; fix or
   delete those cases.

5. **Docs:**
   - **Delete** `docs/PANIC-REPORTING.md` (it moved to the nodus repo, reworded for the
     node ends).
   - In `docs/PROTOCOL.md`, reword the few `porta run` CLI mentions (≈lines 246–247
     chip/sdk usage, ≈line 395) to point at `nodus run` instead — but **do NOT touch**
     the `run` wire-verb definition (§2.1) or the `kind:"panic"` contract.
   - **Delete this handoff file** (`docs/S4-REMOVAL-HANDOFF.md`) as part of the change.

---

## Gate

```
cd ~/workspaceToit/porta
go build ./... && go vet ./... && go test ./...        # all green
go build -o porta ./cmd/porta
./porta --help                                          # lists serve/scan/ping/device/
                                                        # container/log/monitor; NO run/panic
```

---

## Process

- Work on a **feature branch**, not `master`.
- Spec/origin: the nodus dev-tool plan at
  `~/workspaceToit/nodus/docs/plans/2026-06-03-nodus-dev-tool-c2c3.md`
  ("Out of repo / follow-ups" section).
- Use the superpowers skills (`subagent-driven-development` if you break it into tasks;
  `finishing-a-development-branch` at the end).

## Most likely to bite if skipped

1. The **wire-verb-vs-CLI distinction** (the warning table above).
2. The **`monitor.go` surgery** — the only edited (not deleted) file, coupled to
   deleting `decode.go`.
