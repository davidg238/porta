import expect show *
import .store show Store encode-json_ decode-json_
import .command show Command project

main:
  store := Store.open ":memory:"
  dev := "aabbccddeeff"

  // Operator seeds a programming session.
  image := #[0x01, 0x02, 0x03, 0x04]
  crc := 99
  store.register-payload --crc=crc --name="blink" --image=image
  store.enqueue-command dev (Command.run --name="blink" --crc=crc --triggers={"interval": 5}) --issued-by="cli" --now=10
  store.enqueue-command dev (Command.set-poll-interval --interval-s=1) --issued-by="cli" --now=11

  // A node wakes: record contact, drain the queue to exhaustion, applying each.
  store.touch-node dev --source-addr="10.0.0.5:6969" --now=100
  applied := []
  while true:
    next := store.next-undelivered dev
    if next == null: break
    applied.add (Command next["verb"] next["args"])
    // A run pulls the payload it lacks.
    if next["verb"] == "run":
      expect (store.payload-exists next["args"]["crc"])
    store.mark-delivered next["id"] --now=101

  // Every command was delivered exactly once, in FIFO order.
  expect-equals 2 applied.size
  expect-equals "run" applied[0].verb
  expect-equals "set-poll-interval" applied[1].verb
  expect (store.undelivered-commands dev).is-empty
  (store.command-log dev).do: | e/Map | expect-equals 101 e["delivered_at"]

  // The node reports its converged state; the gateway caches it.
  goal := project applied
  store.insert-report dev --observed-state=(encode-json_ {"apps": goal}) --health=(encode-json_ {"wakes": 1}) --now=200
  node := store.node dev
  expect-equals 200 node["last_report_at"]
  observed := decode-json_ node["observed_state"]
  expect-equals crc observed["apps"]["blink"]["crc"]

  store.close
  print "integration OK"
