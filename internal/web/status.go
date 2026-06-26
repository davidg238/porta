// Copyright (c) 2026 Ekorau LLC

package web

import (
	"fmt"
	"net/http"
	"sort"

	"github.com/davidg238/porta/internal/serverstat"
)

// --- Server status page. Renders the same surface as GET /api/status (porta
// build identity + uptime, per-transport volume, report outcomes, sqlite/db
// metrics) as a polled htmx panel, following the telemetry page's structure.

// transportRowVM is one row of the per-transport volume table.
type transportRowVM struct {
	Name    string
	Nodes   int
	Packets int64
	Bytes   string // humanized
}

// tableRowVM is one row of the per-table row-count table.
type tableRowVM struct {
	Name string
	Rows int64
}

// statusVM is the view model for the status page; the polled body re-emits its
// own wrapper element so an outerHTML swap is idempotent.
type statusVM struct {
	Title           string
	Version         string
	Commit          string
	Uptime          string
	Transports      []transportRowVM
	ReportsOK       int64
	ReportsRejected int64
	DBFileBytes     string
	DBWALBytes      string
	DBPageCount     int64
	DBPageSize      int64
	DBFreelist      int64
	SQLiteVersion   string
	Tables          []tableRowVM
	DataLogSpan     string // humanized oldest→newest span, "—" when empty
}

// statusVM assembles the view model from the stats snapshot, the store metrics,
// and per-transport node counts derived from each node's source-address family
// (no schema change — Thread nodes light the thread row on first report). It
// mirrors apisrv.handleStatus so the page and the JSON endpoint never diverge.
func (h *Handler) statusVM() (statusVM, error) {
	var snap serverstat.Snapshot
	if h.stats != nil {
		snap = h.stats.Snapshot()
	}

	m, err := h.st.Metrics()
	if err != nil {
		return statusVM{}, err
	}

	nodeByTransport := map[serverstat.Transport]int{}
	if nodes, err := h.st.ListNodes(); err == nil {
		for _, n := range nodes {
			nodeByTransport[serverstat.TransportOf(n.SourceAddr)]++
		}
	}

	vm := statusVM{
		Title:           "Server Status",
		Version:         orDash(snap.Version),
		Commit:          orDash(snap.Commit),
		Uptime:          humanizeDur(snap.UptimeSeconds),
		ReportsOK:       snap.ReportsOK,
		ReportsRejected: snap.ReportsRejected,
		DBFileBytes:     humanizeBytes(m.FileBytes),
		DBWALBytes:      humanizeBytes(m.WALBytes),
		DBPageCount:     m.PageCount,
		DBPageSize:      m.PageSize,
		DBFreelist:      m.FreelistCount,
		SQLiteVersion:   m.SQLiteVersion,
		DataLogSpan:     dataLogSpan(m.DataLogOldestTS, m.DataLogNewestTS),
	}
	for _, tr := range serverstat.Transports {
		vm.Transports = append(vm.Transports, transportRowVM{
			Name:    string(tr),
			Nodes:   nodeByTransport[tr],
			Packets: snap.Packets[tr],
			Bytes:   humanizeBytes(snap.Bytes[tr]),
		})
	}
	names := make([]string, 0, len(m.TableRows))
	for name := range m.TableRows {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		vm.Tables = append(vm.Tables, tableRowVM{Name: name, Rows: m.TableRows[name]})
	}
	return vm, nil
}

func (h *Handler) handleStatus(w http.ResponseWriter, r *http.Request) {
	vm, err := h.statusVM()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.render(w, "status", vm)
}

func (h *Handler) handleStatusPartial(w http.ResponseWriter, r *http.Request) {
	vm, err := h.statusVM()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.render(w, "status-body", vm)
}

// orDash shows a placeholder for an unstamped build field (e.g. a dev binary
// built without -ldflags), so the page never renders a blank version cell.
func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// dataLogSpan humanizes the data_log retention window. Both 0 means the table
// is empty (the NULL→0 convention in store.Metrics).
func dataLogSpan(oldest, newest int64) string {
	if oldest == 0 && newest == 0 {
		return "—"
	}
	return humanizeDur(newest - oldest)
}

// humanizeBytes renders a byte count as a compact "512 B"/"1.4 KB"/"3.2 MB".
func humanizeBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
