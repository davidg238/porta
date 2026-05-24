// gateway/data_log_test.toit
import expect show *
import .store show Store

main:
  store := Store.open ":memory:"
  expect (store.has-table_ "data_log")

  // Metrics of every scalar type, plus a log line.
  store.insert-data "aabbccddeeff" --ts=100 --seq=0 --kind="metric" --name="pm"   --value=13     --value-type="int"
  store.insert-data "aabbccddeeff" --ts=101 --seq=1 --kind="metric" --name="t"    --value=20.5   --value-type="float"
  store.insert-data "aabbccddeeff" --ts=102 --seq=2 --kind="metric" --name="door" --value=1      --value-type="bool"
  store.insert-data "aabbccddeeff" --ts=103 --seq=3 --kind="metric" --name="mode" --text="auto"  --value-type="string"
  store.insert-data "aabbccddeeff" --ts=104 --seq=4 --kind="log"    --text="started blink"

  rows := store.query-data "aabbccddeeff" --since=0 --until=200
  expect-equals 5 rows.size
  // int preserved as int (NUMERIC affinity), not coerced to float.
  expect-equals 13 rows[0]["value"]
  expect-equals "int" rows[0]["value_type"]
  expect (rows[0]["value"] is int)
  // float preserved.
  expect-equals 20.5 rows[1]["value"]
  expect-equals "float" rows[1]["value_type"]
  // bool stored as 0/1 with a type tag.
  expect-equals 1 rows[2]["value"]
  expect-equals "bool" rows[2]["value_type"]
  // string value lives in text.
  expect-equals "auto" rows[3]["text"]
  expect-equals "string" rows[3]["value_type"]
  // log entry: text set, value_type null.
  expect-equals "log" rows[4]["kind"]
  expect-equals "started blink" rows[4]["text"]
  expect-equals null rows[4]["value_type"]

  // kind filter still works.
  expect-equals 4 (store.query-data "aabbccddeeff" --since=0 --until=200 --kind="metric").size

  // time window + prune unchanged.
  expect-equals 0 (store.query-data "aabbccddeeff" --since=200 --until=300).size
  store.prune-data --cutoff=101
  expect-equals 4 (store.query-data "aabbccddeeff" --since=0 --until=200).size  // ts=100 pruned

  store.close
  print "data_log OK"
