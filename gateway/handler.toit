// gateway/handler.toit — the store-backed TFTP request handler. Implements the
// tftp package's Storage (as evolved by Spec A): it parses the "?id=<mac>&…"
// query the node sends, serves the oldest-undelivered command and payload BLOBs
// on RRQ, ingests a state report on WRQ, and marks a command delivered on the
// RRQ transfer-complete event.

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
