// device/supervisor.toit
import esp32
import system.storage
import system.containers

import .goal_state show GoalState
import .inventory show Inventory InstalledApp
import .image_writer show ImageStreamWriter
import .flash_image show ContainerImageInstaller
import .transport show WifiTransport GatewayClient
import .schedule_store show ScheduleStore clock-us

/** Gateway LAN address. Adjust to the host running host/serve.toit. */
GATEWAY-HOST ::= "192.168.0.175"
GATEWAY-PORT ::= 6969

/** How often to poll the gateway for goal changes. */
POLL-PERIOD ::= Duration --s=30
/** How long to stay awake observing started payloads before sleeping. */
OBSERVE ::= Duration --s=5
/** Deep-sleep duration between wakes. */
SLEEP ::= Duration --s=30

/** NVS bucket + key holding the persistent inventory. */
BUCKET-NAME ::= "porta"
INVENTORY-KEY ::= "inventory"

/**
One supervisor wake: dispatch by wake cause, poll/reconcile if due, start the
  installed payloads, observe, then deep-sleep. Deep-sleep wakes via full reboot,
  so the loop is the reboot; $main is linear.
*/
main:
  cause := esp32.wakeup-cause
  print "supervisor: awake (cause=$cause)"

  bucket := storage.Bucket.open --flash BUCKET-NAME
  inventory := load-inventory bucket
  store := ScheduleStore
  now := clock-us

  // Poll on cold boot (empty inventory) or when the poll period has elapsed.
  cold := inventory.apps.is-empty
  poll-due := cold or (now - store.last-poll-us) >= POLL-PERIOD.in-us
  if poll-due:
    // Never strand the device awake on a transient failure: trace and still sleep.
    catch --trace:
      inventory = poll-and-reconcile bucket inventory
      store.last-poll-us = now

  start-installed inventory
  arm-wakeups inventory

  print "supervisor: observing for $OBSERVE"
  sleep OBSERVE
  print "supervisor: deep-sleeping for $SLEEP"
  esp32.deep-sleep SLEEP

/** Loads the inventory from NVS, or an empty one if none/garbage. */
load-inventory bucket/storage.Bucket -> Inventory:
  tree := bucket.get INVENTORY-KEY --if-absent=: null
  if tree == null: return Inventory.empty
  return Inventory.decode tree

save-inventory bucket/storage.Bucket inventory/Inventory -> none:
  bucket[INVENTORY-KEY] = inventory.encode

/** Fetches the goal, installs new/changed images, returns the updated inventory. */
poll-and-reconcile bucket/storage.Bucket inventory/Inventory -> Inventory:
  print "supervisor: polling $GATEWAY-HOST:$GATEWAY-PORT"
  client/GatewayClient := (WifiTransport --host=GATEWAY-HOST --port=GATEWAY-PORT).connect
  try:
    goal := GoalState.parse (client.fetch-bytes "goal")
    recon := inventory.reconcile goal
    recon.to-schedule.do: | a/InstalledApp | print "supervisor: $a.name unchanged (crc=$a.crc)"
    recon.to-fetch.do: | app |
      print "supervisor: fetching $app.name ($app.size B, crc=$app.crc)"
      installer := ContainerImageInstaller
      writer := ImageStreamWriter installer --size=app.size --crc=app.crc
      client.fetch app.name --to-writer=writer
      id := writer.commit
      inventory.apps[app.name] = InstalledApp --name=app.name --id=id --size=app.size --crc=app.crc --triggers=app.triggers --runlevel=app.runlevel
      print "supervisor: installed $app.name -> $id"
    save-inventory bucket inventory
    return inventory
  finally:
    client.close

/** Starts every installed app (deep-sleep cleared running state each wake). */
start-installed inventory/Inventory -> none:
  inventory.apps.do: | name/string a/InstalledApp |
    containers.start a.id
    print "supervisor: started $name ($a.id)"

/** Re-arms GPIO (ext1) wake sources declared by installed apps' triggers. */
arm-wakeups inventory/Inventory -> none:
  mask := 0
  inventory.apps.do: | _ a/InstalledApp | mask |= a.triggers.ext1-high-mask
  if mask != 0:
    esp32.enable-external-wakeup mask true
    print "supervisor: armed ext1 wake mask=0x$(%x mask)"
