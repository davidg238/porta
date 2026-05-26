// device/vin.toit — VINDRIKTNING (PM1006) air-quality payload. boot × run-once:
// per wake read 8 PM2.5 samples, report the olympic (trimmed) mean, return. The
// supervisor waits on this run-once container (under MAX-AWAKE) then deep-sleeps.
// Telemetry forwarding must be on (`gateway device set-console --on`) for the value
// to ship to the gateway.
import .pm1006 show Pm1006
import .olympic show olympic-mean
import .telemetry_service show TelemetryServiceClient

RX-PIN ::= 25     // PM1006 TX -> ESP32 RX. Adjust to your wiring.
SAMPLES ::= 8

main:
  sensor := Pm1006 --rx=RX-PIN
  samples := []
  SAMPLES.repeat: samples.add sensor.read-pm25
  sensor.close

  pm25 := olympic-mean samples

  tel := TelemetryServiceClient
  tel.open
  tel.log "vin: pm25=$pm25 (olympic of $SAMPLES)"
  tel.report "pm25" pm25
  tel.close
  print "vin: done (pm25=$pm25)"
