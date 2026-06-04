// Copyright (c) 2026 Ekorau LLC

// gateway/crc32.toit — CRC32-IEEE, byte-identical to the device-side image check.
import crypto.crc

/**
Computes the CRC32-IEEE checksum of $bytes.

Uses the same parameters as the device's image verifier
  (device/image_writer.toit) and jaguar's X-Jaguar-CRC32, so a value computed
  here matches what the node recomputes while streaming the image.
*/
crc32 bytes/ByteArray -> int:
  summer := crc.Crc.little-endian 32
      --polynomial=0xEDB88320
      --initial-state=0xffff_ffff
      --xor-result=0xffff_ffff
  summer.add bytes
  return summer.get-as-int
