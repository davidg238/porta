// device/triggers_test.toit
import expect show *
import nodus.triggers show Triggers

main:
  t := Triggers.parse {"boot": 1, "interval": 60, "gpio-high:33": 33, "gpio-touch:4": 4}
  expect t.boot
  expect-equals 60 t.interval-s
  expect-equals [33] t.gpio-high
  expect-equals [4] t.touch
  expect-equals (1 << 33) t.ext1-high-mask

  // gpio-low parse and round-trip
  tl := Triggers.parse {"gpio-low:5": 5}
  expect-equals [5] tl.gpio-low
  tl2 := Triggers.parse tl.to-map
  expect-equals [5] tl2.gpio-low

  // round-trip through to-map
  t2 := Triggers.parse t.to-map
  expect t2.boot
  expect-equals 60 t2.interval-s
  expect-equals [33] t2.gpio-high

  // unknown trigger rejected
  expect-throw "unknown trigger: bogus": Triggers.parse {"bogus": 1}

  // empty triggers → all defaults
  e := Triggers.parse {:}
  expect-not e.boot
  expect-null e.interval-s
  expect-equals 0 e.ext1-high-mask
