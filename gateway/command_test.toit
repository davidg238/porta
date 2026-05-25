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

  // set round-trips through the wire encoding (typed value preserved).
  st := Command.decode (Command.set --app="thermostat" --key="target-c" --value=21.5).encode
  expect-equals VERB-SET st.verb
  expect-equals "thermostat" st.app
  expect-equals "target-c" st.config-key
  expect-equals 21.5 st.config-value
  expect (st.config-value is float)
  // int / bool / string values survive too.
  expect (Command.decode (Command.set --app="a" --key="n" --value=7).encode).config-value is int
  expect-equals true (Command.decode (Command.set --app="a" --key="on" --value=true).encode).config-value
  expect-equals "heat" (Command.decode (Command.set --app="a" --key="mode" --value="heat").encode).config-value

  // project-config folds set commands to {app:{key:value}}, last-write-wins, by app.
  cfg := project-config [
    Command.set --app="t" --key="target" --value=20,
    Command.set --app="t" --key="mode" --value="heat",
    Command.set --app="t" --key="target" --value=22,   // overwrites
    Command.set --app="s" --key="threshold" --value=100,
  ]
  expect-equals 22 cfg["t"]["target"]
  expect-equals "heat" cfg["t"]["mode"]
  expect-equals 100 cfg["s"]["threshold"]
  // a set does not appear in the goal-app projection (separate plane).
  expect (project [Command.set --app="t" --key="x" --value=1]).is-empty

  // infer-scalar types a CLI string.
  expect-equals true (infer-scalar "true")
  expect-equals false (infer-scalar "false")
  expect (infer-scalar "42") is int
  expect-equals 42 (infer-scalar "42")
  expect (infer-scalar "1.5") is float
  expect-equals 1.5 (infer-scalar "1.5")
  expect-equals "auto" (infer-scalar "auto")
