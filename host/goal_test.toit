// host/goal_test.toit
import expect show *
import encoding.json
import .goal show build-goal

main:
  bytes := build-goal --name="payload" --size=38016 --crc=2157114022 --interval-s=5
  obj := json.decode bytes
  app := obj["apps"]["payload"]
  expect-equals 38016 app["size"]
  expect-equals 2157114022 app["crc"]
  expect-equals 5 app["triggers"]["interval"]
  expect-equals 3 app["runlevel"]
