# Porta gateway container for gw85224-01 (and any glibc<2.38 host).
#
# Why a container: the prebuilt `toit-sqlite` binary links GLIBC up to 2.38,
# but gw85224-01 (Debian 12) ships glibc 2.36, so the binary cannot run on the
# host. ubuntu:24.04 carries glibc 2.39. The gateway snapshot bundles every
# Toit dependency, so the image needs only three things from the build kit:
#   * bin/toit-sqlite   — the custom Toit runtime with the sqlite external lib
#   * lib/              — the SDK tree the binary launches (../lib/.../toit.run)
#   * gateway.snapshot  — the compiled gateway
#
# The db lives on a mounted volume (/data) so it survives container rebuilds.
FROM ubuntu:24.04

# Keep the bin/ + lib/ sibling layout the binary expects: toit-sqlite resolves
# its runtime at ../lib/toit/bin/toit.run relative to its own location.
WORKDIR /opt/porta
COPY bin/ /opt/porta/bin/
COPY lib/ /opt/porta/lib/
COPY gateway.snapshot /opt/porta/gateway.snapshot

# Durable store mountpoint (override with -v /var/lib/porta:/data).
VOLUME /data

# Serve the command queue + payloads over TFTP/UDP on :6969.
# --network=host is required (TFTP uses ephemeral UDP ports), so :6969 binds
# the host directly.
EXPOSE 6969/udp
CMD ["/opt/porta/bin/toit-sqlite", "run", "/opt/porta/gateway.snapshot", \
     "--", "--db=/data/porta.db", "serve", "--port=6969"]
