// internal/store/data_test.go
package store

import (
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(t.TempDir() + "/d.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestInsertAndQueryDataAllScalarTypes(t *testing.T) {
	st := openTestStore(t)
	dev := "aabbccddeeff"
	// Int.
	if err := st.InsertData(dev, 100, 0, "metric", "pm", int64(13), "", "int"); err != nil {
		t.Fatal(err)
	}
	// Float.
	if err := st.InsertData(dev, 101, 1, "metric", "t", float64(20.5), "", "float"); err != nil {
		t.Fatal(err)
	}
	// Bool (stored as 0/1 in value, type tag "bool").
	if err := st.InsertData(dev, 102, 2, "metric", "door", int64(1), "", "bool"); err != nil {
		t.Fatal(err)
	}
	// String (value=nil, text holds payload).
	if err := st.InsertData(dev, 103, 3, "metric", "mode", nil, "auto", "string"); err != nil {
		t.Fatal(err)
	}
	// Log (value=nil, text holds payload, value_type "").
	if err := st.InsertData(dev, 104, 4, "log", "", nil, "started blink", ""); err != nil {
		t.Fatal(err)
	}
	rows, err := st.QueryData(dev, 0, 200, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 5 {
		t.Fatalf("got %d rows, want 5", len(rows))
	}
	if v, ok := rows[0].Value.(int64); !ok || v != 13 {
		t.Errorf("rows[0].Value = %v (%T), want int64(13)", rows[0].Value, rows[0].Value)
	}
	if rows[0].ValueType != "int" {
		t.Errorf("rows[0].ValueType = %q, want int", rows[0].ValueType)
	}
	if v, ok := rows[1].Value.(float64); !ok || v != 20.5 {
		t.Errorf("rows[1].Value = %v (%T), want float64(20.5)", rows[1].Value, rows[1].Value)
	}
	if rows[1].ValueType != "float" {
		t.Errorf("rows[1].ValueType = %q, want float", rows[1].ValueType)
	}
	if v, ok := rows[2].Value.(int64); !ok || v != 1 {
		t.Errorf("rows[2].Value = %v (%T), want int64(1)", rows[2].Value, rows[2].Value)
	}
	if rows[2].ValueType != "bool" {
		t.Errorf("rows[2].ValueType = %q, want bool", rows[2].ValueType)
	}
	if rows[3].Text != "auto" {
		t.Errorf("rows[3].Text = %q, want auto", rows[3].Text)
	}
	if rows[3].Value != nil {
		t.Errorf("rows[3].Value = %v, want nil", rows[3].Value)
	}
	if rows[3].ValueType != "string" {
		t.Errorf("rows[3].ValueType = %q, want string", rows[3].ValueType)
	}
	if rows[4].Kind != "log" {
		t.Errorf("rows[4].Kind = %q, want log", rows[4].Kind)
	}
	if rows[4].Text != "started blink" {
		t.Errorf("rows[4].Text = %q, want started blink", rows[4].Text)
	}
	if rows[4].ValueType != "" {
		t.Errorf("rows[4].ValueType = %q, want \"\"", rows[4].ValueType)
	}
}

// TestNormalizeNumericByteSlice locks the contract of the []byte fallback
// branch — the path a textual-numeric column read (e.g. a value out of
// int64 range stored as text) takes. Our binds are always int64/float64/nil
// so the driver doesn't hit it today; this pins the coercion regardless.
func TestNormalizeNumericByteSlice(t *testing.T) {
	cases := []struct {
		in   []byte
		want any
	}{
		{[]byte("12345"), int64(12345)},
		{[]byte("-7"), int64(-7)},
		{[]byte("12.5"), float64(12.5)},
		{[]byte("1e3"), float64(1000)},
		{[]byte("2.5E-1"), float64(0.25)},
		{[]byte(""), nil},      // empty → nil
		{[]byte("abc"), nil},   // non-numeric → nil
		{[]byte("1.2.3"), nil}, // has '.', but ParseFloat fails → nil
		{[]byte("99x"), nil},   // ParseInt fails → nil
	}
	for _, c := range cases {
		got := normalizeNumeric(c.in)
		if got != c.want {
			t.Errorf("normalizeNumeric(%q) = %v (%T), want %v (%T)",
				c.in, got, got, c.want, c.want)
		}
	}
	// Pass-through cases: nil / int64 / float64 are returned unchanged.
	if got := normalizeNumeric(nil); got != nil {
		t.Errorf("normalizeNumeric(nil) = %v, want nil", got)
	}
	if got := normalizeNumeric(int64(9)); got != int64(9) {
		t.Errorf("normalizeNumeric(int64(9)) = %v, want int64(9)", got)
	}
}

func TestQueryDataKindFilter(t *testing.T) {
	st := openTestStore(t)
	dev := "ffeeddccbbaa"
	st.InsertData(dev, 100, 0, "metric", "x", int64(1), "", "int")
	st.InsertData(dev, 101, 1, "log", "", nil, "hi", "")
	st.InsertData(dev, 102, 2, "metric", "y", int64(2), "", "int")
	if rows, _ := st.QueryData(dev, 0, 200, "metric"); len(rows) != 2 {
		t.Errorf("metric filter: got %d rows, want 2", len(rows))
	}
	if rows, _ := st.QueryData(dev, 0, 200, "log"); len(rows) != 1 {
		t.Errorf("log filter: got %d rows, want 1", len(rows))
	}
}

func TestQueryDataTimeWindow(t *testing.T) {
	st := openTestStore(t)
	dev := "112233445566"
	st.InsertData(dev, 100, 0, "metric", "x", int64(1), "", "int")
	st.InsertData(dev, 200, 1, "metric", "x", int64(2), "", "int")
	st.InsertData(dev, 300, 2, "metric", "x", int64(3), "", "int")
	if rows, _ := st.QueryData(dev, 150, 250, ""); len(rows) != 1 {
		t.Errorf("window 150..250: got %d rows, want 1", len(rows))
	}
	if rows, _ := st.QueryData(dev, 400, 500, ""); len(rows) != 0 {
		t.Errorf("window 400..500: got %d rows, want 0", len(rows))
	}
}

// TestQueryDataUntilZeroUnbounded pins the #10 contract: until <= 0 means
// "no upper bound" (a since-only query), not "ts <= 0" which would always be
// empty. A since-only telemetry query must return everything from `since` on.
func TestQueryDataUntilZeroUnbounded(t *testing.T) {
	st := openTestStore(t)
	dev := "aa00bb11cc22"
	st.InsertData(dev, 100, 0, "metric", "x", int64(1), "", "int")
	st.InsertData(dev, 200, 1, "metric", "x", int64(2), "", "int")
	st.InsertData(dev, 300, 2, "metric", "x", int64(3), "", "int")
	// since=150, until=0 → rows with ts>=150 (200, 300).
	rows, err := st.QueryData(dev, 150, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("since-only (until=0): got %d rows, want 2", len(rows))
	}
	if rows[0].TS != 200 || rows[1].TS != 300 {
		t.Errorf("got ts %d,%d; want 200,300", rows[0].TS, rows[1].TS)
	}
	// until=0 still honors the kind filter.
	if r, _ := st.QueryData(dev, 0, 0, "metric"); len(r) != 3 {
		t.Errorf("since=0,until=0,kind=metric: got %d, want 3", len(r))
	}
}

func TestPruneData(t *testing.T) {
	st := openTestStore(t)
	dev := "778899aabbcc"
	st.InsertData(dev, 100, 0, "metric", "x", int64(1), "", "int")
	st.InsertData(dev, 200, 1, "metric", "x", int64(2), "", "int")
	st.InsertData(dev, 300, 2, "metric", "x", int64(3), "", "int")
	if err := st.PruneData(200); err != nil {
		t.Fatal(err)
	}
	rows, _ := st.QueryData(dev, 0, 400, "")
	if len(rows) != 2 {
		t.Errorf("after prune: got %d rows, want 2 (ts<200 removed)", len(rows))
	}
}

// TestRecentDataReturnsNewestFirstLimited verifies RecentData returns at most
// limit rows, newest (highest ts) first.
func TestRecentDataReturnsNewestFirstLimited(t *testing.T) {
	st := openTestStore(t)
	dev := "n1"
	for i := int64(1); i <= 5; i++ {
		if err := st.InsertData(dev, 100+i, i, "metric", "pm25", int64(i), "", "int"); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := st.RecentData(dev, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	if rows[0].TS != 105 {
		t.Errorf("rows[0].TS = %d, want 105 (newest first)", rows[0].TS)
	}
}

// TestNumericAffinityWholeNumberFloat pins the SQLite quirk preserved end-to-
// end: a float64(13.0) bound to a NUMERIC column is stored as INTEGER (13);
// QueryData reads it back as int64(13). value_type stays "float", so the
// FormatLine renderer (Task 3) puts the decimal point back.
func TestNumericAffinityWholeNumberFloat(t *testing.T) {
	st := openTestStore(t)
	dev := "ddccbbaa9988"
	if err := st.InsertData(dev, 10, 0, "metric", "w", float64(13.0), "", "float"); err != nil {
		t.Fatal(err)
	}
	rows, _ := st.QueryData(dev, 0, 100, "")
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].ValueType != "float" {
		t.Errorf("ValueType = %q, want float", rows[0].ValueType)
	}
	if v, ok := rows[0].Value.(int64); !ok || v != 13 {
		t.Errorf("Value = %v (%T), want int64(13) (NUMERIC affinity gotcha)", rows[0].Value, rows[0].Value)
	}
}

func TestRecentMetricsFiltersAndOrders(t *testing.T) {
	st := openTestStore(t)
	// Two metric rows + one log row, two devices.
	st.InsertData("devA", 100, 1, "metric", "pm25", int64(7), "", "int")
	st.InsertData("devA", 100, 0, "log", "", nil, "vin: pm25=7", "")
	st.InsertData("devB", 200, 1, "metric", "temp", int64(21), "", "int")

	all, err := st.RecentMetrics("", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("len = %d, want 2 (log row excluded)", len(all))
	}
	if all[0].TS != 200 || all[0].DeviceID != "devB" {
		t.Errorf("not newest-first or device id missing: %+v", all[0])
	}

	just, err := st.RecentMetrics("devA", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(just) != 1 || just[0].DeviceID != "devA" || just[0].Name != "pm25" {
		t.Errorf("device filter wrong: %+v", just)
	}
}
