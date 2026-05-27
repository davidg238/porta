// device/control_service.toit — the device-wide config (setpoints) read API.
// A payload app opens a ControlServiceClient and calls `get <its-app-name> <key>`.
// The provider (spawned by the supervisor) answers from a config map supplied by a
// read-config lambda — in production that lambda reads the NVS config blob live, so
// a `set` drained earlier this wake is visible. See device/config_store.toit.
import system.services
import .config_store show get-config

interface ControlService:
  static SELECTOR ::= services.ServiceSelector
      --uuid="9d4e1f72-6a3b-4c2e-8f1d-2b7c5a9e0d83"
      --major=1
      --minor=0
  /** Returns app $app's config $key (int/float/bool/string), or null if unset. */
  get app/string key/string -> any
  static GET-INDEX ::= 0

class ControlServiceClient extends services.ServiceClient implements ControlService:
  static SELECTOR ::= ControlService.SELECTOR
  constructor selector/services.ServiceSelector=SELECTOR:
    assert: selector.matches SELECTOR
    super selector

  get app/string key/string -> any: return invoke_ ControlService.GET-INDEX [app, key]

class ControlServiceProvider extends services.ServiceProvider
    implements ControlService services.ServiceHandler:
  read-config_/Lambda   // -> Map ; called per get so config stays live
  constructor .read-config_:
    super "porta/control" --major=1 --minor=0
    provides ControlService.SELECTOR --handler=this

  handle index/int arguments/any --gid/int --client/int -> any:
    if index == ControlService.GET-INDEX: return get arguments[0] arguments[1]
    unreachable

  get app/string key/string -> any: return get-config read-config_.call app key
