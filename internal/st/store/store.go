package store

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Device represents a Thread node known to the gateway.
type Device struct {
	EUI64      string
	Name       string
	SourceAddr string
	Role       string
	RLOC16     int
	State      string
	FirstSeen  time.Time
	LastSeen   time.Time
}

// Command is a queued verb+payload destined for a specific device.
type Command struct {
	ID      int64
	Verb    string
	Payload []byte
}

// DataRow is one logged data sample from a device.
type DataRow struct {
	ID        int64
	EUI64     string
	Timestamp time.Time
	Payload   []byte
}

// DebugBreakpoint is a persisted breakpoint for a device.
type DebugBreakpoint struct {
	DeviceID string
	Module   string
	STLine   int
	PCStart  int
	PCEnd    int
}

// DebugState is the last known debug state of a device.
type DebugState struct {
	DeviceID        string
	Status          string // "running" or "paused"
	PauseReason     string
	CurrentPC       int
	CurrentFunction string
	CurrentModule   string
	CurrentSTLine   int
	UpdatedAt       time.Time
}

// Store wraps the SQLite database.
type Store struct {
	db *sql.DB
}

// Open creates or opens a SQLite database at path and initialises the schema.
func Open(path string) (*Store, error) {
	dsn := path
	if path != ":memory:" {
		dsn = path + "?_journal_mode=WAL"
	}
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("store open: %w", err)
	}
	if path == ":memory:" {
		// Enable WAL via pragma for in-memory databases.
		_, _ = db.Exec("PRAGMA journal_mode=WAL")
	}
	s := &Store{db: db}
	if err := s.initSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("store init schema: %w", err)
	}
	return s, nil
}

// Close closes the underlying database.
func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) initSchema() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS devices (
			eui64       TEXT PRIMARY KEY,
			name        TEXT DEFAULT '',
			source_addr TEXT DEFAULT '',
			role        TEXT DEFAULT '',
			rloc16      INTEGER DEFAULT 0,
			first_seen  TEXT,
			last_seen   TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS command_queue (
			id        INTEGER PRIMARY KEY,
			eui64     TEXT,
			verb      TEXT,
			payload   BLOB,
			queued_at TEXT,
			sent_at   TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS data_log (
			id        INTEGER PRIMARY KEY,
			eui64     TEXT,
			timestamp TEXT,
			payload   BLOB
		)`,
		`CREATE INDEX IF NOT EXISTS idx_cmd_eui64_sent ON command_queue(eui64, sent_at)`,
		`CREATE INDEX IF NOT EXISTS idx_data_eui64_ts  ON data_log(eui64, timestamp)`,
		`CREATE TABLE IF NOT EXISTS debug_breakpoints (
			device_id   TEXT NOT NULL,
			module_name TEXT NOT NULL,
			st_line     INTEGER NOT NULL,
			pc_start    INTEGER NOT NULL,
			pc_end      INTEGER NOT NULL,
			PRIMARY KEY (device_id, module_name, st_line)
		)`,
		`CREATE TABLE IF NOT EXISTS debug_state (
			device_id        TEXT PRIMARY KEY,
			status           TEXT NOT NULL DEFAULT 'running',
			pause_reason     TEXT DEFAULT '',
			current_pc       INTEGER DEFAULT 0,
			current_function TEXT DEFAULT '',
			current_module   TEXT DEFAULT '',
			current_st_line  INTEGER DEFAULT 0,
			updated_at       TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS debug_commands (
			id        INTEGER PRIMARY KEY,
			device_id TEXT NOT NULL,
			command   TEXT NOT NULL,
			queued_at TEXT NOT NULL,
			sent_at   TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_dbgcmd_device ON debug_commands(device_id, sent_at)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("exec %q: %w", stmt[:40], err)
		}
	}
	// Add state column (may already exist).
	s.db.Exec(`ALTER TABLE devices ADD COLUMN state TEXT DEFAULT 'active'`)
	return nil
}

// DeviceSeen records or updates a device in the registry.
func (s *Store) DeviceSeen(eui64, sourceAddr, role string, rloc16 int) error {
	now := time.Now().Format(time.RFC3339Nano)
	_, err := s.db.Exec(`
		INSERT INTO devices (eui64, source_addr, role, rloc16, first_seen, last_seen)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(eui64) DO UPDATE SET
			state       = 'active',
			source_addr = excluded.source_addr,
			role        = excluded.role,
			rloc16      = excluded.rloc16,
			last_seen   = excluded.last_seen
	`, eui64, sourceAddr, role, rloc16, now, now)
	return err
}

// SetDeviceName assigns a human-readable name to a device.
func (s *Store) SetDeviceName(eui64, name string) error {
	res, err := s.db.Exec(`UPDATE devices SET name = ? WHERE eui64 = ?`, name, eui64)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("device %q not found", eui64)
	}
	return nil
}

// ResolveDevice returns the EUI-64 for a device identified by EUI-64 or name.
func (s *Store) ResolveDevice(nameOrEUI string) (string, error) {
	var eui64 string
	err := s.db.QueryRow(
		`SELECT eui64 FROM devices WHERE eui64 = ? OR name = ?`,
		nameOrEUI, nameOrEUI,
	).Scan(&eui64)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("device %q not found", nameOrEUI)
	}
	return eui64, err
}

// ListDevices returns all known devices ordered by most recently seen.
func (s *Store) ListDevices() ([]Device, error) {
	rows, err := s.db.Query(`SELECT eui64, name, source_addr, role, rloc16, COALESCE(state, 'active'), first_seen, last_seen FROM devices ORDER BY last_seen DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var devs []Device
	for rows.Next() {
		var d Device
		var firstSeen, lastSeen string
		if err := rows.Scan(&d.EUI64, &d.Name, &d.SourceAddr, &d.Role, &d.RLOC16, &d.State, &firstSeen, &lastSeen); err != nil {
			return nil, err
		}
		d.FirstSeen, _ = time.Parse(time.RFC3339Nano, firstSeen)
		d.LastSeen, _ = time.Parse(time.RFC3339Nano, lastSeen)
		devs = append(devs, d)
	}
	return devs, rows.Err()
}

// QueueCommand enqueues a command for a device.
func (s *Store) QueueCommand(eui64, verb string, payload []byte) error {
	now := time.Now().Format(time.RFC3339Nano)
	_, err := s.db.Exec(
		`INSERT INTO command_queue (eui64, verb, payload, queued_at) VALUES (?, ?, ?, ?)`,
		eui64, verb, payload, now,
	)
	return err
}

// PopCommand returns and marks the oldest unsent command for a device, or nil if none.
func (s *Store) PopCommand(eui64 string) (*Command, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var cmd Command
	err = tx.QueryRow(
		`SELECT id, verb, payload FROM command_queue WHERE eui64 = ? AND sent_at IS NULL ORDER BY id ASC LIMIT 1`,
		eui64,
	).Scan(&cmd.ID, &cmd.Verb, &cmd.Payload)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	now := time.Now().Format(time.RFC3339Nano)
	if _, err := tx.Exec(`UPDATE command_queue SET sent_at = ? WHERE id = ?`, now, cmd.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &cmd, nil
}

// LogData records a data sample from a device.
func (s *Store) LogData(eui64 string, payload []byte) error {
	now := time.Now().Format(time.RFC3339Nano)
	_, err := s.db.Exec(
		`INSERT INTO data_log (eui64, timestamp, payload) VALUES (?, ?, ?)`,
		eui64, now, payload,
	)
	return err
}

// QueryData returns logged data for a device within a time range.
func (s *Store) QueryData(eui64 string, since, until time.Time) ([]DataRow, error) {
	rows, err := s.db.Query(
		`SELECT id, eui64, timestamp, payload FROM data_log WHERE eui64 = ? AND timestamp >= ? AND timestamp <= ? ORDER BY timestamp ASC`,
		eui64, since.Format(time.RFC3339Nano), until.Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DataRow
	for rows.Next() {
		var r DataRow
		var ts string
		if err := rows.Scan(&r.ID, &r.EUI64, &ts, &r.Payload); err != nil {
			return nil, err
		}
		r.Timestamp, _ = time.Parse(time.RFC3339Nano, ts)
		out = append(out, r)
	}
	return out, rows.Err()
}

// PruneCommands deletes command_queue rows that have been sent and are older than maxAge.
func (s *Store) PruneCommands(maxAge time.Duration) (int64, error) {
	cutoff := time.Now().Add(-maxAge).Format(time.RFC3339Nano)
	res, err := s.db.Exec(`DELETE FROM command_queue WHERE sent_at IS NOT NULL AND sent_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PruneData deletes data_log rows older than maxAge. Returns count of deleted rows.
func (s *Store) PruneData(maxAge time.Duration) (int64, error) {
	cutoff := time.Now().Add(-maxAge).Format(time.RFC3339Nano)
	res, err := s.db.Exec(`DELETE FROM data_log WHERE timestamp < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// AddDevice registers a device as "pending" (known but not yet on mesh).
func (s *Store) AddDevice(eui64 string) error {
	now := time.Now().Format(time.RFC3339Nano)
	_, err := s.db.Exec(`
		INSERT INTO devices (eui64, state, first_seen, last_seen)
		VALUES (?, 'pending', ?, ?)
		ON CONFLICT(eui64) DO NOTHING
	`, eui64, now, now)
	return err
}

// ListPendingDevices returns devices in "pending" state.
func (s *Store) ListPendingDevices() ([]Device, error) {
	rows, err := s.db.Query(`SELECT eui64, name FROM devices WHERE state = 'pending'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var devs []Device
	for rows.Next() {
		var d Device
		if err := rows.Scan(&d.EUI64, &d.Name); err != nil {
			return nil, err
		}
		devs = append(devs, d)
	}
	return devs, rows.Err()
}

// SetDebugBreakpoint adds or updates a breakpoint for a device.
func (s *Store) SetDebugBreakpoint(deviceID, module string, stLine, pcStart, pcEnd int) error {
	_, err := s.db.Exec(`
		INSERT INTO debug_breakpoints (device_id, module_name, st_line, pc_start, pc_end)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(device_id, module_name, st_line) DO UPDATE SET
			pc_start = excluded.pc_start,
			pc_end   = excluded.pc_end
	`, deviceID, module, stLine, pcStart, pcEnd)
	return err
}

// ClearDebugBreakpoint removes a breakpoint.
func (s *Store) ClearDebugBreakpoint(deviceID, module string, stLine int) error {
	_, err := s.db.Exec(
		`DELETE FROM debug_breakpoints WHERE device_id = ? AND module_name = ? AND st_line = ?`,
		deviceID, module, stLine,
	)
	return err
}

// ListDebugBreakpoints returns all breakpoints for a device.
func (s *Store) ListDebugBreakpoints(deviceID string) ([]DebugBreakpoint, error) {
	rows, err := s.db.Query(
		`SELECT device_id, module_name, st_line, pc_start, pc_end FROM debug_breakpoints WHERE device_id = ?`,
		deviceID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var bps []DebugBreakpoint
	for rows.Next() {
		var bp DebugBreakpoint
		if err := rows.Scan(&bp.DeviceID, &bp.Module, &bp.STLine, &bp.PCStart, &bp.PCEnd); err != nil {
			return nil, err
		}
		bps = append(bps, bp)
	}
	return bps, rows.Err()
}

// UpdateDebugState records the current debug state of a device.
func (s *Store) UpdateDebugState(deviceID, status, reason string, pc int, function, module string, stLine int) error {
	now := time.Now().Format(time.RFC3339Nano)
	_, err := s.db.Exec(`
		INSERT INTO debug_state (device_id, status, pause_reason, current_pc, current_function, current_module, current_st_line, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(device_id) DO UPDATE SET
			status           = excluded.status,
			pause_reason     = excluded.pause_reason,
			current_pc       = excluded.current_pc,
			current_function = excluded.current_function,
			current_module   = excluded.current_module,
			current_st_line  = excluded.current_st_line,
			updated_at       = excluded.updated_at
	`, deviceID, status, reason, pc, function, module, stLine, now)
	return err
}

// GetDebugState returns the current debug state for a device.
func (s *Store) GetDebugState(deviceID string) (*DebugState, error) {
	var ds DebugState
	var updatedAt string
	err := s.db.QueryRow(
		`SELECT device_id, status, pause_reason, current_pc, current_function, current_module, current_st_line, updated_at
		 FROM debug_state WHERE device_id = ?`, deviceID,
	).Scan(&ds.DeviceID, &ds.Status, &ds.PauseReason, &ds.CurrentPC,
		&ds.CurrentFunction, &ds.CurrentModule, &ds.CurrentSTLine, &updatedAt)
	if err != nil {
		return nil, err
	}
	ds.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return &ds, nil
}

// QueueDebugCommand queues a debug command for a device.
func (s *Store) QueueDebugCommand(deviceID, command string) error {
	now := time.Now().Format(time.RFC3339Nano)
	_, err := s.db.Exec(
		`INSERT INTO debug_commands (device_id, command, queued_at) VALUES (?, ?, ?)`,
		deviceID, command, now,
	)
	return err
}

// PopDebugCommand returns and marks the oldest unsent debug command, or "" if none.
func (s *Store) PopDebugCommand(deviceID string) (string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	var id int64
	var cmd string
	err = tx.QueryRow(
		`SELECT id, command FROM debug_commands WHERE device_id = ? AND sent_at IS NULL ORDER BY id ASC LIMIT 1`,
		deviceID,
	).Scan(&id, &cmd)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}

	now := time.Now().Format(time.RFC3339Nano)
	if _, err := tx.Exec(`UPDATE debug_commands SET sent_at = ? WHERE id = ?`, now, id); err != nil {
		return "", err
	}
	return cmd, tx.Commit()
}
