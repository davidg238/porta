// internal/store/data.go
package store

import "strconv"

// DataRow is one row from data_log. Value's runtime type matches the
// declared ValueType:
//   "int"    → int64
//   "float"  → float64 (with the NUMERIC-affinity caveat: a whole-number
//              float stores as INTEGER, so reads back as int64 — the
//              formatter renders by ValueType, putting the decimal back)
//   "bool"   → int64 (0 or 1)
//   "string" → Value == nil; Text holds the payload
//   ""       → log row (Value == nil; Text holds the line)
type DataRow struct {
	TS        int64
	Seq       int64
	Kind      string
	Name      string
	Value     any
	Text      string
	ValueType string
}

// InsertData appends one telemetry entry. value's runtime type drives the
// SQL binding: int64 → INTEGER, float64 → REAL, nil → NULL. Empty strings
// for name / text / valueType are bound as NULL.
func (s *Store) InsertData(deviceID string, ts, seq int64, kind, name string, value any, text, valueType string) error {
	_, err := s.db.Exec(
		`INSERT INTO data_log (device_id, ts, seq, kind, name, value, text, value_type)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		deviceID, ts, seq, kind,
		nullStr(name), value, nullStr(text), nullStr(valueType),
	)
	return err
}

// QueryData returns the device's rows with since <= ts <= until, ordered by
// (ts, seq). When kind is non-empty, restricts to that kind. value_type ==
// "" surfaces as the empty string (a log row or a degraded metric).
//
// The value column is NUMERIC; Scan into *any returns the SQLite storage
// class directly: INTEGER → int64, REAL → float64, NULL → nil. The driver
// can also return []byte for some edge cases (e.g. a numeric out of int64
// range stored textually) — normalizeNumeric handles that fallback.
func (s *Store) QueryData(deviceID string, since, until int64, kind string) ([]DataRow, error) {
	q := `SELECT ts, seq, COALESCE(kind,''), COALESCE(name,''), value, COALESCE(text,''), COALESCE(value_type,'')
		  FROM data_log WHERE device_id = ? AND ts >= ? AND ts <= ?`
	args := []any{deviceID, since, until}
	if kind != "" {
		q += ` AND kind = ?`
		args = append(args, kind)
	}
	q += ` ORDER BY ts, seq`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DataRow
	for rows.Next() {
		var r DataRow
		var v any
		if err := rows.Scan(&r.TS, &r.Seq, &r.Kind, &r.Name, &v, &r.Text, &r.ValueType); err != nil {
			return nil, err
		}
		r.Value = normalizeNumeric(v)
		out = append(out, r)
	}
	return out, rows.Err()
}

// normalizeNumeric coerces the result of Scan(*any) on a NUMERIC column.
// go-sqlite3 returns int64 / float64 / nil for the common cases; []byte is
// possible for textually-stored numerics (rare here since our binds are
// always int64/float64/nil) and gets reparsed.
func normalizeNumeric(v any) any {
	switch x := v.(type) {
	case nil, int64, float64:
		return x
	case []byte:
		s := string(x)
		if s == "" {
			return nil
		}
		for i := 0; i < len(s); i++ {
			c := s[i]
			if c == '.' || c == 'e' || c == 'E' {
				if f, err := strconv.ParseFloat(s, 64); err == nil {
					return f
				}
				return nil
			}
		}
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n
		}
		return nil
	default:
		return v
	}
}

// PruneData deletes data_log rows with ts < cutoff (epoch seconds).
func (s *Store) PruneData(cutoff int64) error {
	_, err := s.db.Exec(`DELETE FROM data_log WHERE ts < ?`, cutoff)
	return err
}
