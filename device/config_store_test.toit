// device/config_store_test.toit
import expect show *
import .config_store show set-config get-config

main:
  c := {:}
  set-config c "thermostat" "target-c" 21.5
  set-config c "thermostat" "mode" "heat"
  set-config c "sampler" "threshold" 100
  expect-equals 21.5 (get-config c "thermostat" "target-c")
  expect (get-config c "thermostat" "target-c") is float
  expect-equals "heat" (get-config c "thermostat" "mode")
  expect-equals 100 (get-config c "sampler" "threshold")
  expect-null (get-config c "thermostat" "missing")   // unknown key
  expect-null (get-config c "absent" "k")              // unknown app
  set-config c "thermostat" "target-c" 22.0            // overwrite
  expect-equals 22.0 (get-config c "thermostat" "target-c")
  print "config_store OK"
