// Copyright (c) 2026 Ekorau LLC

// internal/store/debug.go — the debug?id= channel backing store: a per-node
// request queue (drained like commands) + an append-only response log
// (tailed by id like data_log). porta stays a stateless relay; the debug
// session lives in the keeper.
package store

import (
	"database/sql"
	"errors"
)

type DebugRequest struct {
	ID          int64
	Line        string
	DeliveredAt sql.NullInt64
}

type DebugResponse struct {
	ID   int64
	TS   int64
	Seq  int64
	Line string
}

func (s *Store) EnqueueDebugRequest(deviceID, line string, now int64) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO debug_request (device_id, line, issued_at, delivered_at)
		 VALUES (?, ?, ?, NULL)`, deviceID, line, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) NextUndeliveredDebugRequest(deviceID string) (*DebugRequest, error) {
	var r DebugRequest
	err := s.db.QueryRow(
		`SELECT id, line, delivered_at FROM debug_request
		 WHERE device_id = ? AND delivered_at IS NULL ORDER BY id LIMIT 1`,
		deviceID).Scan(&r.ID, &r.Line, &r.DeliveredAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) MarkDebugRequestDelivered(id, now int64) error {
	_, err := s.db.Exec(`UPDATE debug_request SET delivered_at = ? WHERE id = ?`, now, id)
	return err
}

func (s *Store) InsertDebugResponse(deviceID string, ts, seq int64, line string) error {
	_, err := s.db.Exec(
		`INSERT INTO debug_response (device_id, ts, seq, line) VALUES (?, ?, ?, ?)`,
		deviceID, ts, seq, line)
	return err
}

func (s *Store) DebugResponsesAfter(deviceID string, after int64, limit int) ([]DebugResponse, error) {
	q := `SELECT id, ts, seq, COALESCE(line,'') FROM debug_response
		  WHERE device_id = ? AND id > ? ORDER BY id`
	args := []any{deviceID, after}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DebugResponse
	for rows.Next() {
		var r DebugResponse
		if err := rows.Scan(&r.ID, &r.TS, &r.Seq, &r.Line); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
