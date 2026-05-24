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
  print "telemetry codec OK"
