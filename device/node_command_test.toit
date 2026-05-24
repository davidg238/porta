import expect show *
import .node_command show NodeCommand apply-to-goal

main:
  // Decode a run command from its wire JSON.
  run := NodeCommand.decode "{\"verb\":\"run\",\"name\":\"blink\",\"crc\":999,\"size\":2048,\"triggers\":{\"interval\":30},\"runlevel\":3,\"arguments\":[]}".to-byte-array
  expect-equals "run" run.verb
  expect-equals "blink" run.name

  // run inserts/replaces the app in the goal map with the fields GoalState needs.
  goal := {:}
  apply-to-goal goal run
  expect-structural-equals
      {"blink": {"size": 2048, "crc": 999, "triggers": {"interval": 30}, "runlevel": 3, "arguments": []}}
      goal

  // A later run for the same name wins (absolute/idempotent).
  run2 := NodeCommand.decode "{\"verb\":\"run\",\"name\":\"blink\",\"crc\":1000,\"size\":4096,\"triggers\":{\"boot\":1},\"runlevel\":2,\"arguments\":[]}".to-byte-array
  apply-to-goal goal run2
  expect-equals 1000 goal["blink"]["crc"]
  expect-equals 4096 goal["blink"]["size"]

  // stop removes the app.
  stop := NodeCommand.decode "{\"verb\":\"stop\",\"name\":\"blink\"}".to-byte-array
  apply-to-goal goal stop
  expect-structural-equals {:} goal

  // set-poll-interval does not touch the goal; it exposes its interval.
  spi := NodeCommand.decode "{\"verb\":\"set-poll-interval\",\"interval\":5}".to-byte-array
  expect spi.is-set-poll
  expect-equals 5 spi.interval-s
  apply-to-goal goal spi
  expect-structural-equals {:} goal
