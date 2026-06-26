// Copyright (c) 2026 Ekorau LLC

// internal/apisrv/status.go — GET /api/status: porta self-stats + per-transport
// volume + sqlite/db metrics, for the operator status page.
package apisrv

import (
	"net/http"

	"github.com/davidg238/porta/internal/serverstat"
)

type transportStat struct {
	Nodes   int   `json:"nodes"`
	Packets int64 `json:"packets"`
	Bytes   int64 `json:"bytes"`
}

type dbStatus struct {
	FileBytes     int64            `json:"file_bytes"`
	WALBytes      int64            `json:"wal_bytes"`
	PageCount     int64            `json:"page_count"`
	PageSize      int64            `json:"page_size"`
	FreelistCount int64            `json:"freelist_count"`
	SQLiteVersion string           `json:"sqlite_version"`
	Tables        map[string]int64 `json:"tables"`
	DataLogSpan   [2]int64         `json:"data_log_span"` // [oldest_ts, newest_ts]
}

type statusResponse struct {
	Porta struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
		UptimeS int64  `json:"uptime_s"`
	} `json:"porta"`
	Transports map[string]transportStat `json:"transports"`
	Reports    struct {
		OK       int64 `json:"ok"`
		Rejected int64 `json:"rejected"`
	} `json:"reports"`
	DB dbStatus `json:"db"`
}

// handleStatus assembles the status envelope from the stats snapshot, the store
// metrics, and per-transport node counts derived from each node's source-address
// family (no schema change — Thread nodes light up the thread row on first report).
func (h *Handler) handleStatus(w http.ResponseWriter, _ *http.Request) {
	var snap serverstat.Snapshot
	if h.stats != nil {
		snap = h.stats.Snapshot()
	}

	m, err := h.st.Metrics()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "metrics: "+err.Error())
		return
	}

	nodeByTransport := map[serverstat.Transport]int{}
	if nodes, err := h.st.ListNodes(); err == nil {
		for _, n := range nodes {
			nodeByTransport[serverstat.TransportOf(n.SourceAddr)]++
		}
	}

	var resp statusResponse
	resp.Porta.Version = snap.Version
	resp.Porta.Commit = snap.Commit
	resp.Porta.UptimeS = snap.UptimeSeconds
	resp.Transports = map[string]transportStat{}
	for _, t := range serverstat.Transports {
		resp.Transports[string(t)] = transportStat{
			Nodes:   nodeByTransport[t],
			Packets: snap.Packets[t],
			Bytes:   snap.Bytes[t],
		}
	}
	resp.Reports.OK = snap.ReportsOK
	resp.Reports.Rejected = snap.ReportsRejected
	resp.DB = dbStatus{
		FileBytes:     m.FileBytes,
		WALBytes:      m.WALBytes,
		PageCount:     m.PageCount,
		PageSize:      m.PageSize,
		FreelistCount: m.FreelistCount,
		SQLiteVersion: m.SQLiteVersion,
		Tables:        m.TableRows,
		DataLogSpan:   [2]int64{m.DataLogOldestTS, m.DataLogNewestTS},
	}
	writeOK(w, resp)
}
