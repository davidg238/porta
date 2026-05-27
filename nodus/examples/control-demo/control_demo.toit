// device/control_demo.toit — M2.2 hardware demo. Reads its own config via
// ControlService and echoes it back up via TelemetryService, so the down-path is
// observable in `gateway monitor`. Install as app name "control-demo".
import nodus.control_service show ControlServiceClient
import nodus.telemetry_service show TelemetryServiceClient

APP ::= "control-demo"

main:
  control := ControlServiceClient
  control.open
  target := control.get APP "target"      // set via `device set -d <id> control-demo target=<v>`
  control.close

  tel := TelemetryServiceClient
  tel.open
  tel.log "control-demo: target=$target"
  if target != null: tel.report "target" target   // echo the typed value back up
  tel.close
  print "control-demo: done (target=$target)"
