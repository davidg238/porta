// device/vin.toit — VINDRIKTNING (PM1006) air-quality payload. boot × run-once:
// per wake read 8 PM2.5 samples, report the olympic (trimmed) mean, return. The
// supervisor waits on this run-once container (under MAX-AWAKE) then deep-sleeps.
// Telemetry forwarding must be on (`gateway device set-console --on`) for the value
// to ship to the gateway.
//
// Uses the hardware-proven .vindriktning driver (preamble-synced PM1006 reader).
//
// LED feedback (single LED on LED-PIN, active-low): idle off, a 40ms ON pulse as each
// frame arrives. So ~8 visible flashes => frames flowing; dark => no frames (the
// supervisor's MAX-AWAKE cap ends the wake either way).
import gpio
import .vindriktning show Vindriktning
import .olympic show olympic-mean
import .telemetry_service show TelemetryServiceClient

RX-PIN ::= 21     // PM1006 TX -> ESP32 RX (matches the vindriktning module wiring).
LED-PIN ::= 13    // Sampling-feedback LED, active-low (set 0 = on).
SAMPLES ::= 8

main:
  led := gpio.Pin LED-PIN --output
  led.set 1                       // active-low: OFF — idle
  sensor := Vindriktning RX-PIN
  samples := []
  SAMPLES.repeat:
    sensor.next                   // blocks until a valid preamble-framed reading
    samples.add sensor.air-quality
    led.set 0; sleep --ms=40; led.set 1   // 40ms ON pulse to mark each received frame
  led.close                       // leaves the pin released (off)

  pm25 := olympic-mean samples

  tel := TelemetryServiceClient
  tel.open
  tel.log "vin: pm25=$pm25 (olympic of $SAMPLES)"
  tel.report "pm25" pm25
  tel.close
  print "vin: done (pm25=$pm25)"
