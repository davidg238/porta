import expect show *
import .command show *

main:
  // run round-trips through the wire encoding.
  c := Command.run --name="blink" --crc=7 --triggers={"interval": 30}
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
