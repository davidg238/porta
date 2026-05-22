import io show LITTLE-ENDIAN

/** Size in bytes of the self-describing blob header prepended to every firmware image. */
HEADER-SIZE ::= 8

/**
Host-side blob framing for the porta TFTP smoke test.

Produces the self-describing wire format consumed by the on-device loader:
  `[0:4]` image size, little-endian u32
  `[4:8]` CRC32-IEEE of the image bytes, little-endian u32
  `[8:]`  image bytes
*/

/**
Frames $image as a self-describing blob with an 8-byte header.

Prepends a little-endian u32 $image size and a little-endian u32 $crc32
before the image bytes.  The returned $ByteArray has length `image.size + 8`.
*/
frame-blob image/ByteArray crc32/int -> ByteArray:
  out := ByteArray HEADER-SIZE + image.size
  LITTLE-ENDIAN.put-uint32 out 0 image.size
  LITTLE-ENDIAN.put-uint32 out 4 crc32
  out.replace HEADER-SIZE image
  return out
