// Copyright (c) 2026 Ekorau LLC

// gateway/serve.toit — the gateway daemon. Opens the sqlite store, wraps it in
// the StoreBackedHandler, and runs a TFTPServer over UDP. Replaces host/serve.toit.
import cli
import log
import tftp show TFTPServer
import .store show Store
import .handler show StoreBackedHandler

/** Default unprivileged UDP port (port 69 needs root); the node must match it. */
DEFAULT-PORT ::= 6969

/** Opens the store and serves the command queue + payloads until killed. */
cmd-serve parsed/cli.Parsed -> none:
  db := parsed["db"]
  port := parsed["port"]
  store := Store.open db
  handler := StoreBackedHandler store
  server := TFTPServer --storage=handler --port=port --logger=log.default
  print "gateway: serving command queue + payloads on UDP/$port (db=$db)"
  server.start
