import expect show *
import .store show Store DEFAULT-POLL-INTERVAL-S DEFAULT-MAX-OFFLINE-S

main:
  store := Store.open ":memory:"
  // The four M1 tables exist after open.
  expect (store.has-table_ "nodes")
  expect (store.has-table_ "payloads")
  expect (store.has-table_ "command_queue")
  expect (store.has-table_ "reports")
  // touch-node creates a row on first contact (auto-named, default windows).
  store.touch-node "aabbccddeeff" --source-addr="10.0.0.5:6969" --now=1000
  n := store.node "aabbccddeeff"
  expect-equals "aabbccddeeff" n["id"]
  expect (n["name"].contains "-")                 // auto-named
  expect-equals 1000 n["first_seen"]
  expect-equals 1000 n["last_seen"]
  expect-equals DEFAULT-POLL-INTERVAL-S n["poll_interval_s"]
  expect-equals DEFAULT-MAX-OFFLINE-S n["max_offline_s"]

  // A second contact updates last_seen, not first_seen.
  store.touch-node "aabbccddeeff" --now=2000
  expect-equals 1000 (store.node "aabbccddeeff")["first_seen"]
  expect-equals 2000 (store.node "aabbccddeeff")["last_seen"]

  // ensure-node creates a never-contacted row (last_seen stays null).
  store.ensure-node "010203040506" --now=1500
  expect-equals null (store.node "010203040506")["last_seen"]

  // lookups, listing, and setters.
  expect-equals null (store.node "ffffffffffff")              // unknown → null
  expect-equals "aabbccddeeff" (store.node-by-name (store.node "aabbccddeeff")["name"])["id"]
  expect-equals 2 store.nodes.size
  store.set-node-name "aabbccddeeff" "kitchen"
  expect-equals "kitchen" (store.node "aabbccddeeff")["name"]
  store.set-max-offline "aabbccddeeff" 60
  expect-equals 60 (store.node "aabbccddeeff")["max_offline_s"]
  store.set-poll-interval-intended "aabbccddeeff" 1
  expect-equals 1 (store.node "aabbccddeeff")["poll_interval_s"]

  store.close
