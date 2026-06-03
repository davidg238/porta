package apisrv

import (
	"net/http"
	"strconv"

	"github.com/davidg238/porta/internal/store"
)

// telemetryRow is one row of GET /api/nodes/{sel}/telemetry. It mirrors
// store.DataRow on the wire (so apiclient need not import store). value is the
// typed scalar (number for int/float/bool, null for string & log rows whose
// payload is in text); value_type drives client-side rendering.
type telemetryRow struct {
	ID        int64  `json:"id"`
	TS        int64  `json:"ts"`
	Seq       int64  `json:"seq"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Value     any    `json:"value"`
	Text      string `json:"text"`
	ValueType string `json:"value_type"`
}

// parseOptInt parses an optional integer query param: "" → (def, true); a valid
// NON-NEGATIVE integer → (n, true); a negative or malformed value → (0, false).
func parseOptInt(s string, def int64) (int64, bool) {
	if s == "" {
		return def, true
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// handleTelemetry returns a node's data_log rows. With ?after=<id> it tails by
// the monotonic id cursor (ordered by id); otherwise it returns the ts window
// [since, until] (until<=0 = unbounded). kind filters log|metric; limit caps
// the rows in SQL. The selector is resolved server-side (read-only, no EnsureNode).
func (h *Handler) handleTelemetry(w http.ResponseWriter, r *http.Request) {
	id, okSel := h.resolveSel(w, r.PathValue("sel"))
	if !okSel {
		return
	}
	q := r.URL.Query()
	kind := q.Get("kind")
	limit64, okLim := parseOptInt(q.Get("limit"), 0)
	if !okLim {
		writeErr(w, http.StatusBadRequest, "invalid limit")
		return
	}
	limit := int(limit64)

	var rows []store.DataRow
	var err error
	if q.Has("after") {
		after, okA := parseOptInt(q.Get("after"), 0)
		if !okA {
			writeErr(w, http.StatusBadRequest, "invalid after")
			return
		}
		rows, err = h.st.QueryDataAfter(id, after, kind, limit)
	} else {
		since, okS := parseOptInt(q.Get("since"), 0)
		if !okS {
			writeErr(w, http.StatusBadRequest, "invalid since")
			return
		}
		until, okU := parseOptInt(q.Get("until"), 0)
		if !okU {
			writeErr(w, http.StatusBadRequest, "invalid until")
			return
		}
		rows, err = h.st.QueryDataLimited(id, since, until, kind, limit)
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	out := make([]telemetryRow, 0, len(rows))
	for _, dr := range rows {
		out = append(out, telemetryRow{
			ID: dr.ID, TS: dr.TS, Seq: dr.Seq, Kind: dr.Kind,
			Name: dr.Name, Value: dr.Value, Text: dr.Text, ValueType: dr.ValueType,
		})
	}
	writeOK(w, map[string]any{"rows": out})
}
