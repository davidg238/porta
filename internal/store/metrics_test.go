// Copyright (c) 2026 Ekorau LLC

package store

import "testing"

func TestMetricsReportsSizeTablesAndDataLogSpan(t *testing.T) {
	st := openTmp(t)

	// Seed a node + a few data_log rows with known timestamps.
	if err := st.TouchNode("aabbccddeeff", "192.168.0.5:6969", 1000); err != nil {
		t.Fatal(err)
	}
	for _, ts := range []int64{1100, 1200, 1300} {
		if err := st.InsertData("aabbccddeeff", ts, 0, "metric", "pm25", 1.5, "", "float", ""); err != nil {
			t.Fatal(err)
		}
	}

	m, err := st.Metrics()
	if err != nil {
		t.Fatalf("Metrics: %v", err)
	}

	if m.FileBytes <= 0 {
		t.Errorf("FileBytes = %d, want > 0", m.FileBytes)
	}
	if m.PageSize <= 0 || m.PageCount <= 0 {
		t.Errorf("page stats: size=%d count=%d, want > 0", m.PageSize, m.PageCount)
	}
	if m.SQLiteVersion == "" {
		t.Error("SQLiteVersion empty")
	}
	if m.TableRows["data_log"] != 3 {
		t.Errorf("data_log rows = %d, want 3", m.TableRows["data_log"])
	}
	if m.TableRows["nodes"] != 1 {
		t.Errorf("nodes rows = %d, want 1", m.TableRows["nodes"])
	}
	if m.DataLogOldestTS != 1100 || m.DataLogNewestTS != 1300 {
		t.Errorf("data_log span = [%d,%d], want [1100,1300]", m.DataLogOldestTS, m.DataLogNewestTS)
	}
}

func TestMetricsEmptyDataLogSpanIsZero(t *testing.T) {
	st := openTmp(t)
	m, err := st.Metrics()
	if err != nil {
		t.Fatalf("Metrics: %v", err)
	}
	if m.DataLogOldestTS != 0 || m.DataLogNewestTS != 0 {
		t.Errorf("empty span = [%d,%d], want [0,0]", m.DataLogOldestTS, m.DataLogNewestTS)
	}
	if _, ok := m.TableRows["data_log"]; !ok {
		t.Error("data_log should appear in TableRows even when empty")
	}
}
