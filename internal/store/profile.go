// Copyright (c) 2026 Ekorau LLC

// internal/store/profile.go — the profile?id= channel backing store: a per-node
// profile session (label/app, porta-side only, last-write-wins) used to correlate
// arriving blobs, plus an append-only profile_result log with a per-node seq.
package store

import (
	"database/sql"
	"errors"
)

type ProfileSession struct {
	DeviceID  string
	App       string
	Label     string
	StartedAt int64
}

type ProfileResult struct {
	ID       int64
	Seq      int64
	DeviceID string
	TS       int64
	App      string
	Label    string
	ByteLen  int64
	Blob     []byte // populated only by GetProfileResult; nil in list views
}

// UpsertProfileSession records the in-flight profile goal for a node. Single
// row per device (last-write-wins) — the correlation source for arriving blobs.
func (s *Store) UpsertProfileSession(deviceID, app, label string, now int64) error {
	_, err := s.db.Exec(
		`INSERT INTO profile_session (device_id, app, label, started_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(device_id) DO UPDATE SET app=excluded.app, label=excluded.label, started_at=excluded.started_at`,
		deviceID, app, label, now)
	return err
}

func (s *Store) GetProfileSession(deviceID string) (*ProfileSession, error) {
	var p ProfileSession
	err := s.db.QueryRow(
		`SELECT device_id, COALESCE(app,''), COALESCE(label,''), COALESCE(started_at,0)
		 FROM profile_session WHERE device_id = ?`, deviceID).
		Scan(&p.DeviceID, &p.App, &p.Label, &p.StartedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// InsertProfileResult appends one result row, assigning the next per-node seq.
func (s *Store) InsertProfileResult(deviceID, app, label string, ts int64, blob []byte) (int64, error) {
	var seq int64
	if err := s.db.QueryRow(
		`SELECT COALESCE(MAX(seq),0)+1 FROM profile_result WHERE device_id = ?`,
		deviceID).Scan(&seq); err != nil {
		return 0, err
	}
	if _, err := s.db.Exec(
		`INSERT INTO profile_result (device_id, seq, ts, app, label, blob, byte_len)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		deviceID, seq, ts, app, label, blob, len(blob)); err != nil {
		return 0, err
	}
	return seq, nil
}

func (s *Store) ProfileResults(deviceID string, afterSeq int64, limit int) ([]ProfileResult, error) {
	q := `SELECT id, seq, ts, COALESCE(app,''), COALESCE(label,''), COALESCE(byte_len,0)
		  FROM profile_result WHERE device_id = ? AND seq > ? ORDER BY seq`
	args := []any{deviceID, afterSeq}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProfileResult
	for rows.Next() {
		var r ProfileResult
		r.DeviceID = deviceID
		if err := rows.Scan(&r.ID, &r.Seq, &r.TS, &r.App, &r.Label, &r.ByteLen); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) GetProfileResult(deviceID string, seq int64) (*ProfileResult, error) {
	var r ProfileResult
	r.DeviceID = deviceID
	err := s.db.QueryRow(
		`SELECT id, seq, ts, COALESCE(app,''), COALESCE(label,''), COALESCE(byte_len,0), blob
		 FROM profile_result WHERE device_id = ? AND seq = ?`, deviceID, seq).
		Scan(&r.ID, &r.Seq, &r.TS, &r.App, &r.Label, &r.ByteLen, &r.Blob)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}
