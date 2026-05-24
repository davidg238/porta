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
  yield  // let the provider register before we open a client

  client := TelemetryServiceClient
  client.open
  client.log "hello"
  client.report "pm" 13.0
  out := client.drain
  expect-equals 2 out.size
  expect-equals "log" out[0]["kind"]
  expect-equals "hello" out[0]["text"]
  expect-equals "metric" out[1]["kind"]
  expect-equals "pm" out[1]["name"]
  expect-equals 13.0 out[1]["value"]
  // drain emptied the buffer.
  expect-equals 0 (client.drain).size
  client.close
  print "telemetry service OK"
