// gateway/gateway.toit — the Porta gateway CLI (pkg-cli). Every leaf command
// opens the sqlite Store and reads/writes it. The TFTP daemon (`serve`) and the
// store-backed request handler are B2 and are intentionally not here yet.
import cli
import host.file
import .store show Store DEFAULT-MAX-OFFLINE-S
import .command show *
import .crc32 show crc32
import .duration show parse-duration-s

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

  return cli.Command "gateway"
      --help="Porta LAN gateway — command-queue control plane for Toit nodes."
      --options=[ cli.Option "db" --help="Path to the sqlite store." --default="porta.db" ]
      --subcommands=[ scan-cmd, ping-cmd ]

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
