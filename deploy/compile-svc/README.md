# Gateway deployment (gw85224-01)

The gateway is two cooperating services on the same host:

- **`jast-gw`** — the Go broker (TFTP :5683, CLI :5684, MCP/SSE :5685). Binary at
  `/usr/local/bin/jast-gw`, data in `/var/lib/jast-gw/`, runs as `david`.
- **`jast-compile-svc`** — the Python ST compile service (HTTP :5686, loopback).
  Runs `transpiler/compile_service.py` from the repo checkout at
  `/home/david/st-zephyr`. `jast-gw` calls it via `-compile-url`
  (default `http://127.0.0.1:5686`), so the gateway needs no Python itself.

The compile service replaces the gateway's old shell-out to `python3
st_compiler.py`. See `docs/superpowers/plans/2026-05-20-gateway-compile-service.md`.

## Host prerequisites

The compile service needs, on the host:
- `python3` (Debian 12 ships it).
- A C compiler (`cc` / `build-essential`) — `build.sh` compiles the tree-sitter
  parser to `smalltalk.so` on first start.
- The Node tree-sitter CLI is **only** needed if `grammar.js` changes
  (regenerates `parser.c`); a normal `git pull` ships an up-to-date `parser.c`,
  so `build.sh` just runs `cc`.

## Install the compile service

```bash
# On gw85224-01, with the repo at /home/david/st-zephyr:
sudo cp /home/david/st-zephyr/tools/jast-gw/deploy/jast-compile-svc.service \
        /etc/systemd/system/jast-compile-svc.service
sudo systemctl daemon-reload
sudo systemctl enable --now jast-compile-svc.service

# Verify
systemctl status jast-compile-svc.service
curl -s localhost:5686/health   # -> ok
```

If the repo lives at a different path, edit `WorkingDirectory`, `ExecStartPre`,
and `ExecStart` in the unit accordingly.

## Order jast-gw after the compile service

Add an ordering + soft dependency to the existing
`/etc/systemd/system/jast-gw.service` `[Unit]` section:

```ini
[Unit]
After=jast-compile-svc.service
Wants=jast-compile-svc.service
```

`Wants` (not `Requires`) is deliberate: if the compile service is down, only
`run_st` / `compile_and_push` fail (with a clear "compile service unreachable"
error) — the rest of the gateway (TFTP, CLI, device registry) keeps working.
`jast-gw` already defaults `-compile-url` to `http://127.0.0.1:5686`, so no
`ExecStart` change is required.

```bash
sudo systemctl daemon-reload
sudo systemctl restart jast-gw.service
```

## Smoke test end-to-end

```bash
# compile service alone
curl -s -X POST localhost:5686/compile -H 'Content-Type: application/json' \
  -d '{"source":"x := 42.","symbols":false}' | head -c 60; echo

# through the gateway (from the dev box, via the MCP run_st tool against a device)
```
