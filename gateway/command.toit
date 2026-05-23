// gateway/command.toit — the operator command model, its wire codec, and the
// goal projection used to reason about idempotency.
import encoding.json

VERB-RUN ::= "run"
VERB-STOP ::= "stop"
VERB-SET-POLL-INTERVAL ::= "set-poll-interval"

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
  Builds a run command: the node should run app $name from image $crc under the
    given $triggers ({type:value} map, see device/triggers.toit), at $runlevel,
    with container $arguments.
  */
  static run --name/string --crc/int --triggers/Map --runlevel/int=3 --arguments/List=[] -> Command:
    return Command VERB-RUN {
      "name": name,
      "crc": crc,
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
  triggers -> Map?: return args.get "triggers"
  runlevel -> int?: return args.get "runlevel"
  arguments -> List?: return args.get "arguments"
  interval-s -> int?: return args.get "interval"
