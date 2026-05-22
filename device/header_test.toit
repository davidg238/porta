import expect show *
import .header

main:
  // size=5, crc32=0xAABBCCDD, then 5 image bytes
  blob := #[5,0,0,0, 0xDD,0xCC,0xBB,0xAA, 1,2,3,4,5]
  h := parse-header blob
  expect-equals 5 h.size
  expect-equals 0xAABBCCDD h.crc32
  expect-equals #[1,2,3,4,5] (blob[8 .. 8 + h.size])

  // Short-buffer guard: fewer than 8 bytes must throw.
  expect-throw "blob too short: 3 < 8": parse-header #[1,2,3]
