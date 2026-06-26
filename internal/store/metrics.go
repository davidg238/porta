// Copyright (c) 2026 Ekorau LLC

// internal/store/metrics.go — sqlite/db metrics for the status surface.
package store

import (
	"database/sql"
	"os"
)

// Metrics is a point-in-time view of the sqlite store: on-disk size, page-level
// usage (for VACUUM/bloat decisions), per-table row counts, and the data_log
// time span (the unbounded grower — telemetry + panics — so its count and span
// are the retention signal).
type Metrics struct {
	Path            string           // db file path
	FileBytes       int64            // db + -wal + -shm on disk
	WALBytes        int64            // -wal alone (uncheckpointed pages)
	PageCount       int64            // PRAGMA page_count
	PageSize        int64            // PRAGMA page_size
	FreelistCount   int64            // PRAGMA freelist_count (free pages → reclaimable by VACUUM)
	SQLiteVersion   string           // sqlite_version()
	TableRows       map[string]int64 // row count per user table
	DataLogOldestTS int64            // min(ts) in data_log, 0 if empty
	DataLogNewestTS int64            // max(ts) in data_log, 0 if empty
}

// Metrics gathers the current store metrics.
func (s *Store) Metrics() (Metrics, error) {
	m := Metrics{Path: s.path, TableRows: map[string]int64{}}

	// On-disk size: the db plus its WAL/SHM sidecars (present in WAL mode).
	if fi, err := os.Stat(s.path); err == nil {
		m.FileBytes += fi.Size()
	}
	if fi, err := os.Stat(s.path + "-wal"); err == nil {
		m.WALBytes = fi.Size()
		m.FileBytes += fi.Size()
	}
	if fi, err := os.Stat(s.path + "-shm"); err == nil {
		m.FileBytes += fi.Size()
	}

	// Page-level usage + sqlite version (single-value PRAGMA/func reads).
	for _, q := range []struct {
		sql string
		dst *int64
	}{
		{"PRAGMA page_count", &m.PageCount},
		{"PRAGMA page_size", &m.PageSize},
		{"PRAGMA freelist_count", &m.FreelistCount},
	} {
		if err := s.db.QueryRow(q.sql).Scan(q.dst); err != nil {
			return m, err
		}
	}
	if err := s.db.QueryRow("SELECT sqlite_version()").Scan(&m.SQLiteVersion); err != nil {
		return m, err
	}

	// Per-table row counts (every user table, so new tables appear automatically).
	rows, err := s.db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return m, err
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return m, err
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return m, err
	}
	rows.Close()
	for _, t := range tables {
		var n int64
		// Table names come from sqlite_master, not user input — safe to interpolate.
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM "` + t + `"`).Scan(&n); err != nil {
			return m, err
		}
		m.TableRows[t] = n
	}

	// data_log time span — the retention signal (NULL when empty → 0).
	var oldest, newest sql.NullInt64
	if err := s.db.QueryRow("SELECT MIN(ts), MAX(ts) FROM data_log").Scan(&oldest, &newest); err != nil {
		return m, err
	}
	m.DataLogOldestTS = oldest.Int64
	m.DataLogNewestTS = newest.Int64

	return m, nil
}
