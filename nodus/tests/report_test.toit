import expect show *
import encoding.json
import uuid
import nodus.report show build-report
import nodus.inventory show Inventory InstalledApp
import nodus.triggers show Triggers

main:
  app := InstalledApp
      --name="blink"
      --id=(uuid.Uuid #[0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15])
      --size=2048
      --crc=999
      --triggers=(Triggers --interval-s=30)
      --runlevel=3
  inv := Inventory {"blink": app}

  body := build-report inv --uptime-us=1_000_000 --wakes=7
  obj := json.decode body
  expect-equals 999 obj["apps"]["blink"]["crc"]
  expect-equals 3 obj["apps"]["blink"]["runlevel"]
  expect-equals 30 obj["apps"]["blink"]["triggers"]["interval"]
  expect-equals 7 obj["health"]["wakes"]
  expect-equals 1_000_000 obj["health"]["uptime_us"]
  expect-structural-equals {:} obj["config"]

  // An empty inventory still produces a well-formed report.
  empty := build-report Inventory.empty --uptime-us=5 --wakes=1
  expect-structural-equals {:} (json.decode empty)["apps"]

  // The report carries the applied per-app config blob verbatim.
  with-config := build-report inv
      --config=({"blink": {"target": 21.5, "mode": "heat"}})
      --uptime-us=2
      --wakes=3
  cfg := (json.decode with-config)["config"]
  expect-equals 21.5 cfg["blink"]["target"]
  expect-equals "heat" cfg["blink"]["mode"]

  // An omitted config defaults to an empty object (uniform body shape).
  expect-structural-equals {:} (json.decode empty)["config"]

  // The report echoes each app's declared lifecycle (parallel to runlevel).
  vin-app := InstalledApp --name="vin" --id=(uuid.Uuid.uuid5 "" "vin") --size=1 --crc=2 --triggers=(Triggers --boot) --runlevel=3 --lifecycle="run-loop"
  vin-body := build-report (Inventory {"vin": vin-app}) --uptime-us=0 --wakes=1
  decoded := json.decode vin-body
  expect-equals "run-loop" decoded["apps"]["vin"]["lifecycle"]
