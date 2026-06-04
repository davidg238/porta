#!/usr/bin/env bash
# Copyright (c) 2026 Ekorau LLC
# Build a porta .deb: the Go binary (CGO, dynamic — needs glibc >= 2.34 on the
# target, which any current Linux satisfies) + a native systemd unit. No
# container, no external packaging tool beyond dpkg-deb (standard on Debian/Ubuntu).
#
#   ./deploy/build-deb.sh                 # version 0.1.0
#   VERSION=0.1.0+gw ./deploy/build-deb.sh
#
# Output: deploy/dist/porta_<version>_amd64.deb
#
# Install on a target (e.g. gw85224-01):
#   scp deploy/dist/porta_*_amd64.deb david@gw85224-01:
#   ssh david@gw85224-01 'sudo apt install -y ./porta_*_amd64.deb'   # enables + starts porta.service
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$HERE/.." && pwd)"
VERSION="${VERSION:-0.1.0}"
ARCH="amd64"
PKG="porta_${VERSION}_${ARCH}"
DIST="$HERE/dist"
STAGE="$DIST/$PKG"

echo "==> Building porta binary (CGO dynamic, trimmed)"
rm -rf "$STAGE"
mkdir -p "$STAGE/DEBIAN" "$STAGE/usr/bin" "$STAGE/lib/systemd/system"
( cd "$REPO" && CGO_ENABLED=1 go build -trimpath -ldflags "-s -w" -o "$STAGE/usr/bin/porta" ./cmd/porta )

echo "==> Max glibc symbol the binary needs (must be <= target's glibc):"
objdump -T "$STAGE/usr/bin/porta" 2>/dev/null | grep -oE 'GLIBC_[0-9.]+' | sort -V | tail -1 || true

echo "==> Staging control + maintainer scripts + unit"
sed "s/__VERSION__/$VERSION/" "$HERE/debian/control" > "$STAGE/DEBIAN/control"
install -m 0755 "$HERE/debian/postinst" "$STAGE/DEBIAN/postinst"
install -m 0755 "$HERE/debian/prerm"    "$STAGE/DEBIAN/prerm"
install -m 0755 "$HERE/debian/postrm"   "$STAGE/DEBIAN/postrm"
install -m 0644 "$HERE/systemd/porta.service" "$STAGE/lib/systemd/system/porta.service"

echo "==> dpkg-deb --build"
dpkg-deb --root-owner-group --build "$STAGE" "$DIST/$PKG.deb" >/dev/null
echo "==> Built: $DIST/$PKG.deb"
dpkg-deb --info "$DIST/$PKG.deb"
echo "--- contents ---"
dpkg-deb --contents "$DIST/$PKG.deb"
