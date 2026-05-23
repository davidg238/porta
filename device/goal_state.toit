// device/goal_state.toit
import encoding.json
import .triggers show Triggers

/** One application container in a goal-state. */
class App:
  name/string
  size/int       // Image bytes; sizes the ContainerImageWriter.
  crc/int        // CRC32-IEEE of the image; change-detection + verify.
  triggers/Triggers
  runlevel/int
  arguments/List

  constructor --.name --.size --.crc --.triggers --.runlevel=3 --.arguments=[]:

/**
A desired-state goal: the apps a node should run. Mirrors Artemis
  device-config["apps"] (artemis/src/cli/broker.toit:1006-1030) plus the
  size/crc Porta needs for a streaming install.
*/
class GoalState:
  apps/Map  // name/string -> App

  constructor .apps:

  static parse bytes/ByteArray -> GoalState:
    obj := json.decode bytes
    apps := {:}
    (obj.get "apps" --if-absent=: {:}).do: | name/string spec/Map |
      a := App
          --name=name
          --size=spec["size"]
          --crc=spec["crc"]
          --triggers=(Triggers.parse (spec.get "triggers" --if-absent=: {:}))
          --runlevel=(spec.get "runlevel" --if-absent=: 3)
          --arguments=(spec.get "arguments" --if-absent=: [])
      apps[name] = a
    return GoalState apps

  to-json -> ByteArray:
    apps-map := {:}
    apps.do: | name/string app/App |
      apps-map[name] = {
        "size": app.size,
        "crc": app.crc,
        "triggers": app.triggers.to-map,
        "runlevel": app.runlevel,
        "arguments": app.arguments,
      }
    return json.encode {"apps": apps-map}
