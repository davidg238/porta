# nodus

`nodus` (Latin "knot/node") is the node side of the porta/nodus pair: an on-device
loader/supervisor — "the keeper" — plus node services, packaged as a Toit package. The
keeper polls a gateway over UDP/TFTP, installs the delivered container image, runs it per
its declared lifecycle, deep-sleeps, and re-polls. It is one of potentially several node
implementations; the gateway (the **porta** repo) owns the wire protocol that every node
conforms to — see porta's `docs/PROTOCOL.md`. Start with `CLAUDE.md` for orientation,
then `docs/` for the specs and plans.

## Quickstart

(run from the `nodus/` directory)

Run one host test suite:

```bash
toit tests/<name>_test.toit
```

Run all node suites:

```bash
for f in tests/*_test.toit examples/*/*_test.toit; do toit "$f"; done
```

Build an example payload image (snapshot → relocated ESP32 image):

```bash
toit compile -s -o X.snapshot examples/<name>/<file>.toit
toit tool snapshot-to-image -m32 --format=binary -o X.bin X.snapshot
```

Build the supervisor firmware envelope:

```bash
bash host/build-envelope.sh
```

The SDK is pinned at `v2.0.0-alpha.192` (see `host/SDK_VERSION`); payload images and the
device firmware must be built with that same SDK.
