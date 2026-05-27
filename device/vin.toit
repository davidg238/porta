// device/vin.toit — VINDRIKTNING (PM1006) air-quality payload. boot × run-once:
// per wake read 8 PM2.5 samples, report the olympic (trimmed) mean, return. The
// supervisor waits on this run-once container (under MAX-AWAKE) then deep-sleeps.
// Telemetry forwarding must be on (`gateway device set-console --on`) for the value
// to ship to the gateway.
//
// LED feedback (single LED on LED-PIN, active-low): solid on while sampling, a brief
// blink as each frame arrives, off when sampling completes. A stuck-on LED therefore
// means "sampling but no frames" (check the sensor wiring/power) — the supervisor's
// MAX-AWAKE cap ends the wake either way.
import gpio
import .pm1006 show Pm1006
import .olympic show olympic-mean
import .telemetry_service show TelemetryServiceClient

RX-PIN ::= 21     // PM1006 TX -> ESP32 RX (matches the vindriktning module wiring).
LED-PIN ::= 13    // Sampling-feedback LED, active-low (set 0 = on).
SAMPLES ::= 8

main:
  led := gpio.Pin LED-PIN --output
  led.set 0                       // active-low: ON — sampling in progress
  sensor := Pm1006 --rx=RX-PIN
  samples := []
  SAMPLES.repeat:
    samples.add sensor.read-pm25
    led.set 1; sleep --ms=40; led.set 0   // brief blink to mark each received frame
  sensor.close
  led.set 1                       // OFF — sampling complete
  led.close

  pm25 := olympic-mean samples

  tel := TelemetryServiceClient
  tel.open
  tel.log "vin: pm25=$pm25 (olympic of $SAMPLES)"
  tel.report "pm25" pm25
  tel.close
  print "vin: done (pm25=$pm25)"
