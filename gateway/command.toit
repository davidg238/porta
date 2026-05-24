// gateway/command.toit — the operator command model, its wire codec, and the
// goal projection used to reason about idempotency.
import encoding.json

VERB-RUN ::= "run"
VERB-STOP ::= "stop"
VERB-SET-POLL-INTERVAL ::= "set-poll-interval"
VERB-SET-CONSOLE ::= "set-console"

/**
An operator command targeted at a node.

Commands are declarative and absolute, so applying one is idempotent and safe to
  redeliver. A command is stored as a $verb plus an $args map (the verb-specific
  payload) and travels on the wire as a single JSON object ($encode). Any real
  command encodes to at least one byte, which is what lets a zero-byte TFTP body
  mean "command queue drained".
*/
class Command:
  verb/string
  args/Map

  constructor .verb .args:

  /**
  Builds a run command: the node should run app $name from image $crc (byte count
    $size) under the given $triggers ({type:value} map, see device/triggers.toit),
    at $runlevel, with container $arguments.

  $size is required so the device can size its image writer from the command alone,
    without reading the payload first.
  */
  static run --name/string --crc/int --size/int --triggers/Map --runlevel/int=3 --arguments/List=[] -> Command:
    return Command VERB-RUN {
      "name": name,
      "crc": crc,
      "size": size,
      "triggers": triggers,
      "runlevel": runlevel,
      "arguments": arguments,
    }

  /** Builds a stop command: the node should not run app $name. */
  static stop --name/string -> Command:
    return Command VERB-STOP {"name": name}

  /** Builds a command setting the node's wake/poll cadence to $interval-s seconds. */
  static set-poll-interval --interval-s/int -> Command:
    return Command VERB-SET-POLL-INTERVAL {"interval": interval-s}

  /** Builds a command turning the node's console/telemetry forwarding $on. */
  static set-console --on/bool -> Command:
    return Command VERB-SET-CONSOLE {"on": on}

  /** Serializes this command to its JSON wire form. */
  encode -> ByteArray:
    m := {"verb": verb}
    args.do: | key value | m[key] = value
    return json.encode m

  /** Reconstructs a $Command from its JSON wire form $bytes. */
  static decode bytes/ByteArray -> Command:
    obj := json.decode bytes
    a := {:}
    obj.do: | key value | if key != "verb": a[key] = value
    return Command obj["verb"] a

  name -> string?: return args.get "name"
  crc -> int?: return args.get "crc"
  size -> int?: return args.get "size"
  triggers -> Map?: return args.get "triggers"
  runlevel -> int?: return args.get "runlevel"
  arguments -> List?: return args.get "arguments"
  interval-s -> int?: return args.get "interval"

/**
Builds the {type:value} trigger map (device/triggers.toit form) from repeatable
  --trigger $flags (each "boot", "interval=<s>", "install=<n>",
  "gpio-high=<pin>", "gpio-low=<pin>", or "gpio-touch=<pin>") plus the optional
  --interval shorthand $interval-s (seconds, or null).

Throws on an unknown trigger type or a non-integer value.
*/
triggers-from-flags flags/List --interval-s/int? -> Map:
  m := {:}
  if interval-s != null: m["interval"] = interval-s
  flags.do: | spec/string |
    eq := spec.index-of "="
    if eq < 0:
      if spec == "boot": m["boot"] = 1
      else: throw "unknown trigger: $spec"
    else:
      type := spec[..eq]
      value := int.parse spec[eq + 1..] --if-error=: throw "invalid trigger value: $spec"
      if type == "interval": m["interval"] = value
      else if type == "install": m["install"] = value
      else if type == "gpio-high" or type == "gpio-low" or type == "gpio-touch":
        m["$type:$value"] = value
      else: throw "unknown trigger: $type"
  return m

/**
Folds an ordered list of $commands into the goal-app map a node would converge
  to: app name → {"crc", "triggers", "runlevel", "arguments"}.

A run sets (or replaces) its app; a stop removes it; set-poll-interval does not
  affect the app set. Because commands are absolute, re-applying a run is a no-op
  and a later run for the same name wins — this function makes that idempotency
  testable on host and is reused by the device-side apply in B2.
*/
project commands/List -> Map:
  goal := {:}
  commands.do: | c/Command |
    if c.verb == VERB-RUN:
      goal[c.name] = {
        "crc": c.crc,
        "triggers": c.triggers,
        "runlevel": c.runlevel,
        "arguments": c.arguments,
      }
    else if c.verb == VERB-STOP:
      goal.remove c.name
  return goal
