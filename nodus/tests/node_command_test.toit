import encoding.json
import expect show *
import nodus.node_command show NodeCommand apply-to-goal

main:
  // Decode a run command from its wire JSON.
  run := NodeCommand.decode "{\"verb\":\"run\",\"name\":\"blink\",\"crc\":999,\"size\":2048,\"triggers\":{\"interval\":30},\"runlevel\":3,\"arguments\":[]}".to-byte-array
  expect-equals "run" run.verb
  expect-equals "blink" run.name

  // run inserts/replaces the app in the goal map with the fields GoalState needs,
  // defaulting lifecycle to run-once when the command omits it.
  goal := {:}
  apply-to-goal goal run
  expect-structural-equals
      {"blink": {"size": 2048, "crc": 999, "triggers": {"interval": 30}, "runlevel": 3, "lifecycle": "run-once", "arguments": []}}
      goal

  // An explicit lifecycle is carried through.
  run-loop := NodeCommand.decode "{\"verb\":\"run\",\"name\":\"svc\",\"crc\":1,\"size\":2,\"triggers\":{\"boot\":1},\"runlevel\":3,\"lifecycle\":\"run-loop\",\"arguments\":[]}".to-byte-array
  apply-to-goal goal run-loop
  expect-equals "run-loop" goal["svc"]["lifecycle"]

  // A later run for the same name wins (absolute/idempotent).
  run2 := NodeCommand.decode "{\"verb\":\"run\",\"name\":\"blink\",\"crc\":1000,\"size\":4096,\"triggers\":{\"boot\":1},\"runlevel\":2,\"arguments\":[]}".to-byte-array
  apply-to-goal goal run2
  expect-equals 1000 goal["blink"]["crc"]
  expect-equals 4096 goal["blink"]["size"]

  // stop removes the app.
  stop := NodeCommand.decode "{\"verb\":\"stop\",\"name\":\"blink\"}".to-byte-array
  apply-to-goal goal stop
  stop-svc := NodeCommand.decode "{\"verb\":\"stop\",\"name\":\"svc\"}".to-byte-array
  apply-to-goal goal stop-svc
  expect-structural-equals {:} goal

  // set-poll-interval does not touch the goal; it exposes its interval.
  spi := NodeCommand.decode "{\"verb\":\"set-poll-interval\",\"interval\":5}".to-byte-array
  expect spi.is-set-poll
  expect-equals 5 spi.interval-s
  apply-to-goal goal spi
  expect-structural-equals {:} goal

  // set decodes with typed value; it is NOT applied to the goal-app map.
  set-cmd := NodeCommand.decode (json.encode {"verb": "set", "app": "thermostat", "key": "target-c", "value": 21.5})
  expect set-cmd.is-set
  expect-equals "thermostat" set-cmd.app
  expect-equals "target-c" set-cmd.config-key
  expect-equals 21.5 set-cmd.config-value
  expect (set-cmd.config-value is float)
  goal = {:}
  apply-to-goal goal set-cmd      // set is a no-op on the goal plane
  expect goal.is-empty
