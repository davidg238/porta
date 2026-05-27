// device/node_command.toit — on-device decode of a gateway command and its
// application to the node's goal-app map. The wire form mirrors
// gateway/command.toit (decode only; the device never encodes commands).
import encoding.json

VERB-RUN ::= "run"
VERB-STOP ::= "stop"
VERB-SET-POLL-INTERVAL ::= "set-poll-interval"
VERB-SET-CONSOLE ::= "set-console"
VERB-SET ::= "set"

/** One command pulled from the gateway, as a verb plus its argument map. */
class NodeCommand:
  verb/string
  args/Map
  constructor .verb .args:

  /** Decodes a command from its JSON wire form $bytes. */
  static decode bytes/ByteArray -> NodeCommand:
    obj := json.decode bytes
    a := {:}
    obj.do: | key value | if key != "verb": a[key] = value
    return NodeCommand obj["verb"] a

  name -> string?: return args.get "name"
  crc -> int?: return args.get "crc"
  size -> int?: return args.get "size"
  interval-s -> int?: return args.get "interval"
  is-set-poll -> bool: return verb == VERB-SET-POLL-INTERVAL
  is-set-console -> bool: return verb == VERB-SET-CONSOLE
  is-set -> bool: return verb == VERB-SET
  app -> string?: return args.get "app"
  config-key -> string?: return args.get "key"
  config-value -> any: return args.get "value"

/**
Applies $command to the goal-app map $goal (name → {"size","crc","triggers",
  "runlevel","lifecycle","arguments"}, the shape GoalState.parse consumes). A run sets/replaces
  its app; a stop removes it; set-poll-interval does not affect the app set.
*/
apply-to-goal goal/Map command/NodeCommand -> none:
  if command.verb == VERB-RUN:
    goal[command.args["name"]] = {
      "size": command.args["size"],
      "crc": command.args["crc"],
      "triggers": command.args.get "triggers" --if-absent=: {:},
      "runlevel": command.args.get "runlevel" --if-absent=: 3,
      "lifecycle": command.args.get "lifecycle" --if-absent=: "run-once",
      "arguments": command.args.get "arguments" --if-absent=: [],
    }
  else if command.verb == VERB-STOP:
    goal.remove command.args["name"]
