import expect show *
import .store show Store

main:
  store := Store.open ":memory:"
  // The four M1 tables exist after open.
  expect (store.has-table_ "nodes")
  expect (store.has-table_ "payloads")
  expect (store.has-table_ "command_queue")
  expect (store.has-table_ "reports")
  store.close
