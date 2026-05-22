import crypto.crc
import io
import system.containers
import uuid

/**
On-device flash-image helper lifted and adapted from jaguar's `flash-image`
  (jaguar/src/jaguar.toit).
*/

/**
Streams $image-size bytes from $reader into a transient `ContainerImageWriter`,
verifies the resulting CRC32-IEEE against $crc32 (reused verbatim from the TFTP
header), and commits the image, returning its UUID.

The $name parameter is intentionally unused in M1 (transient install — no named
registry). It is kept in the signature so that the M2 named-install task can add
the registry call without changing callers.

# Errors
Throws "truncated stream: expected N bytes, got M" if the reader closes before
  $image-size bytes have been delivered.
Throws "CRC32 mismatch" if the computed CRC32-IEEE digest does not equal $crc32.
  The writer is closed on both error paths.
*/
flash-image image-size/int reader/io.Reader name/string? --crc32/int -> uuid.Uuid:
  summer := crc.Crc.little-endian 32
      --polynomial=0xEDB88320
      --initial-state=0xffff_ffff
      --xor-result=0xffff_ffff
  written := 0
  writer := containers.ContainerImageWriter image-size
  while written < image-size:
    data := reader.read
    if not data: break
    summer.add data
    // Update written before writer.write — the RPC call may neuter the ByteArray,
    // zeroing data.size after the call (same subtlety as in jaguar).
    written += data.size
    writer.write data
  if written != image-size:
    writer.close
    throw "truncated stream: expected $image-size bytes, got $written"
  if summer.get-as-int != crc32:
    writer.close
    throw "CRC32 mismatch"
  return writer.commit
