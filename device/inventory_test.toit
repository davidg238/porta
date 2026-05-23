// device/inventory_test.toit
import expect show *
import uuid
import .goal_state show GoalState App
import .triggers show Triggers
import .inventory show Inventory InstalledApp

make-goal crc/int -> GoalState:
  t := Triggers --interval-s=60
  a := App --name="payload" --size=10 --crc=crc --triggers=t --runlevel=3
  return GoalState {"payload": a}

main:
  id := uuid.Uuid.uuid5 "porta" "x"

  // empty inventory → everything must be fetched
  r0 := Inventory.empty.reconcile (make-goal 111)
  expect-equals 1 r0.to-fetch.size
  expect-equals 0 r0.to-schedule.size

  // matching crc → schedule from flash, no fetch
  t60 := Triggers --interval-s=60
  installed-payload := InstalledApp --name="payload" --id=id --size=10 --crc=111 --triggers=t60 --runlevel=3
  inv := Inventory {"payload": installed-payload}
  r1 := inv.reconcile (make-goal 111)
  expect-equals 0 r1.to-fetch.size
  expect-equals 1 r1.to-schedule.size

  // changed crc → fetch
  r2 := inv.reconcile (make-goal 222)
  expect-equals 1 r2.to-fetch.size

  // app removed from goal → to-remove
  r3 := inv.reconcile (GoalState {:})
  expect-equals 1 r3.to-remove.size

  // encode/decode round-trip
  tree := inv.encode
  back := Inventory.decode tree
  expect-equals 111 (back.apps["payload"] as InstalledApp).crc
  expect-equals id (back.apps["payload"] as InstalledApp).id
  expect-equals 60 (back.apps["payload"] as InstalledApp).triggers.interval-s
