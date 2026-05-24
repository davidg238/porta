// gateway/data_log_test.toit
import expect show *
import .store show Store

main:
  store := Store.open ":memory:"
  expect (store.has-table_ "data_log")

  // Insert a metric and a log line for one device.
  store.insert-data "aabbccddeeff" --ts=100 --seq=0 --kind="metric" --name="pm" --value=13.0
  store.insert-data "aabbccddeeff" --ts=101 --seq=1 --kind="log" --text="started blink"
  // A row for a different, later device + time (must not leak into the query below).
  store.insert-data "010203040506" --ts=500 --seq=0 --kind="metric" --name="t" --value=20.5

  rows := store.query-data "aabbccddeeff" --since=0 --until=200
  expect-equals 2 rows.size
  expect-equals "metric" rows[0]["kind"]            // oldest first (ts, seq)
  expect-equals "pm" rows[0]["name"]
  expect-equals 13.0 rows[0]["value"]
  expect-equals "log" rows[1]["kind"]
  expect-equals "started blink" rows[1]["text"]

  // kind filter.
  only-metrics := store.query-data "aabbccddeeff" --since=0 --until=200 --kind="metric"
  expect-equals 1 only-metrics.size

  // time window excludes out-of-range rows.
  expect-equals 0 (store.query-data "aabbccddeeff" --since=200 --until=300).size

  // prune drops rows older than the cutoff.
  store.prune-data --cutoff=101
  expect-equals 1 (store.query-data "aabbccddeeff" --since=0 --until=200).size  // ts=100 pruned, ts=101 kept

  store.close
  print "data_log OK"
