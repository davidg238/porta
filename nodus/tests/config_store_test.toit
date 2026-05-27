// device/config_store_test.toit
import encoding.tison
import expect show *
import nodus.config_store show set-config get-config mutable-config-copy

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

  // Regression (hardware-found via D5): a config reloaded from NVS is tison-decoded
  // into FIXED-SIZE maps — adding a new key to one throws COLLECTION_CANNOT_CHANGE_SIZE
  // (replacing an existing value is fine, which is why it stayed latent). load-config
  // must return a growable copy; mutable-config-copy is what does that.
  raw := tison.decode (tison.encode {"thermostat": {"setpoint": 22.5}})
  expect-throw "COLLECTION_CANNOT_CHANGE_SIZE": raw["thermostat"]["new"] = 1  // the bug, documented

  cfg := mutable-config-copy raw
  set-config cfg "thermostat" "mode" "heat"   // add a new key to an existing app
  set-config cfg "boiler" "target" 60         // add a brand-new app
  expect-equals 22.5 (get-config cfg "thermostat" "setpoint")  // preserved
  expect-equals "heat" (get-config cfg "thermostat" "mode")
  expect-equals 60 (get-config cfg "boiler" "target")
  // An empty/absent blob copies to a fresh growable map.
  empty := mutable-config-copy {:}
  set-config empty "a" "k" 1
  expect-equals 1 (get-config empty "a" "k")
  print "config_store OK"
