// Copyright (c) 2026 Ekorau LLC

package handler

import (
	"strings"
	"testing"

	"github.com/davidg238/porta/internal/serverstat"
)

func TestWriteRecordsReportOutcomeAndLogsRejection(t *testing.T) {
	h, _ := newH(t)
	st := serverstat.New("t", "t", func() int64 { return 0 })
	h.SetStats(st)
	var logs []string
	h.SetLog(func(f string, a ...any) { logs = append(logs, f) })

	// A good report → ReportOK, no rejection log.
	good := []byte(`{"apps":{},"config":{},"health":{}}`)
	if err := h.Write("report?id=dev", "1.2.3.4:5", good); err != nil {
		t.Fatalf("good report: %v", err)
	}

	// A doubled-JSON report (the original bug) → ReportRejected + a log line.
	bad := []byte(`{"apps":{}}{"apps":{}}`)
	if err := h.Write("report?id=dev", "1.2.3.4:5", bad); err == nil {
		t.Fatal("expected bad report to be rejected")
	}

	snap := st.Snapshot()
	if snap.ReportsOK != 1 || snap.ReportsRejected != 1 {
		t.Errorf("counters ok=%d rejected=%d, want 1/1", snap.ReportsOK, snap.ReportsRejected)
	}
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "rejected") {
		t.Errorf("expected a rejection log line, got: %q", joined)
	}
}
