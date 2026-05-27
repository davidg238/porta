// device/set_console_apply_test.toit
import expect show *
import encoding.json
import nodus.node_command show NodeCommand

main:
  bytes := json.encode {"verb": "set-console", "on": true}
  cmd := NodeCommand.decode bytes
  expect cmd.is-set-console
  expect-equals true (cmd.args.get "on")
  // A run command is not a set-console.
  run := NodeCommand.decode (json.encode {"verb": "run", "name": "blink", "crc": 1, "size": 2})
  expect-not run.is-set-console
  print "set-console decode OK"
