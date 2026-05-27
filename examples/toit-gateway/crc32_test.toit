import expect show *
import .crc32 show crc32

main:
  // Canonical CRC32-IEEE check value for the ASCII string "123456789".
  expect-equals 0xCBF4_3926 (crc32 "123456789".to-byte-array)
  // Empty input: initial 0xffffffff XOR-ed with 0xffffffff is 0.
  expect-equals 0 (crc32 #[])
  // Stability: same bytes → same value.
  bytes := #[0xde, 0xad, 0xbe, 0xef]
  expect-equals (crc32 bytes) (crc32 bytes)
