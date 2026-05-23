// host/goal.toit
import encoding.json

/**
Builds the goal-state JSON the minimal gateway serves at "goal". One app named
  $name, with a single interval trigger. Mirrors the Porta goal contract in
  docs/specs/2026-05-22-...; the node fetches the image by the app's name.
*/
build-goal --name/string --size/int --crc/int --interval-s/int -> ByteArray:
  return json.encode {
    "apps": {
      name: {
        "size": size,
        "crc": crc,
        "triggers": {"interval": interval-s},
        "runlevel": 3,
        "arguments": [],
      },
    },
  }
