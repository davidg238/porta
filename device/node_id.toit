// device/node_id.toit — the node's stable identity for ?id=<mac> requests.

/**
Formats a base MAC ($mac, 6 bytes from esp32.mac-address) as 12 lowercase hex
  digits with no separators (e.g. #[0xa0,…] → "a0b1c2d3e4f5"). The base MAC is
  stable across deep-sleep and reflash, so it is the node's primary key.
*/
mac-to-id mac/ByteArray -> string:
  hex := "0123456789abcdef"
  out := ByteArray (mac.size * 2)
  mac.size.repeat: | i |
    b := mac[i]
    out[i * 2] = hex[(b >> 4) & 0xf]
    out[i * 2 + 1] = hex[b & 0xf]
  return out.to-string
