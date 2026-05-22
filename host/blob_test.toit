import expect show *
import .blob
import ..device.header

main:
  image := #[0xDE, 0xAD, 0xBE, 0xEF, 0x42]
  crc32 := 0xAABBCCDD

  framed := frame-blob image crc32

  // The framed blob must be exactly 8 bytes of header plus the image.
  expect-equals (8 + image.size) framed.size

  // Round-trip: parse the framed blob back and verify the field values.
  h := parse-header framed
  expect-equals image.size h.size
  expect-equals crc32 h.crc32

  // The image bytes at offset 8 must equal the original image.
  expect-equals image (framed[8 .. 8 + h.size])
