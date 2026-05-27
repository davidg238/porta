// device/telemetry_codec_test.toit
import expect show *
import encoding.json
import .telemetry_codec show build-data-body

main:
  entries := [
    {"kind": "metric", "name": "pm", "value": 13.0},
    {"kind": "log", "text": "hi"},
  ]
  body := build-data-body entries
  // Body is JSONL: one decodable object per non-empty line, in order.
  lines := body.to-string.split "\n"
  // Trailing newline → a final empty element; filter it.
  decoded := []
  lines.do: | l/string | if l.trim != "": decoded.add (json.decode l.to-byte-array)
  expect-equals 2 decoded.size
  expect-equals "pm" decoded[0]["name"]
  expect-equals "hi" decoded[1]["text"]

  // Empty input → empty body.
  expect-equals 0 (build-data-body []).size

  // Type fidelity across the JSONL round-trip (the gateway infers value_type from
  // the decoded runtime type). Decode each line and check the runtime type.
  typed := build-data-body [
    {"kind": "metric", "name": "i", "value": 7},        // int
    {"kind": "metric", "name": "f", "value": 20.5},     // fractional float
    {"kind": "metric", "name": "w", "value": 13.0},     // whole-number float
    {"kind": "metric", "name": "b", "value": true},     // bool
    {"kind": "metric", "name": "s", "value": "x"},      // string
  ]
  tlines := []
  (typed.to-string.split "\n").do: | l/string | if l.trim != "": tlines.add (json.decode l.to-byte-array)
  expect (tlines[0]["value"] is int)
  expect (tlines[1]["value"] is float)
  expect (tlines[3]["value"] is bool)
  expect-equals "x" tlines[4]["value"]
  expect (tlines[4]["value"] is string)
  expect (tlines[2]["value"] is float)               // whole-number float stays float
  expect-equals 13.0 tlines[2]["value"]
  print "telemetry codec OK"
