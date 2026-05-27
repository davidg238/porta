#!/usr/bin/env bash
# nodus/host/build-envelope.sh — builds a no-jaguar firmware envelope whose sole
# boot container is the Porta supervisor.
#
# Produces firmware-esp32.envelope at the nodus package root with the supervisor
# installed (trigger=boot, default). Flash it with:
#   jag flash firmware-esp32.envelope --exclude-jaguar \
#     --wifi-ssid "<SSID>" --wifi-password "<PW>" --port /dev/ttyUSB0
#
# Requires toit + jag on PATH at SDK v2.0.0-alpha.192 (the envelope is matched to
# that SDK; a mismatch fails at install or boot).
#
# Word size: classic ESP32 (Xtensa) images are 32-bit here — confirmed empirically
# (a -m64 image is rejected by `container install` on this SDK's firmware-esp32).
set -euo pipefail

SDK_VERSION="v2.0.0-alpha.192"
ENV="firmware-esp32.envelope"

# Run from the nodus package root (parent of host/), regardless of invocation dir
# or whether nodus is a subdir of a monorepo or its own repo.
cd "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# 1. Prebuilt, SDK-matched envelope (downloaded once; reused if present).
if [ ! -f "$ENV" ]; then
  echo "downloading $ENV ($SDK_VERSION)..."
  curl -fL -o "$ENV.gz" \
    "https://github.com/toitlang/envelopes/releases/download/$SDK_VERSION/firmware-esp32.envelope.gz"
  gunzip -f "$ENV.gz"
fi

# 2. Supervisor → 32-bit binary image (classic ESP32 is 32-bit). Keep the
#    snapshot for crash decoding (`toit decode -s supervisor.snapshot <blob>`).
toit compile -s -o supervisor.snapshot src/supervisor.toit
toit tool snapshot-to-image -m32 --format=binary -o supervisor.image supervisor.snapshot

# 3. Install the supervisor as a boot container (default trigger=boot).
toit tool firmware -e "$ENV" container install supervisor supervisor.image

# 4. Show contents (expect 'supervisor' present, no 'jaguar').
toit tool firmware -e "$ENV" show
