#!/usr/bin/env bash
# Copyright (c) 2026 Ekorau LLC
# Run the Toit gateway host test suite with the custom toit-sqlite runtime
# (the sqlite dep links an external C lib, so the stock `toit` runtime can't RUN it;
# stock `toit` still resolves packages fine). Override with TOIT_SQLITE=/path/to/bin.
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TS="${TOIT_SQLITE:-$HOME/workspaceToit/sqlite/build/bin/toit-sqlite}"
[ -x "$TS" ] || { echo "toit-sqlite not found at $TS (set TOIT_SQLITE)"; exit 1; }

cd "$HERE"
toit pkg install >/dev/null          # regenerate .packages (path + url deps)
fail=0
for t in *_test.toit; do
  printf '%-28s ' "$t"
  if "$TS" run "$t" >/tmp/toit-test.log 2>&1; then echo PASS; else echo FAIL; cat /tmp/toit-test.log; fail=1; fi
done
exit $fail
