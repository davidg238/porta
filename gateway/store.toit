// gateway/store.toit — the gateway's sqlite store: nodes, payloads,
// command_queue, reports. Shared (in B2) by the daemon and the CLI.
import sqlite
import encoding.json
import .names show node-name-for

DEFAULT-POLL-INTERVAL-S ::= 30
DEFAULT-MAX-OFFLINE-S ::= 300

NODE-SELECT_ ::= "SELECT id, name, source_addr, first_seen, last_seen, poll_interval_s, max_offline_s, last_report_at, observed_state FROM nodes"

/** The gateway's sqlite-backed store. */
class Store:
  db_/sqlite.Database

  /** Opens (creating if absent) the database at $path and ensures the schema. */
  constructor.open path/string:
    db_ = sqlite.open path
    db_.execute "PRAGMA journal_mode = WAL"
    init-schema_

  /** Closes the underlying database. */
  close -> none:
    db_.close

  init-schema_ -> none:
    db_.execute """
        CREATE TABLE IF NOT EXISTS nodes (
          id TEXT PRIMARY KEY,
          name TEXT,
          source_addr TEXT,
          first_seen INTEGER,
          last_seen INTEGER,
          poll_interval_s INTEGER,
          max_offline_s INTEGER,
          last_report_at INTEGER,
          observed_state TEXT)"""
    db_.execute """
        CREATE TABLE IF NOT EXISTS payloads (
          crc INTEGER PRIMARY KEY,
          name TEXT,
          size INTEGER,
          image BLOB)"""
    db_.execute """
        CREATE TABLE IF NOT EXISTS command_queue (
          id INTEGER PRIMARY KEY AUTOINCREMENT,
          device_id TEXT,
          seq INTEGER,
          verb TEXT,
          args TEXT,
          issued_at INTEGER,
          issued_by TEXT,
          delivered_at INTEGER)"""
    db_.execute """
        CREATE TABLE IF NOT EXISTS reports (
          id INTEGER PRIMARY KEY AUTOINCREMENT,
          device_id TEXT,
          ts INTEGER,
          observed_state TEXT,
          health TEXT)"""

  /** Whether a table named $name exists. Test/diagnostic helper. */
  has-table_ name/string -> bool:
    return (db_.query-one "SELECT 1 FROM sqlite_master WHERE type='table' AND name=?" [name]) != null

  /**
  Records contact from node $id (a MAC) at $now (epoch seconds).

  Inserts a new, auto-named row with default poll/offline windows on first
    contact; on later contacts updates last_seen and (if given) $source-addr.
  */
  touch-node id/string --source-addr/string?=null --now/int -> none:
    if (node id) == null:
      db_.execute "INSERT INTO nodes (id, name, source_addr, first_seen, last_seen, poll_interval_s, max_offline_s) VALUES (?, ?, ?, ?, ?, ?, ?)"
          [id, (node-name-for id), source-addr, now, now, DEFAULT-POLL-INTERVAL-S, DEFAULT-MAX-OFFLINE-S]
    else:
      db_.execute "UPDATE nodes SET last_seen = ?, source_addr = COALESCE(?, source_addr) WHERE id = ?"
          [now, source-addr, id]

  /**
  Ensures a row exists for node $id without recording contact (last_seen stays
    null), so an operator can address a never-yet-seen node by its MAC. $now
    seeds first_seen for ordering.
  */
  ensure-node id/string --now/int -> none:
    if (node id) == null:
      db_.execute "INSERT INTO nodes (id, name, first_seen, poll_interval_s, max_offline_s) VALUES (?, ?, ?, ?, ?)"
          [id, (node-name-for id), now, DEFAULT-POLL-INTERVAL-S, DEFAULT-MAX-OFFLINE-S]

  /** Returns the node row for $id as a map, or null if unknown. */
  node id/string -> Map?:
    return node-row_ (db_.query-one "$NODE-SELECT_ WHERE id = ?" [id])

  /** Returns the node row whose name is $name, or null. */
  node-by-name name/string -> Map?:
    return node-row_ (db_.query-one "$NODE-SELECT_ WHERE name = ?" [name])

  /** Returns all node rows, ordered by name. */
  nodes -> List:
    result := []
    db_.query "$NODE-SELECT_ ORDER BY name": | row | result.add (node-row_ row)
    return result

  /** Overrides node $id's friendly name. */
  set-node-name id/string name/string -> none:
    db_.execute "UPDATE nodes SET name = ? WHERE id = ?" [name, id]

  /** Sets node $id's offline threshold to $seconds (a gateway-side config row). */
  set-max-offline id/string seconds/int -> none:
    db_.execute "UPDATE nodes SET max_offline_s = ? WHERE id = ?" [seconds, id]

  /** Records the intended poll cadence ($seconds) for display; the authoritative change is the enqueued command. */
  set-poll-interval-intended id/string seconds/int -> none:
    db_.execute "UPDATE nodes SET poll_interval_s = ? WHERE id = ?" [seconds, id]

  node-row_ row/List? -> Map?:
    if row == null: return null
    return {
      "id": row[0], "name": row[1], "source_addr": row[2],
      "first_seen": row[3], "last_seen": row[4],
      "poll_interval_s": row[5], "max_offline_s": row[6],
      "last_report_at": row[7], "observed_state": row[8],
    }

// Stores $obj as a JSON string (sqlite TEXT). json.encode yields a ByteArray,
// which a TEXT column would store as a BLOB; .to-string keeps it textual.
encode-json_ obj -> string:
  return (json.encode obj).to-string

// Decodes a JSON string $s (as read back from a TEXT column) to a Toit value.
decode-json_ s/string -> any:
  return json.decode s.to-byte-array
