#!/usr/bin/env bash
# Copyright (c) 2026 Ekorau LLC
# Stage the porta gateway deploy kit: compile a fresh snapshot and gather the
# toit-sqlite runtime so `docker build` (here or on the target) has everything.
#
# Output: deploy/kit/  (gitignored — it holds ~56 MB of build artifacts)
#   Dockerfile
#   gateway.snapshot
#   bin/toit-sqlite
#   lib/...            (the SDK tree the binary launches)
#
# Transfer the whole kit/ to the target and build there:
#   rsync -a deploy/kit/ david@gw85224-01:~/porta-kit/
#   ssh david@gw85224-01 'cd ~/porta-kit && docker build -t porta-gw:latest .'
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$HERE/.." && pwd)"
SQLITE_BUILD="${SQLITE_BUILD:-$HOME/workspaceToit/sqlite/build}"
TS="$SQLITE_BUILD/bin/toit-sqlite"
KIT="$HERE/kit"

[ -x "$TS" ] || { echo "toit-sqlite not found at $TS (set SQLITE_BUILD)"; exit 1; }

echo "==> Compiling gateway snapshot"
rm -rf "$KIT"
mkdir -p "$KIT"
( cd "$REPO/examples/toit-gateway" && "$TS" compile --snapshot -o "$KIT/gateway.snapshot" gateway.toit )

echo "==> Staging toit-sqlite runtime (bin/ + lib/)"
mkdir -p "$KIT/bin"
cp "$SQLITE_BUILD/bin/toit-sqlite" "$KIT/bin/"
cp -a "$SQLITE_BUILD/lib" "$KIT/lib"

echo "==> Copying Dockerfile"
cp "$HERE/Dockerfile" "$KIT/Dockerfile"

echo "==> Kit ready: $KIT ($(du -sh "$KIT" | cut -f1))"
ls -la "$KIT"
