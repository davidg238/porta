// gateway/monitor_test.toit
import expect show *
import .gateway show monitor-line_

main:
  intm := {"ts": 100, "seq": 0, "kind": "metric", "name": "n", "value": 7, "text": null, "value_type": "int"}
  expect-equals "100  metric  n=7" (monitor-line_ intm)
  floatm := {"ts": 101, "seq": 1, "kind": "metric", "name": "pm", "value": 13.0, "text": null, "value_type": "float"}
  expect-equals "101  metric  pm=13.0" (monitor-line_ floatm)
  boolt := {"ts": 102, "seq": 2, "kind": "metric", "name": "door", "value": 1, "text": null, "value_type": "bool"}
  expect-equals "102  metric  door=true" (monitor-line_ boolt)
  boolf := {"ts": 103, "seq": 3, "kind": "metric", "name": "door", "value": 0, "text": null, "value_type": "bool"}
  expect-equals "103  metric  door=false" (monitor-line_ boolf)
  strm := {"ts": 104, "seq": 4, "kind": "metric", "name": "mode", "value": null, "text": "auto", "value_type": "string"}
  expect-equals "104  metric  mode=auto" (monitor-line_ strm)
  log := {"ts": 105, "seq": 5, "kind": "log", "name": null, "value": null, "text": "started blink", "value_type": null}
  expect-equals "105  log     started blink" (monitor-line_ log)
  // Graceful degradation: a metric whose value was an unsupported (non-scalar) type
  // ingested as value=null, value_type=null — renders the name with a "null" value.
  degraded := {"ts": 106, "seq": 6, "kind": "metric", "name": "x", "value": null, "text": null, "value_type": null}
  expect-equals "106  metric  x=null" (monitor-line_ degraded)
  print "monitor-line OK"
