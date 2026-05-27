// device/telemetry_buffer_test.toit
import expect show *
import nodus.telemetry_buffer show TelemetryBuffer

main:
  buf := TelemetryBuffer --cap=3
  buf.add {"kind": "log", "text": "a"}
  buf.add {"kind": "metric", "name": "pm", "value": 13.0}
  expect-equals 2 buf.size

  // drain returns all entries (oldest first) and empties the buffer.
  out := buf.drain
  expect-equals 2 out.size
  expect-equals "a" out[0]["text"]
  expect-equals 0 buf.size
  expect-equals 0 (buf.drain).size                 // empty drain → no entries

  // Overflow drops the oldest and prepends a dropped-count marker on the next drain.
  4.repeat: | i | buf.add {"kind": "log", "text": "$i"}   // cap=3, so "0" is dropped
  dumped := buf.drain
  expect-equals 4 dumped.size                       // 1 marker + 3 survivors
  expect-equals "log" dumped[0]["kind"]
  expect (dumped[0]["text"].contains "dropped 1")
  expect-equals "1" dumped[1]["text"]               // oldest survivor

  // cap < 1 is rejected.
  expect-throw "INVALID_ARGUMENT": TelemetryBuffer --cap=0

  // cap=1: each add after the first evicts the previous entry.
  b1 := TelemetryBuffer --cap=1
  b1.add {"kind": "log", "text": "x"}
  b1.add {"kind": "log", "text": "y"}   // "x" dropped
  d1 := b1.drain
  expect-equals 2 d1.size               // 1 marker + "y"
  expect (d1[0]["text"].contains "dropped 1")
  expect-equals "y" d1[1]["text"]

  print "telemetry buffer OK"
