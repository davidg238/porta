import io show LITTLE-ENDIAN

/**
Parsing of the 8-byte self-describing blob header that prefixes every porta firmware blob.
*/

/** Size in bytes of the self-describing blob header prepended to every firmware image. */
HEADER-SIZE ::= 8

/**
Parsed 8-byte self-describing blob header.

The blob wire format is:
  `[0:4]` image size, little-endian u32
  `[4:8]` CRC32-IEEE of the image bytes, little-endian u32
  `[8:]`  image bytes
*/
class Header:
  /** The number of image bytes following the 8-byte header. */
  size/int
  /** CRC32-IEEE of the image bytes, as captured from the host. */
  crc32/int

  constructor .size .crc32:

/**
Parses the 8-byte header prepended to a TFTP firmware blob.

Reads $bytes `[0:4]` as a little-endian u32 image size and `[4:8]` as a
little-endian u32 CRC32-IEEE value. The image bytes follow at `[8:]`.

Throws if $bytes is fewer than 8 bytes.
*/
parse-header bytes/ByteArray -> Header:
  if bytes.size < HEADER-SIZE: throw "blob too short: $bytes.size < $HEADER-SIZE"
  size := LITTLE-ENDIAN.uint32 bytes 0
  crc32 := LITTLE-ENDIAN.uint32 bytes 4
  return Header size crc32
