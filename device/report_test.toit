import expect show *
import encoding.json
import uuid
import .report show build-report
import .inventory show Inventory InstalledApp
import .triggers show Triggers

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

  // An empty inventory still produces a well-formed report.
  empty := build-report Inventory.empty --uptime-us=5 --wakes=1
  expect-structural-equals {:} (json.decode empty)["apps"]
