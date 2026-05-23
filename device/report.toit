// device/report.toit — builds the per-wake state-report body the node PUTs to
// the gateway (WRQ "report?id=<mac>"): observed apps + a small health struct.
import encoding.json
import .inventory show Inventory InstalledApp

/**
Builds the report body as a JSON object {"apps":{name:{crc,runlevel,triggers}},
  "health":{uptime_us,wakes}}. Carries no per-app logs and is bounded by the app
  count (M1's soft cap lives in the supervisor). $uptime-us is monotonic time;
  $wakes is the cumulative wake count.
*/
build-report inventory/Inventory --uptime-us/int --wakes/int -> ByteArray:
  apps := {:}
  inventory.apps.do: | name/string a/InstalledApp |
    apps[name] = {
      "crc": a.crc,
      "runlevel": a.runlevel,
      "triggers": a.triggers.to-map,
    }
  return json.encode {
    "apps": apps,
    "health": {"uptime_us": uptime-us, "wakes": wakes},
  }
