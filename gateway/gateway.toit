// gateway/gateway.toit — the Porta gateway CLI (pkg-cli). Every leaf command
// opens the sqlite Store and reads/writes it. The TFTP daemon (`serve`) and the
// store-backed request handler are B2 and are intentionally not here yet.
import cli
import host.file
import .store show Store DEFAULT-MAX-OFFLINE-S decode-json_
import .command show *
import .crc32 show crc32
import .duration show parse-duration-s
import .serve show cmd-serve DEFAULT-PORT

main args:
  (build-command).run args

/** Builds the full `gateway` command tree. */
build-command -> cli.Command:
  scan-cmd := cli.Command "scan"
      --help="List known nodes and their online/offline health."
      --options=[ cli.Flag "include-never-seen" --help="Also list nodes that have never polled." ]
      --run=:: cmd-scan it

  ping-cmd := cli.Command "ping"
      --help="Report whether a node has been seen within its max-offline window."
      --options=[ cli.Option "device" --short-name="d" --help="Node name or MAC." --required ]
      --run=:: cmd-ping it

  device-show-cmd := cli.Command "show"
      --help="Show a node's last contact, observed state, and queued commands."
      --options=[ cli.Option "device" --short-name="d" --help="Node name or MAC." --required ]
      --run=:: cmd-device-show it

  device-set-max-offline-cmd := cli.Command "set-max-offline"
      --help="Set the offline threshold used to judge a node's health."
      --options=[ cli.Option "device" --short-name="d" --help="Node name or MAC." --required ]
      --rest=[ cli.Option "duration" --help="e.g. 30s, 5m, 1h." --required ]
      --run=:: cmd-device-set-max-offline it

  device-set-poll-interval-cmd := cli.Command "set-poll-interval"
      --help="Enqueue a change to the node's wake/poll cadence."
      --options=[ cli.Option "device" --short-name="d" --help="Node name or MAC." --required ]
      --rest=[ cli.Option "duration" --help="e.g. 1s, 30s, 5m." --required ]
      --run=:: cmd-device-set-poll-interval it

  device-name-cmd := cli.Command "name"
      --help="Override a node's auto-assigned friendly name."
      --options=[ cli.Option "device" --short-name="d" --help="Node name or MAC." --required ]
      --rest=[ cli.Option "new-name" --help="The new name." --required ]
      --run=:: cmd-device-name it

  device-cmd := cli.Command "device"
      --help="Inspect and configure a node."
      --subcommands=[
        device-show-cmd, device-set-max-offline-cmd,
        device-set-poll-interval-cmd, device-name-cmd,
      ]

  container-install-cmd := cli.Command "install"
      --help="""
        Register a container image for a node and enqueue a run command.

        <file> dispatches by extension: .bin is a prebuilt image (M1); .pod and
        .toit are accepted but not yet supported (scheduled for M3 / M4).
        """
      --options=[
        cli.Option "device" --short-name="d" --help="Node name or MAC." --required,
        cli.OptionInt "crc" --help="Image CRC32 (computed from the file if omitted).",
        cli.Option "interval" --help="Run-interval shorthand, e.g. 30s (a --trigger interval=…).",
        cli.Option "trigger" --multi --help="Repeatable trigger: boot | interval=<s> | gpio-high=<pin> | gpio-low=<pin> | gpio-touch=<pin>.",
        cli.OptionInt "runlevel" --help="Container runlevel." --default=3,
      ]
      --rest=[
        cli.Option "name" --help="App name on the node." --required,
        cli.Option "file" --help="Image file (.bin | .pod | .toit)." --required,
      ]
      --examples=[
        cli.Example "Install blink to run every 30 s on a node addressed by MAC:"
            --arguments="--device=aabbccddeeff --interval=30s blink ./blink.bin"
            --global-priority=5,
      ]
      --run=:: cmd-container-install it

  container-uninstall-cmd := cli.Command "uninstall"
      --help="Enqueue a stop command so the node no longer runs an app."
      --options=[ cli.Option "device" --short-name="d" --help="Node name or MAC." --required ]
      --rest=[ cli.Option "name" --help="App name to stop." --required ]
      --run=:: cmd-container-uninstall it

  container-list-cmd := cli.Command "list"
      --help="List a node's apps from its latest report."
      --options=[ cli.Option "device" --short-name="d" --help="Node name or MAC." --required ]
      --run=:: cmd-container-list it

  container-cmd := cli.Command "container"
      --help="Install, remove, and list a node's containers."
      --subcommands=[ container-install-cmd, container-uninstall-cmd, container-list-cmd ]

  log-cmd := cli.Command "log"
      --help="Show a node's command audit history (issued and delivered)."
      --options=[ cli.Option "device" --short-name="d" --help="Node name or MAC." --required ]
      --run=:: cmd-log it

  serve-cmd := cli.Command "serve"
      --help="Run the gateway daemon: serve the command queue and payloads over TFTP/UDP."
      --options=[ cli.OptionInt "port" --help="UDP port to listen on." --default=DEFAULT-PORT ]
      --run=:: cmd-serve it

  monitor-cmd := cli.Command "monitor"
      --help="Show a node's telemetry (data_log); --follow tails new rows as wakes deliver them."
      --options=[
        cli.Option "device" --short-name="d" --help="Node name or MAC." --required,
        cli.Option "since" --help="Look-back window, e.g. 1h, 30m (default 1h).",
        cli.Flag "follow" --short-name="f" --help="Keep polling and print new rows until interrupted.",
        cli.Option "kind" --help="Filter to 'log' or 'metric'.",
      ]
      --run=:: cmd-monitor it

  return cli.Command "gateway"
      --help="Porta LAN gateway — command-queue control plane for Toit nodes."
      --options=[ cli.Option "db" --help="Path to the sqlite store." --default="porta.db" ]
      --subcommands=[ serve-cmd, scan-cmd, ping-cmd, device-cmd, container-cmd, log-cmd, monitor-cmd ]

// --- shared helpers ----------------------------------------------------------

now_ -> int: return Time.now.s-since-epoch

open-store_ parsed/cli.Parsed -> Store: return Store.open parsed["db"]

/** Whether $s is a 12-hex-digit base MAC. */
is-mac_ s/string -> bool:
  if s.size != 12: return false
  s.do --runes: | c |
    if not ('0' <= c <= '9' or 'a' <= c <= 'f'): return false
  return true

/**
Resolves a node name-or-MAC $key to a node id, or aborts the process.

Matches an existing id, then an existing name; failing both, accepts a literal
  12-hex-digit MAC (so a node can be addressed before its first poll).
*/
resolve-node-id_ store/Store key/string -> string:
  if (store.node key) != null: return key
  by-name := store.node-by-name key
  if by-name != null: return by-name["id"]
  if is-mac_ key: return key
  print "Error: unknown node '$key' (not a known name and not a 12-hex-digit MAC)."
  exit 1
  unreachable

/** Whether $node (a node row map, possibly null) is within its max-offline window at $now. */
online_ node/Map? --now/int -> bool:
  if node == null or node["last_seen"] == null: return false
  return (now - node["last_seen"]) <= node["max_offline_s"]

// --- commands ----------------------------------------------------------------

cmd-scan parsed/cli.Parsed -> none:
  store := open-store_ parsed
  include-never := parsed["include-never-seen"]
  now := now_
  print "DEVICE        NAME             LAST-SEEN  STATUS"
  store.nodes.do: | node/Map |
    if node["last_seen"] == null and not include-never: continue.do
    status := node["last_seen"] == null ? "never-seen" : (online_ node --now=now ? "online" : "offline")
    last := node["last_seen"] == null ? "-" : "$(now - node["last_seen"])s ago"
    print "$(node["id"])  $(pad_ node["name"] 15)  $(pad_ last 9)  $status"
  store.close

cmd-ping parsed/cli.Parsed -> none:
  store := open-store_ parsed
  id := resolve-node-id_ store parsed["device"]
  node := store.node id
  now := now_
  if node == null or node["last_seen"] == null:
    print "$id: never seen"
  else if online_ node --now=now:
    print "$id: online (last seen $(now - node["last_seen"])s ago)"
  else:
    print "$id: offline (last seen $(now - node["last_seen"])s ago)"
  store.close

/** Right-pads $s with spaces to at least $width characters. */
pad_ s/string width/int -> string:
  if s.size >= width: return s
  return s + (" " * (width - s.size))

/** Formats a data_log row {ts,seq,kind,name,value,text} for `monitor`. */
monitor-line_ r/Map -> string:
  if r["kind"] == "metric": return "$(r["ts"])  metric  $(r["name"])=$(r["value"])"
  return "$(r["ts"])  log     $(r["text"])"

cmd-device-show parsed/cli.Parsed -> none:
  store := open-store_ parsed
  id := resolve-node-id_ store parsed["device"]
  node := store.node id
  if node == null:
    print "$id: no row yet (never seen, never configured)"
    store.close
    return
  now := now_
  print "id:            $(node["id"])"
  print "name:          $(node["name"])"
  print "last-seen:     $(node["last_seen"] == null ? "never" : "$(now - node["last_seen"])s ago")"
  print "poll-interval: $(node["poll_interval_s"])s"
  print "max-offline:   $(node["max_offline_s"])s"
  print "observed:      $(node["observed_state"] == null ? "(no report yet)" : node["observed_state"])"
  undelivered := store.undelivered-commands id
  print "queued (undelivered): $undelivered.size"
  undelivered.do: | c/Map | print "  #$(c["id"]) $(c["verb"]) $(c["args"])"
  store.close

cmd-device-set-max-offline parsed/cli.Parsed -> none:
  store := open-store_ parsed
  id := resolve-node-id_ store parsed["device"]
  store.ensure-node id --now=now_
  seconds := parse-duration-s parsed["duration"]
  store.set-max-offline id seconds
  print "$id: max-offline = $(seconds)s"
  store.close

cmd-device-set-poll-interval parsed/cli.Parsed -> none:
  store := open-store_ parsed
  id := resolve-node-id_ store parsed["device"]
  store.ensure-node id --now=now_
  seconds := parse-duration-s parsed["duration"]
  cmd-id := store.enqueue-command id (Command.set-poll-interval --interval-s=seconds) --issued-by="cli" --now=now_
  store.set-poll-interval-intended id seconds
  print "$id: enqueued set-poll-interval $(seconds)s (command #$cmd-id)"
  store.close

cmd-device-name parsed/cli.Parsed -> none:
  store := open-store_ parsed
  id := resolve-node-id_ store parsed["device"]
  store.ensure-node id --now=now_
  store.set-node-name id parsed["new-name"]
  print "$id: name = $(parsed["new-name"])"
  store.close

cmd-container-install parsed/cli.Parsed -> none:
  store := open-store_ parsed
  id := resolve-node-id_ store parsed["device"]
  store.ensure-node id --now=now_
  name := parsed["name"]
  path := parsed["file"]

  if path.ends-with ".pod":
    print "Error: .pod ingestion is not yet supported (scheduled for M3)."
    exit 1
  if path.ends-with ".toit":
    print "Error: .toit source-compile is not yet supported (scheduled for M4)."
    exit 1
  if not path.ends-with ".bin":
    print "Error: unsupported file type for '$path' (expected .bin, .pod, or .toit)."
    exit 1

  image := file.read-contents path
  crc := parsed["crc"] != null ? parsed["crc"] : (crc32 image)
  triggers := triggers-from-flags parsed["trigger"]
      --interval-s=(parsed["interval"] != null ? (parse-duration-s parsed["interval"]) : null)
  if triggers.is-empty:
    print "Note: no triggers given — '$name' will be installed but not started until a trigger is added."

  store.register-payload --crc=crc --name=name --image=image
  run-cmd := Command.run --name=name --crc=crc --size=image.size --triggers=triggers --runlevel=parsed["runlevel"]
  cmd-id := store.enqueue-command id run-cmd --issued-by="cli" --now=now_
  print "$id: registered $name@$crc ($(image.size) B); enqueued run (command #$cmd-id)"
  store.close

cmd-container-uninstall parsed/cli.Parsed -> none:
  store := open-store_ parsed
  id := resolve-node-id_ store parsed["device"]
  store.ensure-node id --now=now_
  name := parsed["name"]
  cmd-id := store.enqueue-command id (Command.stop --name=name) --issued-by="cli" --now=now_
  print "$id: enqueued stop $name (command #$cmd-id)"
  store.close

cmd-container-list parsed/cli.Parsed -> none:
  store := open-store_ parsed
  id := resolve-node-id_ store parsed["device"]
  node := store.node id
  if node == null or node["observed_state"] == null:
    print "$id: no report yet"
    store.close
    return
  observed := decode-json_ node["observed_state"]
  apps := observed.get "apps" --if-absent=: {:}
  if apps.is-empty:
    print "$id: no containers installed"
    store.close
    return
  print "DEVICE        IMAGE       NAME"
  apps.do: | name/string spec/Map |
    print "$(node["id"])  $(pad_ "$(spec.get "crc")" 10)  $name"
  store.close

cmd-log parsed/cli.Parsed -> none:
  store := open-store_ parsed
  id := resolve-node-id_ store parsed["device"]
  entries := store.command-log id
  if entries.is-empty:
    print "$id: no commands"
    store.close
    return
  print "ID   VERB              DELIVERED  ARGS"
  entries.do: | e/Map |
    delivered := e["delivered_at"] == null ? "pending" : "yes"
    print "$(pad_ "#$(e["id"])" 4) $(pad_ e["verb"] 17) $(pad_ delivered 10) $(e["args"])"
  store.close

cmd-monitor parsed/cli.Parsed -> none:
  store := open-store_ parsed
  id := resolve-node-id_ store parsed["device"]
  kind := parsed["kind"]
  since-s := parsed["since"] != null ? (parse-duration-s parsed["since"]) : 3600
  now := now_
  (store.query-data id --since=(now - since-s) --until=now --kind=kind).do: | r/Map |
    print (monitor-line_ r)
  if parsed["follow"]:
    last := now
    while true:
      sleep --ms=2000
      t := now_
      (store.query-data id --since=(last + 1) --until=t --kind=kind).do: | r/Map |
        print (monitor-line_ r)
      last = t
  store.close
