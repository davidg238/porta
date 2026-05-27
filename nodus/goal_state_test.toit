// device/goal_state_test.toit
import expect show *
import encoding.json as json-lib
import .goal_state show GoalState App

main:
  json := """{"apps":{"payload":{"size":38016,"crc":2157114022,
      "triggers":{"interval":60},"runlevel":3,"arguments":[]}}}"""
  g := GoalState.parse json.to-byte-array
  expect-equals 1 g.apps.size
  app/App := g.apps["payload"]
  expect-equals "payload" app.name
  expect-equals 38016 app.size
  expect-equals 2157114022 app.crc
  expect-equals 60 app.triggers.interval-s
  expect-equals 3 app.runlevel

  // round-trip: parse(to-json) preserves fields
  g2 := GoalState.parse g.to-json
  expect-equals 38016 (g2.apps["payload"] as App).size
  expect-equals 2157114022 (g2.apps["payload"] as App).crc
  expect-equals 60 (g2.apps["payload"] as App).triggers.interval-s

  // missing optional fields default
  g3 := GoalState.parse """{"apps":{"x":{"size":1,"crc":2}}}""".to-byte-array
  expect-equals 3 (g3.apps["x"] as App).runlevel
  expect-equals [] (g3.apps["x"] as App).arguments

  // lifecycle defaults to run-once and round-trips through parse/to-json.
  g-default := GoalState.parse (json-lib.encode {"apps": {"a": {"size": 1, "crc": 2, "triggers": {"boot": 1}, "runlevel": 3}}})
  expect-equals "run-once" g-default.apps["a"].lifecycle
  g-loop := GoalState.parse (json-lib.encode {"apps": {"b": {"size": 1, "crc": 2, "triggers": {"boot": 1}, "runlevel": 3, "lifecycle": "run-loop"}}})
  expect-equals "run-loop" g-loop.apps["b"].lifecycle
  reparsed := GoalState.parse g-loop.to-json
  expect-equals "run-loop" reparsed.apps["b"].lifecycle
