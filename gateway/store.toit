// gateway/store.toit — the gateway's sqlite store: nodes, payloads,
// command_queue, reports. Shared (in B2) by the daemon and the CLI.
import sqlite
import encoding.json

DEFAULT-POLL-INTERVAL-S ::= 30
DEFAULT-MAX-OFFLINE-S ::= 300

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

// Stores $obj as a JSON string (sqlite TEXT). json.encode yields a ByteArray,
// which a TEXT column would store as a BLOB; .to-string keeps it textual.
encode-json_ obj -> string:
  return (json.encode obj).to-string

// Decodes a JSON string $s (as read back from a TEXT column) to a Toit value.
decode-json_ s/string -> any:
  return json.decode s.to-byte-array
