// Copyright (c) 2026 Ekorau LLC

package apisrv

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/davidg238/porta/internal/serverstat"
)

func TestStatusEndpointReportsTransportsAndDB(t *testing.T) {
	h, st := newTestHandler(t)
	stats := serverstat.New("9.9.9", "abc123", func() int64 { return 0 })
	h.WithStats(stats)

	// One WiFi node (IPv4 source), one Thread node (IPv6 source).
	st.TouchNode("aabbccddeeff", "192.168.0.5:6969", 1000)
	st.TouchNode("ddeeff001122", "[fd77:9957:d9f3:1::9]:6969", 1000)
	stats.Packet(serverstat.WiFi, 120)
	stats.Packet(serverstat.Thread, 80)
	stats.ReportOK()
	stats.ReportRejected()

	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest("GET", "/api/status", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var env struct {
		OK   bool           `json:"ok"`
		Data statusResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	d := env.Data
	if !env.OK || d.Porta.Version != "9.9.9" || d.Porta.Commit != "abc123" {
		t.Errorf("porta block = %+v (ok=%v)", d.Porta, env.OK)
	}
	if d.Transports["wifi"].Nodes != 1 || d.Transports["wifi"].Packets != 1 || d.Transports["wifi"].Bytes != 120 {
		t.Errorf("wifi transport = %+v", d.Transports["wifi"])
	}
	if d.Transports["thread"].Nodes != 1 || d.Transports["thread"].Packets != 1 || d.Transports["thread"].Bytes != 80 {
		t.Errorf("thread transport = %+v", d.Transports["thread"])
	}
	if d.Reports.OK != 1 || d.Reports.Rejected != 1 {
		t.Errorf("reports = %+v", d.Reports)
	}
	if d.DB.SQLiteVersion == "" || d.DB.Tables["nodes"] != 2 {
		t.Errorf("db = %+v", d.DB)
	}
}
