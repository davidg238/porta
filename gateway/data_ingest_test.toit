// gateway/data_ingest_test.toit
import expect show *
import .handler show StoreBackedHandler
import .store show Store
import tftp show STORAGE-ACCESS-DENIED

main:
  store := Store.open ":memory:"
  handler := StoreBackedHandler store
  store.ensure-node "aabbccddeeff" --now=1000

  // A WRQ to "data" ingests JSONL: one entry per line. The trailing line is
  // truncated (no closing brace) and must be skipped, not abort the batch.
  body := ("{\"ts\":100,\"seq\":0,\"kind\":\"metric\",\"name\":\"pm\",\"value\":13}\n"
         + "{\"ts\":101,\"seq\":1,\"kind\":\"log\",\"text\":\"hi\"}\n"
         + "{\"ts\":102,\"seq\":2,\"kind\":\"met").to-byte-array
  w := handler.writer-for "data?id=aabbccddeeff"
  w.write body
  w.close

  rows := store.query-data "aabbccddeeff" --since=0 --until=200
  expect-equals 2 rows.size                          // 2 good lines; truncated 3rd skipped
  expect-equals 13.0 rows[0]["value"]                // int 13 stored as REAL 13.0
  expect-equals "hi" rows[1]["text"]
  expect-equals "metric" rows[0]["kind"]
  expect-equals "log" rows[1]["kind"]

  // contact was recorded.
  expect ((store.node "aabbccddeeff")["last_seen"]) != null

  // A WRQ to an unknown resource is still refused.
  expect-throw STORAGE-ACCESS-DENIED: handler.writer-for "bogus?id=aabbccddeeff"

  // A WRQ to "data" with no id is refused.
  expect-throw STORAGE-ACCESS-DENIED: handler.writer-for "data"

  // Non-object JSON lines are skipped; the batch is not aborted.
  store.ensure-node "ffeeddccbbaa" --now=1000
  body2 := ("{\"ts\":200,\"seq\":0,\"kind\":\"log\",\"text\":\"a\"}\n"
          + "42\n"
          + "{\"ts\":201,\"seq\":1,\"kind\":\"log\",\"text\":\"b\"}\n").to-byte-array
  w2 := handler.writer-for "data?id=ffeeddccbbaa"
  w2.write body2
  w2.close
  expect-equals 2 (store.query-data "ffeeddccbbaa" --since=0 --until=300).size

  store.close
  print "data ingest OK"
