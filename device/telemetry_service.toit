// device/telemetry_service.toit — the device-wide telemetry API. Payload apps open
// a TelemetryServiceClient and call `log`/`report`; the provider (spawned by the
// supervisor) buffers entries, and the supervisor `drain`s them once per wake.
import system.services
import .telemetry_buffer show TelemetryBuffer

interface TelemetryService:
  static SELECTOR ::= services.ServiceSelector
      --uuid="7c3a1e90-2b4d-4f8a-9c6e-5a1b2c3d4e5f"
      --major=1
      --minor=0
  log message/string -> none
  static LOG-INDEX ::= 0

  report name/string value/float -> none
  static REPORT-INDEX ::= 1

  drain -> List
  static DRAIN-INDEX ::= 2

class TelemetryServiceClient extends services.ServiceClient implements TelemetryService:
  static SELECTOR ::= TelemetryService.SELECTOR
  constructor selector/services.ServiceSelector=SELECTOR:
    assert: selector.matches SELECTOR
    super selector

  log message/string -> none: invoke_ TelemetryService.LOG-INDEX message
  report name/string value/float -> none: invoke_ TelemetryService.REPORT-INDEX [name, value]
  drain -> List: return invoke_ TelemetryService.DRAIN-INDEX null

class TelemetryServiceProvider extends services.ServiceProvider
    implements TelemetryService services.ServiceHandler:
  buffer_/TelemetryBuffer
  constructor .buffer_:
    super "porta/telemetry" --major=1 --minor=0
    provides TelemetryService.SELECTOR --handler=this

  handle index/int arguments/any --gid/int --client/int -> any:
    if index == TelemetryService.LOG-INDEX: return log arguments
    if index == TelemetryService.REPORT-INDEX:
      value := arguments[1]
      if value is int: value = value.to-float
      return report arguments[0] value
    if index == TelemetryService.DRAIN-INDEX: return drain
    unreachable

  log message/string -> none: buffer_.add {"kind": "log", "text": message}
  report name/string value/float -> none: buffer_.add {"kind": "metric", "name": name, "value": value}
  drain -> List: return buffer_.drain
