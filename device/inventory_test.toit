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

  // multi-app reconcile: keep (same crc → schedule), add (new → fetch), drop (absent → remove)
  t60b := Triggers --interval-s=60
  installed-keep := InstalledApp --name="keep" --id=id --size=10 --crc=111 --triggers=t60b --runlevel=3
  installed-drop := InstalledApp --name="drop" --id=id --size=10 --crc=222 --triggers=t60b --runlevel=3
  inv-multi := Inventory {"keep": installed-keep, "drop": installed-drop}
  t60c := Triggers --interval-s=60
  app-keep := App --name="keep" --size=10 --crc=111 --triggers=t60c --runlevel=3
  app-add := App --name="add" --size=20 --crc=333 --triggers=t60c --runlevel=3
  goal-multi := GoalState {"keep": app-keep, "add": app-add}
  r4 := inv-multi.reconcile goal-multi
  expect-equals 1 r4.to-fetch.size
  expect-equals 1 r4.to-schedule.size
  expect-equals 1 r4.to-remove.size
  expect-equals "add" (r4.to-fetch[0] as App).name
  expect-equals "keep" (r4.to-schedule[0] as InstalledApp).name
  expect-equals "drop" (r4.to-remove[0] as InstalledApp).name

  // encode/decode round-trip
  tree := inv.encode
  back := Inventory.decode tree
  expect-equals 111 (back.apps["payload"] as InstalledApp).crc
  expect-equals id (back.apps["payload"] as InstalledApp).id
  expect-equals 60 (back.apps["payload"] as InstalledApp).triggers.interval-s
  expect-equals "payload" (back.apps["payload"] as InstalledApp).name

  // to-goal-map reconstructs the goal-app map (GoalState.parse shape) from inventory.
  blink-id := uuid.Uuid #[0,1,2,3,4,5,6,7,8,9,10,11,12,13,14,15]
  a := InstalledApp --name="blink" --id=blink-id --size=2048 --crc=999 --triggers=(Triggers --interval-s=30) --runlevel=3
  gm := (Inventory {"blink": a}).to-goal-map
  expect-equals 2048 gm["blink"]["size"]
  expect-equals 999 gm["blink"]["crc"]
  expect-equals 30 gm["blink"]["triggers"]["interval"]
  expect-equals 3 gm["blink"]["runlevel"]
  expect-structural-equals {:} Inventory.empty.to-goal-map

  // prune-missing drops apps whose image id is gone, keeps the present ones.
  id-a := uuid.Uuid #[0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,1]
  id-b := uuid.Uuid #[0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,2]
  app-a := InstalledApp --name="a" --id=id-a --size=1 --crc=1 --triggers=(Triggers) --runlevel=3
  app-b := InstalledApp --name="b" --id=id-b --size=1 --crc=1 --triggers=(Triggers) --runlevel=3
  inv-p := Inventory {"a": app-a, "b": app-b}
  // Only id-a is still installed → "b" is dropped.
  dropped := inv-p.prune-missing [id-a]
  expect-equals 1 dropped.size
  expect-equals "b" dropped[0]
  expect (inv-p.apps.contains "a")
  expect (not inv-p.apps.contains "b")
  // Nothing installed → both dropped, inventory empty.
  inv2 := Inventory {"a": app-a}
  expect-equals 1 (inv2.prune-missing []).size
  expect inv2.apps.is-empty
  // All present → nothing dropped.
  dropped3 := (Inventory {"a": app-a}).prune-missing [id-a, id-b]
  expect-equals 0 dropped3.size
