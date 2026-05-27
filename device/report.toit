// device/report.toit — builds the per-wake state-report body the node PUTs to
// the gateway (WRQ "report?id=<mac>"): observed apps + a small health struct.
import encoding.json
import .inventory show Inventory InstalledApp

/**
Builds the report body as a JSON object {"apps":{name:{crc,runlevel,lifecycle,triggers}},
  "config":{app:{key:value}}, "health":{uptime_us,wakes}}. $config is the node's
  applied per-app config blob (see device/config_store.toit); it defaults to empty.
  Carries no per-app logs and is bounded by the app/config count. $uptime-us is
  monotonic time; $wakes is the cumulative wake count.
*/
build-report inventory/Inventory --config/Map={:} --uptime-us/int --wakes/int -> ByteArray:
  apps := {:}
  inventory.apps.do: | name/string a/InstalledApp |
    apps[name] = {
      "crc": a.crc,
      "runlevel": a.runlevel,
      "lifecycle": a.lifecycle,
      "triggers": a.triggers.to-map,
    }
  return json.encode {
    "apps": apps,
    "config": config,
    "health": {"uptime_us": uptime-us, "wakes": wakes},
  }
