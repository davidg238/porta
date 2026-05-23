import expect show *
import .handler show StoreBackedHandler parse-resource_
import .store show Store decode-json_
import .command show Command
import tftp show Peer RRQ STORAGE-FILE-NOT-FOUND STORAGE-ACCESS-DENIED

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

  store := Store.open ":memory:"
  handler := StoreBackedHandler store
  now := 1000

  // Unknown node, empty queue: a "commands" RRQ yields a zero-byte body (drain sentinel).
  r0 := handler.reader-for "commands?id=aabbccddeeff"
  expect-equals null r0.read   // immediate EOF == zero bytes
  r0.close

  // Enqueue one command; the next "commands" RRQ serves its exact wire bytes.
  store.ensure-node "aabbccddeeff" --now=now
  cmd := Command.set-poll-interval --interval-s=1
  store.enqueue-command "aabbccddeeff" cmd --issued-by="test" --now=now
  r1 := handler.reader-for "commands?id=aabbccddeeff"
  expect-equals cmd.encode r1.read
  expect-equals null r1.read
  r1.close

  // Register a payload; a "payload" RRQ for its crc streams the image bytes.
  store.register-payload --crc=999 --name="blink" --image=#[1, 2, 3, 4]
  rp := handler.reader-for "payload?id=aabbccddeeff&name=blink&crc=999"
  expect-equals #[1, 2, 3, 4] rp.read
  rp.close

  // A payload RRQ for an unknown crc throws the not-found sentinel.
  expect-throw STORAGE-FILE-NOT-FOUND: handler.reader-for "payload?id=aabbccddeeff&name=blink&crc=7"

  // exists/size: commands always readable (size unknown); payload sized by the BLOB.
  expect (handler.exists "commands?id=aabbccddeeff")
  expect-equals null (handler.size "commands?id=aabbccddeeff")
  expect (handler.exists "payload?id=x&crc=999")
  expect-equals 4 (handler.size "payload?id=x&crc=999")
  expect-equals null (handler.size "payload?id=x&crc=7")
  store.close

  // A WRQ to "report" buffers the body and, on close, records observed apps + health.
  store2 := Store.open ":memory:"
  h2 := StoreBackedHandler store2
  store2.ensure-node "aabbccddeeff" --now=2000
  body := #[]
  body = "{\"apps\":{\"blink\":{\"crc\":999,\"runlevel\":3}},\"health\":{\"wakes\":4}}".to-byte-array
  w := h2.writer-for "report?id=aabbccddeeff"
  w.write body
  w.close
  reps := store2.reports "aabbccddeeff"
  expect-equals 1 reps.size
  observed := decode-json_ reps[0]["observed_state"]
  expect-equals 999 observed["apps"]["blink"]["crc"]
  health := decode-json_ reps[0]["health"]
  expect-equals 4 health["wakes"]
  // The node row's cached observed_state was refreshed too.
  node := store2.node "aabbccddeeff"
  expect ((decode-json_ node["observed_state"])["apps"].contains "blink")
  // A WRQ to anything but "report" is refused.
  expect-throw STORAGE-ACCESS-DENIED: h2.writer-for "payload?id=aabbccddeeff&crc=1"
  store2.close
