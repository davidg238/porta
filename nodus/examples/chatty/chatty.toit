// device/chatty.toit — a test payload that emits telemetry each run, to verify the
// M2 up-path end to end. Exercises every scalar value type (bool/string/int/float)
// plus a log line, so `gateway monitor` shows the full typed surface. Install it via
// `gateway container install`.
import nodus.telemetry_service show TelemetryServiceClient

main:
  client := TelemetryServiceClient
  client.open
  client.report "boot" true          // bool
  client.report "mode" "blink"       // string
  5.repeat: | i |
    client.log "chatty: tick $i"
    client.report "counter" i         // int
    client.report "load" (i * 1.5)    // float
    sleep --ms=500
  client.close
  print "chatty: done"
