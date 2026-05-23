import expect show *
import .handler show parse-resource_

main:
  // No query → base only, empty params.
  bare := parse-resource_ "commands"
  expect-equals "commands" bare[0]
  expect-structural-equals {:} bare[1]

  // Query → base + decoded params (insertion order irrelevant for a Map).
  full := parse-resource_ "payload?id=a0b1c2d3e4f5&name=blink&crc=12345"
  expect-equals "payload" full[0]
  expect-structural-equals {"id": "a0b1c2d3e4f5", "name": "blink", "crc": "12345"} full[1]

  // A bare key with no '=' maps to the empty string.
  flag := parse-resource_ "report?id=abc&verbose"
  expect-equals "report" flag[0]
  expect-equals "abc" flag[1]["id"]
  expect-equals "" flag[1]["verbose"]
