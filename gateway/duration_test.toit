import expect show *
import .duration show parse-duration-s

main:
  expect-equals 30 (parse-duration-s "30s")
  expect-equals 300 (parse-duration-s "5m")
  expect-equals 3600 (parse-duration-s "1h")
  expect-equals 172800 (parse-duration-s "2d")
  expect-equals 45 (parse-duration-s "45")        // bare integer = seconds
  expect-throw "invalid duration: ": parse-duration-s ""
  expect-throw "invalid duration unit: 10x": parse-duration-s "10x"
  expect-throw "invalid duration: ah": parse-duration-s "ah"
