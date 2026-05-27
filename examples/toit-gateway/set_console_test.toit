// gateway/set_console_test.toit
import expect show *
import .command show Command VERB-SET-CONSOLE

main:
  cmd := Command.set-console --on=true
  expect-equals VERB-SET-CONSOLE cmd.verb
  expect-equals true cmd.args["on"]
  // Round-trips through the wire codec.
  round := Command.decode cmd.encode
  expect-equals VERB-SET-CONSOLE round.verb
  expect-equals true round.args["on"]
  // It is at least one byte (preserves the drain-sentinel invariant).
  expect (cmd.encode.size >= 1)
  print "set-console command OK"
