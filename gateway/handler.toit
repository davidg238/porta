// gateway/handler.toit — the store-backed TFTP request handler. Implements the
// tftp package's Storage (as evolved by Spec A): it parses the "?id=<mac>&…"
// query the node sends, serves the oldest-undelivered command and payload BLOBs
// on RRQ, ingests a state report on WRQ, and marks a command delivered on the
// RRQ transfer-complete event.

import io
import io.buffer show Buffer
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

  writer-for name/string --req/Request?=null --tsize-hint/int?=null -> io.CloseableWriter:
    throw STORAGE-ACCESS-DENIED
