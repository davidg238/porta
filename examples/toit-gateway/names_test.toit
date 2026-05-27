import expect show *
import .names show node-name-for

main:
  mac := "a0b1c2d3e4f5"
  // Deterministic: same MAC → same name.
  expect-equals (node-name-for mac) (node-name-for mac)
  // Shape: "adjective-noun".
  name := node-name-for mac
  expect (name.contains "-")
  // Different MACs usually differ (these two are chosen to differ).
  expect-not-equals (node-name-for "000000000001") (node-name-for "ffffffffffff")
