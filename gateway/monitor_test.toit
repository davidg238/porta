// gateway/monitor_test.toit
import expect show *
import .gateway show monitor-line_

main:
  metric := {"ts": 100, "seq": 0, "kind": "metric", "name": "pm", "value": 13.0, "text": null}
  expect-equals "100  metric  pm=13.0" (monitor-line_ metric)
  log := {"ts": 101, "seq": 1, "kind": "log", "name": null, "value": null, "text": "started blink"}
  expect-equals "101  log     started blink" (monitor-line_ log)
  print "monitor-line OK"
