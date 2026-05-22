# porta

The **jast** gateway as its own project — split from `st-zephyr`, and the home
for rewriting the gateway in Toit.

Start with **`CLAUDE.md`** for orientation, then `docs/specs/` for the approved
design and `docs/plans/` for the implementation plan.

- `gateway/` — existing Go gateway (copied from `st-zephyr/tools/jast-gw`)
- `device/` — Toit on-device loader (the keeper)
- `host/`   — throwaway smoke-test harness (jag-image capture sink + TFTP server)
- `st-zephyr` — local symlink to the parent project (gitignored)
