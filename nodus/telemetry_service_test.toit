// device/telemetry_service_test.toit
import expect show *
import .telemetry_buffer show TelemetryBuffer
import .telemetry_service show TelemetryServiceClient TelemetryServiceProvider

main:
  spawn::
    provider := TelemetryServiceProvider (TelemetryBuffer --cap=64)
    provider.install
    sleep (Duration --s=2)
    provider.uninstall
  sleep --ms=200  // Let the provider register before we open a client.

  client := TelemetryServiceClient
  client.open
  client.log "hello"
  client.report "pm" 13.0       // float
  client.report "n" 7            // int
  client.report "door" true      // bool
  client.report "mode" "auto"    // string
  out := client.drain
  expect-equals 5 out.size
  expect-equals "log" out[0]["kind"]
  expect-equals "hello" out[0]["text"]
  // Float preserved.
  expect-equals "metric" out[1]["kind"]
  expect-equals "pm" out[1]["name"]
  expect-equals 13.0 out[1]["value"]
  expect (out[1]["value"] is float)
  // Int preserved (NOT coerced to float).
  expect-equals 7 out[2]["value"]
  expect (out[2]["value"] is int)
  // Bool preserved.
  expect-equals true out[3]["value"]
  expect (out[3]["value"] is bool)
  // String preserved.
  expect-equals "auto" out[4]["value"]
  expect (out[4]["value"] is string)
  // Drain emptied the buffer.
  expect-equals 0 (client.drain).size
  client.close
  print "telemetry service OK"
