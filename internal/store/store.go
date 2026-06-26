// Copyright (c) 2026 Ekorau LLC

// Package store is the porta gateway's sqlite data layer: node inventory,
// payload blobs, the command queue, and the append-only report log.
package store

import (
	"database/sql"
	"encoding/json"
	"errors"

	_ "github.com/mattn/go-sqlite3"
)

const (
	DefaultPollIntervalS = 30
	// OfflineMultiplier is porta's liveness policy constant k: a node is offline
	// once silent for k × its check-in cadence. k=3 tolerates 2 consecutive
	// missed check-ins (flaky TFTP / WiFi re-assoc) before flapping offline.
	OfflineMultiplier = 3
)

const schema = `
CREATE TABLE IF NOT EXISTS nodes (
  id TEXT PRIMARY KEY,
  name TEXT,
  source_addr TEXT,
  kind TEXT NOT NULL DEFAULT 'toit',
  first_seen INTEGER,
  last_seen INTEGER,
  poll_interval_s INTEGER DEFAULT 30,
  last_report_at INTEGER,
  observed_state TEXT,
  chip TEXT,
  sdk TEXT,
  last_reset TEXT,
  last_reset_code INTEGER,
  node_config TEXT
);
CREATE TABLE IF NOT EXISTS payloads (
  crc INTEGER PRIMARY KEY,
  name TEXT,
  size INTEGER,
  image BLOB
);
CREATE TABLE IF NOT EXISTS command_queue (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  device_id TEXT,
  verb TEXT,
  args TEXT,
  issued_at INTEGER,
  issued_by TEXT,
  delivered_at INTEGER
);
CREATE TABLE IF NOT EXISTS reports (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  device_id TEXT,
  ts INTEGER,
  observed_state TEXT,
  health TEXT
);
CREATE TABLE IF NOT EXISTS data_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  device_id TEXT,
  ts INTEGER,
  seq INTEGER,
  kind TEXT,
  name TEXT,
  value NUMERIC,
  text TEXT,
  value_type TEXT,
  level TEXT
);
CREATE INDEX IF NOT EXISTS idx_data_device_ts ON data_log(device_id, ts);
CREATE TABLE IF NOT EXISTS debug_request (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  device_id TEXT,
  line TEXT,
  issued_at INTEGER,
  delivered_at INTEGER
);
CREATE TABLE IF NOT EXISTS debug_response (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  device_id TEXT,
  ts INTEGER,
  seq INTEGER,
  line TEXT
);
CREATE INDEX IF NOT EXISTS idx_dbgreq_device ON debug_request(device_id, delivered_at);
CREATE INDEX IF NOT EXISTS idx_dbgresp_device ON debug_response(device_id, id);
`

// Store wraps the sqlite database.
type Store struct {
	db *sql.DB
}

// Node is a row from the nodes table.
type Node struct {
	ID            string
	Name          string
	SourceAddr    string
	Kind          string
	FirstSeen     sql.NullInt64
	LastSeen      sql.NullInt64
	PollIntervalS int64
	LastReportAt  sql.NullInt64
	ObservedState string
	Chip          string
	Sdk           string
	LastReset     string
	LastResetCode sql.NullInt64
	// NodeConfig is the node's last echoed effective-config block (the raw
	// node_config JSON), persisted on cold boot + on-change only. "" until the
	// node first echoes it. The node owns its config; porta caches + derives.
	NodeConfig string
}

// CadenceS returns the node's control-plane check-in cadence in seconds, parsed
// from its echoed node_config: a deep-sleep node's cadence is its max_asleep_s,
// an always-on node's is its loop_sleep_s. 0 when no (or unparseable) echo —
// callers then fall back to the stored poll_interval_s default.
func (n *Node) CadenceS() int64 {
	if n.NodeConfig == "" {
		return 0
	}
	var c struct {
		Mode       string `json:"mode"`
		MaxAsleepS int64  `json:"max_asleep_s"`
		LoopSleepS int64  `json:"loop_sleep_s"`
	}
	if json.Unmarshal([]byte(n.NodeConfig), &c) != nil {
		return 0
	}
	switch c.Mode {
	case "deep-sleep":
		return c.MaxAsleepS
	case "always-on":
		return c.LoopSleepS
	}
	return 0
}

// Mode returns the node's echoed power mode ("deep-sleep"/"always-on"), or "" if
// it has not echoed a node_config yet.
func (n *Node) Mode() string {
	if n.NodeConfig == "" {
		return ""
	}
	var c struct {
		Mode string `json:"mode"`
	}
	if json.Unmarshal([]byte(n.NodeConfig), &c) != nil {
		return ""
	}
	return c.Mode
}

// EffectiveCadenceS is the node's check-in cadence used for liveness: the
// cadence echoed in node_config, else the stored poll_interval_s (a pre-echo
// bootstrap fallback), else the default. Always > 0.
func (n *Node) EffectiveCadenceS() int64 {
	c := n.CadenceS()
	if c <= 0 {
		c = n.PollIntervalS
	}
	if c <= 0 {
		c = DefaultPollIntervalS
	}
	return c
}

// OfflineThresholdS is the silence (seconds) after which the node reads offline:
// OfflineMultiplier × its effective cadence. Derived — porta stores no settable
// max_offline.
func (n *Node) OfflineThresholdS() int64 {
	return OfflineMultiplier * n.EffectiveCadenceS()
}

// Online reports whether the node has been seen within its derived offline
// window (OfflineMultiplier × cadence).
func (n *Node) Online(now int64) bool {
	return n.LastSeen.Valid && (now-n.LastSeen.Int64) <= n.OfflineThresholdS()
}

// Command is a row from the command_queue table.
type Command struct {
	ID          int64
	Verb        string
	Args        string // JSON object of flattened args
	IssuedAt    int64
	IssuedBy    string
	DeliveredAt sql.NullInt64
}

// Open opens (creating if needed) the sqlite database and applies the schema.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// nullStr returns a driver value that is NULL when v is empty.
func nullStr(v string) interface{} {
	if v == "" {
		return nil
	}
	return v
}

// nullInt returns a driver value that is NULL when v is nil.
func nullInt(v *int64) interface{} {
	if v == nil {
		return nil
	}
	return *v
}

// TouchNode records contact: creates the node on first sight (with an
// auto-assigned name), otherwise bumps last_seen and refreshes source_addr.
// An empty source_addr is COALESCEd so it never clobbers a known address.
func (s *Store) TouchNode(id, sourceAddr string, now int64) error {
	_, err := s.db.Exec(`
		INSERT INTO nodes (id, name, source_addr, first_seen, last_seen, poll_interval_s)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
		  last_seen = excluded.last_seen,
		  source_addr = COALESCE(excluded.source_addr, nodes.source_addr)`,
		id, NodeNameFor(id), nullStr(sourceAddr), now, now, DefaultPollIntervalS)
	return err
}

// EnsureNode guarantees a row exists without recording contact (no last_seen).
// Used to address a node by MAC before its first poll.
func (s *Store) EnsureNode(id string, now int64) error {
	_, err := s.db.Exec(`
		INSERT INTO nodes (id, name, first_seen, poll_interval_s)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`,
		id, NodeNameFor(id), now, DefaultPollIntervalS)
	return err
}

const nodeCols = `id, COALESCE(name,''), COALESCE(source_addr,''), kind, first_seen, last_seen,
	COALESCE(poll_interval_s,30), last_report_at,
	COALESCE(observed_state,''), COALESCE(chip,''), COALESCE(sdk,''),
	COALESCE(last_reset,''), last_reset_code, COALESCE(node_config,'')`

func scanNode(row interface{ Scan(...interface{}) error }) (*Node, error) {
	var n Node
	err := row.Scan(&n.ID, &n.Name, &n.SourceAddr, &n.Kind, &n.FirstSeen,
		&n.LastSeen, &n.PollIntervalS, &n.LastReportAt, &n.ObservedState,
		&n.Chip, &n.Sdk, &n.LastReset, &n.LastResetCode, &n.NodeConfig)
	if err != nil {
		return nil, err
	}
	return &n, nil
}

// GetNode returns the node row or (nil, nil) if absent.
func (s *Store) GetNode(id string) (*Node, error) {
	n, err := scanNode(s.db.QueryRow(`SELECT `+nodeCols+` FROM nodes WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return n, err
}

// NodeByName returns the node with the given friendly name, or (nil, nil).
func (s *Store) NodeByName(name string) (*Node, error) {
	n, err := scanNode(s.db.QueryRow(`SELECT `+nodeCols+` FROM nodes WHERE name = ?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return n, err
}

// ListNodes returns all nodes ordered by id.
func (s *Store) ListNodes() ([]Node, error) {
	rows, err := s.db.Query(`SELECT ` + nodeCols + ` FROM nodes ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	return out, rows.Err()
}

func (s *Store) SetNodeName(id, name string) error {
	_, err := s.db.Exec(`UPDATE nodes SET name = ? WHERE id = ?`, name, id)
	return err
}

// UpdateNodeIdentity records the node's self-reported firmware identity.
// Empty chip/sdk/kind are COALESCEd so a report missing the field never
// clobbers a previously-known value (kind then keeps its 'toit' default).
func (s *Store) UpdateNodeIdentity(id, chip, sdk, kind string) error {
	_, err := s.db.Exec(
		`UPDATE nodes SET chip = COALESCE(?, chip), sdk = COALESCE(?, sdk),
		 kind = COALESCE(?, kind) WHERE id = ?`,
		nullStr(chip), nullStr(sdk), nullStr(kind), id)
	return err
}

// UpdateNodeReset records the node's last reported reset category + optional
// raw platform code. An empty category / nil code is COALESCEd so a report
// missing the field never clobbers a previously-known value.
func (s *Store) UpdateNodeReset(id, reset string, code *int64) error {
	_, err := s.db.Exec(
		`UPDATE nodes SET last_reset = COALESCE(?, last_reset),
		 last_reset_code = COALESCE(?, last_reset_code) WHERE id = ?`,
		nullStr(reset), nullInt(code), id)
	return err
}

// UpdateNodeConfig caches the node's echoed effective-config block and mirrors
// the node-owned name for display. An empty configJSON / name is COALESCEd so a
// steady-state report (no echo) never clobbers the cache, and an unnamed echo
// (name key omitted) keeps porta's prior/auto-assigned name. The node owns its
// config + name; porta only mirrors what it echoes.
func (s *Store) UpdateNodeConfig(id, configJSON, name string) error {
	_, err := s.db.Exec(
		`UPDATE nodes SET node_config = COALESCE(?, node_config),
		 name = COALESCE(?, name) WHERE id = ?`,
		nullStr(configJSON), nullStr(name), id)
	return err
}

func (s *Store) RegisterPayload(crc int64, name string, image []byte) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO payloads (crc, name, size, image) VALUES (?, ?, ?, ?)`,
		crc, name, len(image), image)
	return err
}

func (s *Store) PayloadExists(crc int64) (bool, error) {
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM payloads WHERE crc = ?`, crc).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// Payload returns the raw image bytes for crc, or (nil, nil) if absent.
func (s *Store) Payload(crc int64) ([]byte, error) {
	var img []byte
	err := s.db.QueryRow(`SELECT image FROM payloads WHERE crc = ?`, crc).Scan(&img)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return img, err
}

// EnqueueCommand appends a command and returns its new id.
func (s *Store) EnqueueCommand(deviceID, verb, argsJSON, issuedBy string, now int64) (int64, error) {
	res, err := s.db.Exec(`
		INSERT INTO command_queue (device_id, verb, args, issued_at, issued_by, delivered_at)
		VALUES (?, ?, ?, ?, ?, NULL)`,
		deviceID, verb, argsJSON, now, issuedBy)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func scanCommand(row interface{ Scan(...interface{}) error }) (*Command, error) {
	var c Command
	err := row.Scan(&c.ID, &c.Verb, &c.Args, &c.IssuedAt, &c.IssuedBy, &c.DeliveredAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

const cmdCols = `id, verb, COALESCE(args,''), issued_at, COALESCE(issued_by,''), delivered_at`

// NextUndelivered returns the oldest undelivered command, or (nil, nil).
func (s *Store) NextUndelivered(deviceID string) (*Command, error) {
	c, err := scanCommand(s.db.QueryRow(`SELECT `+cmdCols+`
		FROM command_queue WHERE device_id = ? AND delivered_at IS NULL
		ORDER BY id LIMIT 1`, deviceID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return c, err
}

func (s *Store) queryCommands(where, deviceID string) ([]Command, error) {
	rows, err := s.db.Query(`SELECT `+cmdCols+` FROM command_queue WHERE device_id = ? `+where+` ORDER BY id`, deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Command
	for rows.Next() {
		c, err := scanCommand(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func (s *Store) UndeliveredCommands(deviceID string) ([]Command, error) {
	return s.queryCommands("AND delivered_at IS NULL", deviceID)
}

func (s *Store) CommandLog(deviceID string) ([]Command, error) {
	return s.queryCommands("", deviceID)
}

// RecentCommandsForDevice returns the newest <= limit commands for one device
// (delivered or not), newest first. Backs the node page's Recent commands view.
func (s *Store) RecentCommandsForDevice(deviceID string, limit int) ([]Command, error) {
	rows, err := s.db.Query(`SELECT `+cmdCols+`
		FROM command_queue WHERE device_id = ? ORDER BY id DESC LIMIT ?`, deviceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Command
	for rows.Next() {
		c, err := scanCommand(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// LoggedCommand is a command queue row with its device id, for the global
// audit view (the per-device Command lacks device_id).
type LoggedCommand struct {
	Command
	DeviceID string
}

// RecentCommands returns the newest <= limit commands across all devices,
// newest first.
func (s *Store) RecentCommands(limit int) ([]LoggedCommand, error) {
	rows, err := s.db.Query(`SELECT `+cmdCols+`, COALESCE(device_id,'') FROM command_queue ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LoggedCommand
	for rows.Next() {
		var c Command
		var dev string
		if err := rows.Scan(&c.ID, &c.Verb, &c.Args, &c.IssuedAt, &c.IssuedBy, &c.DeliveredAt, &dev); err != nil {
			return nil, err
		}
		out = append(out, LoggedCommand{Command: c, DeviceID: dev})
	}
	return out, rows.Err()
}

func (s *Store) MarkDelivered(id, now int64) error {
	_, err := s.db.Exec(`UPDATE command_queue SET delivered_at = ? WHERE id = ?`, now, id)
	return err
}

// InsertReport appends to the report log and refreshes the node's cached
// observed_state + last_report_at.
func (s *Store) InsertReport(deviceID, observedState, health string, now int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO reports (device_id, ts, observed_state, health) VALUES (?, ?, ?, ?)`,
		deviceID, now, observedState, health); err != nil {
		tx.Rollback()
		return err
	}
	if _, err := tx.Exec(`UPDATE nodes SET observed_state = ?, last_report_at = ? WHERE id = ?`,
		observedState, now, deviceID); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}
