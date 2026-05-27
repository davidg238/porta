// device/control_service_test.toit
import expect show *
import nodus.control_service show ControlServiceClient ControlServiceProvider

main:
  config := {
    "thermostat": {"target-c": 21.5, "mode": "heat"},
    "sampler": {"threshold": 100, "enabled": true},
  }
  spawn::
    provider := ControlServiceProvider:: config   // read-config lambda
    provider.install
    sleep (Duration --s=2)
    provider.uninstall
  sleep --ms=200  // let the provider register before we open a client.

  client := ControlServiceClient
  client.open
  // Typed values survive the service boundary.
  expect-equals 21.5 (client.get "thermostat" "target-c")
  expect (client.get "thermostat" "target-c") is float
  expect-equals "heat" (client.get "thermostat" "mode")
  expect-equals 100 (client.get "sampler" "threshold")
  expect (client.get "sampler" "threshold") is int
  expect-equals true (client.get "sampler" "enabled")
  // Absent app or key → null.
  expect-null (client.get "thermostat" "missing")
  expect-null (client.get "absent" "k")
  client.close
  print "control service OK"
