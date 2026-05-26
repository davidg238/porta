import expect show *
import .pm1006 show pm1006-valid-frame pm1006-pm25 PM1006-FRAME-SIZE

/** Builds a valid 20-byte PM1006 frame carrying $pm25, with a correcting checksum byte. */
build-frame pm25/int -> ByteArray:
  f := ByteArray PM1006-FRAME-SIZE
  f[0] = 0x16; f[1] = 0x11; f[2] = 0x0b
  f[5] = (pm25 >> 8) & 0xff
  f[6] = pm25 & 0xff
  sum := 0
  19.repeat: sum += f[it]
  f[19] = (-sum) & 0xff   // Make the modulo-256 sum zero.
  return f

main:
  good := build-frame 42
  expect (pm1006-valid-frame good)
  expect-equals 42 (pm1006-pm25 good)
  // Two-byte value round-trips.
  big := build-frame 800
  expect (pm1006-valid-frame big)
  expect-equals 800 (pm1006-pm25 big)
  // Wrong length rejected.
  expect-not (pm1006-valid-frame (ByteArray 10))
  // Bad header rejected.
  bad-header := build-frame 42
  bad-header[0] = 0x00
  expect-not (pm1006-valid-frame bad-header)
  // Corrupted body (checksum no longer zero) rejected.
  bad-sum := build-frame 42
  bad-sum[10] = (bad-sum[10] + 1) & 0xff
  expect-not (pm1006-valid-frame bad-sum)
  print "pm1006 OK"
