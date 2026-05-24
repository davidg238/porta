// gateway/store.toit — the gateway's sqlite store: nodes, payloads,
// command_queue, reports. Shared (in B2) by the daemon and the CLI.
import sqlite
import encoding.json
import .names show node-name-for
import .command show Command

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
    db_.execute "PRAGMA busy_timeout = 5000"
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

  /** Stores image $image (keyed by $crc, labelled $name); replaces any existing row for $crc. */
  register-payload --crc/int --name/string --image/ByteArray -> none:
    db_.execute "INSERT OR REPLACE INTO payloads (crc, name, size, image) VALUES (?, ?, ?, ?)"
        [crc, name, image.size, image]

  /** Whether a payload with $crc is stored. */
  payload-exists crc/int -> bool:
    return (db_.query-one "SELECT 1 FROM payloads WHERE crc = ?" [crc]) != null

  /** Returns the payload for $crc as {"crc","name","size","image"}, or null. */
  payload crc/int -> Map?:
    row := db_.query-one "SELECT crc, name, size, image FROM payloads WHERE crc = ?" [crc]
    if row == null: return null
    return {"crc": row[0], "name": row[1], "size": row[2], "image": row[3]}

  /**
  Appends $command to node $device-id's FIFO queue, recording $issued-by and
    $now (epoch seconds). Returns the new command id.
  */
  enqueue-command device-id/string command/Command --issued-by/string --now/int -> int:
    db_.execute "INSERT INTO command_queue (device_id, verb, args, issued_at, issued_by, delivered_at) VALUES (?, ?, ?, ?, ?, NULL)"
        [device-id, command.verb, (encode-json_ command.args), now, issued-by]
    return db_.last-insert-rowid

  /** Returns $device-id's undelivered commands, oldest first. */
  undelivered-commands device-id/string -> List:
    result := []
    db_.query "SELECT id, verb, args, issued_at, issued_by FROM command_queue WHERE device_id = ? AND delivered_at IS NULL ORDER BY id" [device-id]: | row |
      result.add (command-row_ row)
    return result

  /** Returns the oldest undelivered command for $device-id, or null. */
  next-undelivered device-id/string -> Map?:
    row := db_.query-one "SELECT id, verb, args, issued_at, issued_by FROM command_queue WHERE device_id = ? AND delivered_at IS NULL ORDER BY id LIMIT 1" [device-id]
    if row == null: return null
    return command-row_ row

  /** Marks command $id delivered at $now (epoch seconds). */
  mark-delivered id/int --now/int -> none:
    db_.execute "UPDATE command_queue SET delivered_at = ? WHERE id = ?" [now, id]

  /** Returns the full command history for $device-id, oldest first (the audit log). */
  command-log device-id/string -> List:
    result := []
    db_.query "SELECT id, verb, args, issued_at, issued_by, delivered_at FROM command_queue WHERE device_id = ? ORDER BY id" [device-id]: | row |
      result.add {
        "id": row[0], "verb": row[1], "args": (decode-json_ row[2]),
        "issued_at": row[3], "issued_by": row[4], "delivered_at": row[5],
      }
    return result

  /**
  Records a node's observed state from a wake.

  Appends a row to the reports audit table (with $observed-state and $health,
    each a JSON string, at $now epoch seconds) and refreshes the cached
    observed_state / last_report_at on the $device-id node row.
  */
  insert-report device-id/string --observed-state/string --health/string --now/int -> none:
    db_.execute "INSERT INTO reports (device_id, ts, observed_state, health) VALUES (?, ?, ?, ?)"
        [device-id, now, observed-state, health]
    db_.execute "UPDATE nodes SET observed_state = ?, last_report_at = ? WHERE id = ?"
        [observed-state, now, device-id]

  /** Returns $device-id's reports, newest first. */
  reports device-id/string -> List:
    result := []
    db_.query "SELECT ts, observed_state, health FROM reports WHERE device_id = ? ORDER BY ts DESC" [device-id]: | row |
      result.add {"ts": row[0], "observed_state": row[1], "health": row[2]}
    return result

  command-row_ row/List -> Map:
    return {
      "id": row[0], "verb": row[1], "args": (decode-json_ row[2]),
      "issued_at": row[3], "issued_by": row[4],
    }

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
