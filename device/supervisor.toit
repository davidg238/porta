// device/supervisor.toit
import esp32
import encoding.json
import system.storage
import system.containers

import .goal_state show GoalState
import .inventory show Inventory InstalledApp
import .node_command show NodeCommand apply-to-goal
import .node_id show mac-to-id
import .report show build-report
import .image_writer show ImageStreamWriter
import .flash_image show ContainerImageInstaller
import .transport show WifiTransport GatewayClient
import .schedule_store show ScheduleStore clock-us
import .telemetry_buffer show TelemetryBuffer
import .telemetry_service show TelemetryServiceClient TelemetryServiceProvider
import .telemetry_codec show build-data-body
import .config_store show load-config save-config set-config
import .control_service show ControlServiceProvider

/** Gateway LAN address. Adjust to the host running `gateway serve`. */
GATEWAY-HOST ::= "192.168.0.175"
GATEWAY-PORT ::= 6969

/** Fallback poll cadence (seconds) before the node has been told otherwise. */
DEFAULT-POLL-S ::= 30
/** How long to stay awake observing started payloads before sleeping. */
OBSERVE ::= Duration --s=5

/** NVS bucket + keys for persistent node-local state. */
BUCKET-NAME ::= "porta"
INVENTORY-KEY ::= "inventory"
POLL-INTERVAL-KEY ::= "poll_interval_s"
CONSOLE-KEY ::= "console_forward"

/**
One supervisor wake: identify, poll the gateway if due (drain commands → apply →
  reconcile → fetch/remove → report), start the installed payloads, then deep-sleep
  for the node-local poll interval. Deep-sleep wakes via full reboot, so $main is
  linear and the reboot is the loop.
*/
main:
  print "supervisor: awake (cause=$esp32.wakeup-cause)"
  id := mac-to-id esp32.mac-address
  bucket := storage.Bucket.open --flash BUCKET-NAME
  inventory := load-inventory bucket
  poll-interval-s := bucket.get POLL-INTERVAL-KEY --if-absent=: DEFAULT-POLL-S
  store := ScheduleStore
  now := clock-us

  // Bring up the telemetry provider before any payload app can emit.
  spawn-remoting_
  sleep --ms=200  // let the provider register before payloads open clients

  // Poll on cold boot (empty inventory) or once the poll interval has elapsed.
  cold := inventory.apps.is-empty
  poll-due := cold or (now - store.last-poll-us) >= (poll-interval-s * 1_000_000)
  if poll-due:
    // Never strand the node awake on a transient failure: trace and still sleep.
    catch --trace:
      poll-interval-s = poll-and-reconcile bucket inventory id poll-interval-s store
      store.last-poll-us = now

  start-installed inventory
  arm-wakeups inventory

  print "supervisor: observing for $OBSERVE"
  sleep OBSERVE

  // Ship telemetry produced this wake (after payloads ran), if forwarding is on.
  // This opens a second TFTP connection every wake the flag is on (not only poll wakes).
  if (bucket.get CONSOLE-KEY --if-absent=: false): flush-telemetry_ id

  print "supervisor: deep-sleeping for $(poll-interval-s)s"
  esp32.deep-sleep (Duration --s=poll-interval-s)

/** Loads the inventory from NVS, or an empty one if none/garbage. */
load-inventory bucket/storage.Bucket -> Inventory:
  tree := bucket.get INVENTORY-KEY --if-absent=: null
  if tree == null: return Inventory.empty
  inventory := Inventory.decode tree
  installed-ids := containers.images.map: it.id
  dropped := inventory.prune-missing installed-ids
  if not dropped.is-empty:
    dropped.do: | name/string | print "supervisor: dropping stale app $name (image gone)"
    save-inventory bucket inventory
  return inventory

save-inventory bucket/storage.Bucket inventory/Inventory -> none:
  bucket[INVENTORY-KEY] = inventory.encode

/**
Connects, drains the command queue applying each command to a goal seeded from the
  current inventory, reconciles (fetch new/changed images, remove dropped apps),
  reports observed state, and returns the (possibly updated) poll interval.
*/
poll-and-reconcile bucket/storage.Bucket inventory/Inventory id/string poll-interval-s/int store/ScheduleStore -> int:
  print "supervisor: polling $GATEWAY-HOST:$GATEWAY-PORT as id=$id"
  client/GatewayClient := (WifiTransport --host=GATEWAY-HOST --port=GATEWAY-PORT).connect
  try:
    goal-map := inventory.to-goal-map
    // Drain: each "commands" RRQ returns the oldest undelivered command, or a
    // zero-byte body when the queue is exhausted.
    while true:
      bytes := client.fetch-bytes "commands?id=$id"
      if bytes.is-empty: break
      command := NodeCommand.decode bytes
      if command.is-set-poll:
        poll-interval-s = command.interval-s
        bucket[POLL-INTERVAL-KEY] = poll-interval-s
        print "supervisor: poll interval now $(poll-interval-s)s"
      else if command.is-set-console:
        on := command.args.get "on" --if-absent=: false
        bucket[CONSOLE-KEY] = on
        print "supervisor: console-forward now $on"
      else if command.is-set:
        config := load-config bucket
        set-config config command.app command.config-key command.config-value
        save-config bucket config
        print "supervisor: set $(command.app).$(command.config-key) = $(command.config-value)"
      else:
        apply-to-goal goal-map command
        print "supervisor: applied $command.verb $(command.name)"

    goal := GoalState.parse (json.encode {"apps": goal-map})
    recon := inventory.reconcile goal
    recon.to-fetch.do: | app |
      print "supervisor: fetching $app.name ($app.size B, crc=$app.crc)"
      installer := ContainerImageInstaller
      writer := ImageStreamWriter installer --size=app.size --crc=app.crc
      client.fetch "payload?id=$id&name=$app.name&crc=$app.crc" --to-writer=writer
      image-id := writer.commit
      inventory.apps[app.name] = InstalledApp --name=app.name --id=image-id --size=app.size --crc=app.crc --triggers=app.triggers --runlevel=app.runlevel
      print "supervisor: installed $app.name -> $image-id"
    recon.to-remove.do: | a/InstalledApp |
      print "supervisor: removing $a.name"
      catch --trace: containers.uninstall a.id
      inventory.apps.remove a.name
    save-inventory bucket inventory

    // Report observed state before sleeping (audit + convergence).
    body := build-report inventory --config=(load-config bucket) --uptime-us=clock-us --wakes=store.wakes
    client.put "report?id=$id" body
    print "supervisor: reported $(inventory.apps.size) app(s)"
    return poll-interval-s
  finally:
    client.close

/** Starts every installed app (deep-sleep cleared running state each wake). */
start-installed inventory/Inventory -> none:
  inventory.apps.do: | name/string a/InstalledApp |
    e := catch --trace: containers.start a.id
    if e: print "supervisor: could not start $name ($a.id): $e"
    else: print "supervisor: started $name ($a.id)"

/** Re-arms GPIO (ext1) wake sources declared by installed apps' triggers. */
arm-wakeups inventory/Inventory -> none:
  mask := 0
  inventory.apps.do: | _ a/InstalledApp | mask |= a.triggers.ext1-high-mask
  if mask != 0:
    esp32.enable-external-wakeup mask true
    print "supervisor: armed ext1 wake mask=0x$(%x mask)"

/** Spawns the telemetry provider in its own process (services only, no socket). */
spawn-remoting_ -> none:
  spawn::
    catch --trace:
      provider := TelemetryServiceProvider (TelemetryBuffer --cap=128)
      provider.install
      // ControlService reads the NVS config blob live (its own bucket handle in
      // this spawned process), so a `set` drained later this wake is visible.
      config-bucket := storage.Bucket.open --flash BUCKET-NAME
      control := ControlServiceProvider:: load-config config-bucket
      control.install
      print "supervisor: telemetry + control providers registered"
      while true: sleep (Duration --s=3600)  // outlive the wake window; deep-sleep ends it

/**
Drains the telemetry buffer (a client call to the spawned provider) and, if any
  entries accrued this wake, ships them as a JSONL "data?id=" WRQ. Best-effort:
  any failure is traced and the node still deep-sleeps.
*/
flush-telemetry_ id/string -> none:
  catch --trace:
    tclient := TelemetryServiceClient
    tclient.open
    entries := []
    try:
      entries = tclient.drain
    finally:
      tclient.close
    if entries.is-empty: return
    body := build-data-body entries
    gw := (WifiTransport --host=GATEWAY-HOST --port=GATEWAY-PORT).connect
    try:
      gw.put "data?id=$id" body
      print "supervisor: shipped $(entries.size) telemetry entr(ies)"
    finally:
      gw.close
