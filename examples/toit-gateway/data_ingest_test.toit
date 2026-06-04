// Copyright (c) 2026 Ekorau LLC

// gateway/data_ingest_test.toit
import expect show *
import .handler show StoreBackedHandler
import .store show Store
import tftp show STORAGE-ACCESS-DENIED

main:
  store := Store.open ":memory:"
  handler := StoreBackedHandler store
  store.ensure-node "aabbccddeeff" --now=1000

  // JSONL ingest preserves each metric value's scalar type. The final line is
  // truncated (no closing brace) and must be skipped, not abort the batch.
  body := ("{\"ts\":100,\"seq\":0,\"kind\":\"metric\",\"name\":\"pm\",\"value\":13}\n"
         + "{\"ts\":101,\"seq\":1,\"kind\":\"metric\",\"name\":\"t\",\"value\":20.5}\n"
         + "{\"ts\":102,\"seq\":2,\"kind\":\"metric\",\"name\":\"door\",\"value\":true}\n"
         + "{\"ts\":103,\"seq\":3,\"kind\":\"metric\",\"name\":\"mode\",\"value\":\"auto\"}\n"
         + "{\"ts\":104,\"seq\":4,\"kind\":\"log\",\"text\":\"hi\"}\n"
         + "{\"ts\":105,\"seq\":5,\"kind\":\"met").to-byte-array
  w := handler.writer-for "data?id=aabbccddeeff"
  w.write body
  w.close

  rows := store.query-data "aabbccddeeff" --since=0 --until=200
  expect-equals 5 rows.size                          // 5 good lines; truncated 6th skipped
  // int preserved as int.
  expect-equals 13 rows[0]["value"]
  expect (rows[0]["value"] is int)
  expect-equals "int" rows[0]["value_type"]
  // float preserved.
  expect-equals 20.5 rows[1]["value"]
  expect-equals "float" rows[1]["value_type"]
  // bool stored as 1 with type tag.
  expect-equals 1 rows[2]["value"]
  expect-equals "bool" rows[2]["value_type"]
  // string value lives in text.
  expect-equals "auto" rows[3]["text"]
  expect-equals "string" rows[3]["value_type"]
  expect-equals null rows[3]["value"]                // string lives in text, value is null
  // log entry passes through, value_type null.
  expect-equals "log" rows[4]["kind"]
  expect-equals "hi" rows[4]["text"]
  expect-equals null rows[4]["value_type"]

  // contact recorded.
  expect ((store.node "aabbccddeeff")["last_seen"]) != null

  // Unknown resource and no-id are refused.
  expect-throw STORAGE-ACCESS-DENIED: handler.writer-for "bogus?id=aabbccddeeff"
  expect-throw STORAGE-ACCESS-DENIED: handler.writer-for "data"

  // A non-object JSON line is skipped, not fatal.
  store.ensure-node "ffeeddccbbaa" --now=1000
  body2 := ("{\"ts\":200,\"seq\":0,\"kind\":\"log\",\"text\":\"a\"}\n"
          + "42\n"
          + "{\"ts\":201,\"seq\":1,\"kind\":\"log\",\"text\":\"b\"}\n").to-byte-array
  w2 := handler.writer-for "data?id=ffeeddccbbaa"
  w2.write body2
  w2.close
  expect-equals 2 (store.query-data "ffeeddccbbaa" --since=0 --until=300).size

  // A non-scalar value (JSON array/object) is unsupported: the row is still
  // inserted, but value and value_type are null (graceful degradation, not a crash).
  store.ensure-node "112233445566" --now=1000
  body3 := "{\"ts\":300,\"seq\":0,\"kind\":\"metric\",\"name\":\"x\",\"value\":[1,2]}\n".to-byte-array
  w3 := handler.writer-for "data?id=112233445566"
  w3.write body3
  w3.close
  row3s := store.query-data "112233445566" --since=0 --until=400
  expect-equals 1 row3s.size
  expect-equals null row3s[0]["value"]
  expect-equals null row3s[0]["value_type"]

  store.close
  print "data ingest OK"
