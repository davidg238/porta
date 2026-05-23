import expect show *
import .command show *

main:
  // run round-trips through the wire encoding.
  c := Command.run --name="blink" --crc=7 --size=4096 --triggers={"interval": 30}
  bytes := c.encode
  expect (bytes.size >= 1)            // the "real command ≥ 1 byte" invariant
  d := Command.decode bytes
  expect-equals VERB-RUN d.verb
  expect-equals "blink" d.name
  expect-equals 7 d.crc
  expect-equals 30 d.triggers["interval"]
  expect-equals 3 d.runlevel          // default
  expect-equals [] d.arguments        // default

  // stop.
  s := Command.decode (Command.stop --name="blink").encode
  expect-equals VERB-STOP s.verb
  expect-equals "blink" s.name

  // set-poll-interval.
  p := Command.decode (Command.set-poll-interval --interval-s=5).encode
  expect-equals VERB-SET-POLL-INTERVAL p.verb
  expect-equals 5 p.interval-s

  // triggers-from-flags builds the device {type:value} trigger map.
  t := triggers-from-flags ["boot", "gpio-high=33"] --interval-s=30
  expect-equals 1 t["boot"]
  expect-equals 33 t["gpio-high:33"]
  expect-equals 30 t["interval"]
  expect-throw "unknown trigger: bogus": triggers-from-flags ["bogus"] --interval-s=null

  // project folds a command list to the goal-app map; it is idempotent.
  run-x := Command.run --name="x" --crc=1 --size=512 --triggers={"interval": 10}
  expect-structural-equals (project [run-x]) (project [run-x, run-x])      // re-run is a no-op
  expect (project [run-x, (Command.stop --name="x")]).is-empty  // stop removes
  later := project [run-x, (Command.run --name="x" --crc=2 --size=1024 --triggers={:})]
  expect-equals 2 later["x"]["crc"]                             // later run wins

  // run carries size so the device can size its image writer from the command alone.
  rc := Command.run --name="blink" --crc=999 --size=2048 --triggers={"interval": 30}
  expect-equals 2048 rc.size
  decoded := Command.decode rc.encode
  expect-equals 2048 decoded.size
  expect-equals 999 decoded.crc
