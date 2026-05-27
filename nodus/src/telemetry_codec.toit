// device/telemetry_codec.toit — encodes telemetry entries as JSONL (one entry per
// line) for the data?id= WRQ. JSONL keeps the device side streaming (encode one
// entry at a time) and the gateway side bounded (decode + insert one line at a time).
import encoding.json
import io.buffer show Buffer

/** Encodes $entries (each a Map) as JSONL: json.encode each, newline-separated. */
build-data-body entries/List -> ByteArray:
  buf := Buffer
  entries.do: | e/Map |
    buf.write (json.encode e)
    buf.write "\n"
  return buf.bytes
