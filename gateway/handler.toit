// gateway/handler.toit — the store-backed TFTP request handler. Implements the
// tftp package's Storage (as evolved by Spec A): it parses the "?id=<mac>&…"
// query the node sends, serves the oldest-undelivered command and payload BLOBs
// on RRQ, ingests a state report on WRQ, and marks a command delivered on the
// RRQ transfer-complete event.

import io
import io.buffer show Buffer
import encoding.json
import tftp show Storage Request Peer RRQ STORAGE-FILE-NOT-FOUND STORAGE-ACCESS-DENIED
import .store show Store encode-json_ decode-json_
import .command show Command

/**
Splits a raw resource name "base?k=v&k2=v2" into [base/string, params/Map].

A key with no "=" maps to the empty string. The node sends its identity and
  payload selector as this query suffix (e.g. "payload?id=<mac>&name=<n>&crc=<c>").
*/
parse-resource_ raw/string -> List:
  q := raw.index-of "?"
  if q < 0: return [raw, {:}]
  base := raw[..q]
  params := {:}
  (raw[q + 1..].split "&").do: | kv/string |
    eq := kv.index-of "="
    if eq < 0: params[kv] = ""
    else: params[kv[..eq]] = kv[eq + 1..]
  return [base, params]

/**
An $io.CloseableReader over a fixed $ByteArray (or zero bytes when given null).

The engine reads the body once; a null payload yields immediate EOF, which is
  how the handler signals "command queue drained" — a zero-byte RRQ body.
*/
class BytesReader_ extends io.CloseableReader:
  bytes_/ByteArray? := ?
  constructor .bytes_:
  read_ -> ByteArray?:
    b := bytes_
    bytes_ = null
    return b
  close_ -> none:

/** Current wall-clock time in epoch seconds. */
now_ -> int: return Time.now.s-since-epoch

/** A $Storage backed by the gateway's sqlite $Store. */
class StoreBackedHandler extends Storage:
  store_/Store
  constructor .store_:

  exists name/string --req/Request?=null -> bool:
    base := (parse-resource_ name)[0]
    return base == "commands" or base == "payload"

  size name/string --req/Request?=null -> int?:
    parsed := parse-resource_ name
    if parsed[0] != "payload": return null   // "commands" body is dynamic.
    crc := int.parse (parsed[1].get "crc" --if-absent=: "") --if-error=: return null
    p := store_.payload crc
    return p == null ? null : p["size"]

  reader-for name/string --req/Request?=null -> io.CloseableReader:
    parsed := parse-resource_ name
    base := parsed[0]
    params := parsed[1]
    id := params.get "id"
    if id != null: store_.touch-node id --now=now_
    if base == "commands":
      next := id == null ? null : store_.next-undelivered id
      if next == null: return BytesReader_ null
      return BytesReader_ (Command next["verb"] next["args"]).encode
    if base == "payload":
      crc := int.parse (params.get "crc" --if-absent=: "") --if-error=: throw STORAGE-FILE-NOT-FOUND
      p := store_.payload crc
      if p == null: throw STORAGE-FILE-NOT-FOUND
      return BytesReader_ p["image"]
    throw STORAGE-FILE-NOT-FOUND

  on-transfer-complete --op/int --resource/string --peer/Peer --bytes/int --ok/bool -> none:
    if not ok: return
    if op != RRQ: return
    parsed := parse-resource_ resource
    if parsed[0] != "commands": return
    id := parsed[1].get "id"
    if id == null: return
    next := store_.next-undelivered id
    if next == null: return  // Drain-sentinel transfer: nothing to mark.
    store_.mark-delivered next["id"] --now=now_

  writer-for name/string --req/Request?=null --tsize-hint/int?=null -> io.CloseableWriter:
    parsed := parse-resource_ name
    base := parsed[0]
    id := parsed[1].get "id"
    if id == null: throw STORAGE-ACCESS-DENIED
    store_.touch-node id --now=now_
    if base == "report": return ReportWriter_ store_ id now_
    if base == "data": return DataWriter_ store_ id now_
    throw STORAGE-ACCESS-DENIED

/**
An $io.CloseableWriter that buffers a WRQ "report" body and, on close, splits it
  into the observed-app state and the health struct and records both via
  $Store.insert-report. The body is one JSON object {"apps":{…}, "health":{…}}.
*/
class ReportWriter_ extends io.CloseableWriter:
  store_/Store
  id_/string
  now_/int
  buffer_/Buffer := Buffer
  constructor .store_ .id_ .now_:

  try-write_ data/io.Data from/int to/int -> int:
    buffer_.write data from to
    return to - from

  close_ -> none:
    obj := decode-json_ buffer_.bytes.to-string
    apps := obj.get "apps" --if-absent=: {:}
    health := obj.get "health" --if-absent=: {:}
    store_.insert-report id_
        --observed-state=(encode-json_ {"apps": apps})
        --health=(encode-json_ health)
        --now=now_

/**
An $io.CloseableWriter that buffers a WRQ "data" body (JSONL — one telemetry entry
  per line) and, on close, decodes each line and appends it to the data_log. A line
  that fails to decode (e.g. a truncated final line) is skipped, so a short tail
  costs only that line. Each entry is {"ts"?,"seq"?,"kind","name"?,"value"?,"text"?};
  missing ts/seq default to the gateway receive time / line index.

  The decoded "value" field's runtime type is preserved via $Store.insert-data's
  value_type tag: bool→0/1 + "bool"; int→int + "int"; float→float + "float";
  string→goes into the text column + "string"; absent/null→value null, type null.
*/
class DataWriter_ extends io.CloseableWriter:
  store_/Store
  id_/string
  now_/int
  buffer_/Buffer := Buffer
  constructor .store_ .id_ .now_:

  try-write_ data/io.Data from/int to/int -> int:
    buffer_.write data from to
    return to - from

  close_ -> none:
    line-no := 0
    (buffer_.bytes.to-string.split "\n").do: | line/string |
      line = line.trim
      if line == "": continue.do
      entry/Map? := null
      catch: entry = json.decode line.to-byte-array
      if entry is not Map: continue.do
      raw := entry.get "value"
      text := entry.get "text"
      value/num? := null
      value-type/string? := null
      if raw is bool:
        value = raw ? 1 : 0
        value-type = "bool"
      else if raw is int:
        value = raw
        value-type = "int"
      else if raw is float:
        value = raw
        value-type = "float"
      else if raw is string:
        text = raw
        value-type = "string"
      // else raw == null: a log line or a valueless entry — leave value/value-type null.
      store_.insert-data id_
          --ts=(entry.get "ts" --if-absent=: now_)
          --seq=(entry.get "seq" --if-absent=: line-no)
          --kind=(entry.get "kind" --if-absent=: "log")
          --name=(entry.get "name")
          --value=value
          --text=text
          --value-type=value-type
      line-no++
