// Copyright (c) 2026 Ekorau LLC

import expect show *
import .store show Store DEFAULT-POLL-INTERVAL-S DEFAULT-MAX-OFFLINE-S encode-json_ decode-json_
import .command show Command

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

  image := #[0xca, 0xfe, 0xba, 0xbe, 0x00, 0x01]
  expect-not (store.payload-exists 12345)
  store.register-payload --crc=12345 --name="blink" --image=image
  expect (store.payload-exists 12345)
  p := store.payload 12345
  expect-equals "blink" p["name"]
  expect-equals 6 p["size"]
  expect-equals image p["image"]                 // BLOB round-trips byte-identical
  // Re-register the same crc replaces (idempotent registration).
  store.register-payload --crc=12345 --name="blink" --image=#[0x01]
  expect-equals 1 (store.payload 12345)["size"]
  expect-equals null (store.payload 999)         // unknown crc → null

  dev := "aabbccddeeff"
  id1 := store.enqueue-command dev (Command.run --name="blink" --crc=7 --size=6 --triggers={"interval": 30}) --issued-by="cli" --now=3000
  id2 := store.enqueue-command dev (Command.stop --name="old") --issued-by="cli" --now=3001
  expect (id2 > id1)

  // FIFO: next-undelivered returns the oldest; mark-delivered advances it.
  first := store.next-undelivered dev
  expect-equals id1 first["id"]
  expect-equals "run" first["verb"]
  expect-equals 7 first["args"]["crc"]            // args decoded to a map
  expect-equals 2 (store.undelivered-commands dev).size
  store.mark-delivered id1 --now=3100
  expect-equals id2 (store.next-undelivered dev)["id"]
  expect-equals 1 (store.undelivered-commands dev).size

  // The log is the full audit history with delivery stamps.
  log := store.command-log dev
  expect-equals 2 log.size
  expect-equals 3100 log[0]["delivered_at"]
  expect-equals null log[1]["delivered_at"]

  // Queues are per device.
  expect-equals 0 (store.undelivered-commands "010203040506").size

  observed := encode-json_ {"apps": {"blink": {"crc": 7, "runlevel": 3}}}
  health := encode-json_ {"uptime_s": 12, "free_heap": 50000, "wakes": 3}
  store.insert-report "aabbccddeeff" --observed-state=observed --health=health --now=4000
  // The latest observed state + report time are cached on the node row.
  refreshed := store.node "aabbccddeeff"
  expect-equals 4000 refreshed["last_report_at"]
  decoded := decode-json_ refreshed["observed_state"]
  expect-equals 7 decoded["apps"]["blink"]["crc"]
  // The append-only reports table holds the history (newest first).
  reports := store.reports "aabbccddeeff"
  expect-equals 1 reports.size
  expect-equals 4000 reports[0]["ts"]

  store.close
