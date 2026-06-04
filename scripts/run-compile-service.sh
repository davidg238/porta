#!/usr/bin/env bash
# Copyright (c) 2026 Ekorau LLC
# Start the ST compile service that jast-gw calls over HTTP.
# Builds the tree-sitter parser first (idempotent), then runs the service.
# Run from anywhere; paths are resolved relative to the repo.
set -euo pipefail

# repo root = two levels up from tools/jast-gw/scripts
ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"

"$ROOT/transpiler/tree-sitter-smalltalk/build.sh"
exec python3 "$ROOT/transpiler/compile_service.py" "$@"
