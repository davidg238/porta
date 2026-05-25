// gateway/command.toit — the operator command model, its wire codec, and the
// goal projection used to reason about idempotency.
import encoding.json

VERB-RUN ::= "run"
VERB-STOP ::= "stop"
VERB-SET-POLL-INTERVAL ::= "set-poll-interval"
VERB-SET-CONSOLE ::= "set-console"
VERB-SET ::= "set"

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

  /** Builds a command setting app $app's config $key to scalar $value (int/float/bool/string). */
  static set --app/string --key/string --value -> Command:
    return Command VERB-SET {"app": app, "key": key, "value": value}

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
  app -> string?: return args.get "app"
  config-key -> string?: return args.get "key"
  config-value -> any: return args.get "value"

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
Types a CLI value string: "true"/"false" → bool, an integer → int, a decimal →
  float, anything else → the string unchanged. Mirrors the scalar surface of
  TelemetryService.report so the down-path and up-path agree on value types.

"nan" and "inf" are not decimals and pass through as strings.
*/
infer-scalar value-str/string -> any:
  if value-str == "true": return true
  if value-str == "false": return false
  as-int := int.parse value-str --if-error=: null
  if as-int != null: return as-int
  as-float := float.parse value-str --if-error=: null
  if as-float != null and as-float.is-finite: return as-float
  return value-str

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

/**
Folds an ordered list of $commands into the desired config map: app name →
  {key: value}. Later sets for the same app/key win (declarative & absolute, like
  $project). Non-set verbs are ignored — config is a plane separate from the goal.
*/
project-config commands/List -> Map:
  config := {:}
  commands.do: | c/Command |
    if c.verb == VERB-SET:
      (config.get c.app --init=: {:})[c.config-key] = c.config-value
  return config

/**
Diffs desired config (projected from the $command-log) against $observed config and
  returns the $Command objects to re-issue to self-heal divergence. For each divergent
  (app, key) it returns that key's *latest `set` log entry replayed verbatim* — the
  original command rebuilt via `Command verb args` from the stored row, not from
  extracted scalars, so the re-issued args (and scalar types) are identical.

A key is re-issued only when its latest `set` is already delivered
  (`delivered_at` != null) AND the observed value diverges (absent, or
  present-but-unequal). An undelivered latest `set` legitimately lags (in-flight) and
  is skipped — and since a re-issued set is itself undelivered next report, re-issue is
  self-throttling. Observed keys with no desired `set` are left alone (desired never
  shrinks). This is the generic diff seam: goal/apps can later feed a different
  projection of the same `command-log` shape.

$command-log is `Store.command-log` output: maps carrying `verb`, decoded `args`,
  `issued_by`, `delivered_at`. $observed is the report echo: app -> {key: value}.
*/
reconcile-config command-log/List observed/Map -> List:
  // Latest set entry per (app, key), in log order — last write wins (like project-config).
  latest := {:}  // app -> { key -> log-entry Map }
  command-log.do: | e/Map |
    if e["verb"] == VERB-SET:
      args := e["args"]
      (latest.get args["app"] --init=: {:})[args["key"]] = e
  reissues := []
  latest.do: | app/string keys/Map |
    obs-app := observed.get app --if-absent=: {:}
    keys.do: | key/string entry/Map |
      if entry["delivered_at"] != null:
        desired-val := entry["args"]["value"]
        converged := (obs-app.contains key) and obs-app[key] == desired-val
        if not converged:
          reissues.add (Command VERB-SET entry["args"])
  return reissues

/**
Counts the `gateway-reconcile` `set` commands targeting ($app, $key) in the
  $command-log — the self-heal attempt count. Because re-issue is self-throttled
  (one per delivered-but-still-failed report), a count >= 2 for a still-divergent key
  means the node delivered and failed to apply twice: a real apply crash-loop, not
  reconcile noise. Used by `device get` to surface a warning.
*/
reconcile-count command-log/List app/string key/string -> int:
  count := 0
  command-log.do: | e/Map |
    if e["verb"] == VERB-SET and e["issued_by"] == "gateway-reconcile":
      args := e["args"]
      if args["app"] == app and args["key"] == key: count++
  return count

/**
Classifies config $key across the $desired and $observed maps for `device get`:
  "(drift)" when both are present and unequal, "(pending)" when desired is present
  but observed is absent (the node has not yet converged), else "" (equal, or the
  key is desired-absent). Values compare with `==` on the JSON-decoded scalars.
*/
config-marker desired/Map observed/Map key/string -> string:
  has-d := desired.contains key
  has-o := observed.contains key
  if has-d and has-o: return desired[key] == observed[key] ? "" : "(drift)"
  if has-d: return "(pending)"
  return ""

/**
Renders the desired-vs-observed config table for app $app as a list of printable
  lines (caller adds any node-id prefix). Covers the union of $desired and
  $observed keys (desired order first, then observed-only keys); an absent value
  cell renders "--", and each row carries the $config-marker. Both maps empty →
  a single "$app has no config" line.
*/
render-config-table app/string desired/Map observed/Map -> List:
  if desired.is-empty and observed.is-empty:
    return ["$app has no config"]
  keys := []
  desired.do --keys: keys.add it
  observed.do --keys: | k | if not desired.contains k: keys.add k
  lines := ["config for $app", "  $(pad-col_ "KEY" 12)$(pad-col_ "DESIRED" 12)OBSERVED"]
  keys.do: | k/string |
    d-cell := desired.contains k ? "$desired[k]" : "--"
    o-cell := observed.contains k ? "$observed[k]" : "--"
    marker := config-marker desired observed k
    row := "  $(pad-col_ k 12)$(pad-col_ d-cell 12)$(pad-col_ o-cell 10)$marker"
    lines.add (row.trim --right)
  return lines

/** Right-pads $s with spaces to at least $width columns (one trailing space min). */
pad-col_ s/string width/int -> string:
  pad := width - s.size
  return pad > 0 ? "$s$(" " * pad)" : "$s "
