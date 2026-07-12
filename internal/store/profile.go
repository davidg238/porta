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
	DurationS int64 // commanded profile window, seconds (0 = open/continuous)
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
func (s *Store) UpsertProfileSession(deviceID, app, label string, durationS, now int64) error {
	_, err := s.db.Exec(
		`INSERT INTO profile_session (device_id, app, label, started_at, duration_s)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(device_id) DO UPDATE SET app=excluded.app, label=excluded.label,
		   started_at=excluded.started_at, duration_s=excluded.duration_s`,
		deviceID, app, label, now, durationS)
	return err
}

func (s *Store) GetProfileSession(deviceID string) (*ProfileSession, error) {
	var p ProfileSession
	err := s.db.QueryRow(
		`SELECT device_id, COALESCE(app,''), COALESCE(label,''), COALESCE(started_at,0), COALESCE(duration_s,0)
		 FROM profile_session WHERE device_id = ?`, deviceID).
		Scan(&p.DeviceID, &p.App, &p.Label, &p.StartedAt, &p.DurationS)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// LatestProfileResultTS returns the ts of the device's newest profile result,
// or 0 if it has none. Used to tell whether an armed session has been fulfilled.
func (s *Store) LatestProfileResultTS(deviceID string) (int64, error) {
	var ts int64
	err := s.db.QueryRow(
		`SELECT COALESCE(MAX(ts),0) FROM profile_result WHERE device_id = ?`, deviceID).
		Scan(&ts)
	return ts, err
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

// ProfileResultsRecent returns the newest limit rows for the device, ordered
// newest-first (DESC seq). Blob is omitted (list view). limit must be > 0.
func (s *Store) ProfileResultsRecent(deviceID string, limit int) ([]ProfileResult, error) {
	rows, err := s.db.Query(
		`SELECT id, seq, ts, COALESCE(app,''), COALESCE(label,''), COALESCE(byte_len,0)
		 FROM profile_result WHERE device_id = ? ORDER BY seq DESC LIMIT ?`,
		deviceID, limit)
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
