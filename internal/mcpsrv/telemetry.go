package mcpsrv

import (
	"context"
	"fmt"

	"github.com/davidg238/porta/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// QueryTelemetryInput bounds a telemetry query. When Since and Until are both
// zero, the most recent rows are returned; otherwise the [Since,Until] window
// is queried. Kind filters by row kind when non-empty. Limit defaults to 100,
// caps at 1000.
type QueryTelemetryInput struct {
	Device string `json:"device" jsonschema:"node MAC (12 lowercase hex) or friendly name"`
	Since  int64  `json:"since,omitempty" jsonschema:"window start, epoch seconds"`
	Until  int64  `json:"until,omitempty" jsonschema:"window end, epoch seconds"`
	Kind   string `json:"kind,omitempty" jsonschema:"filter by telemetry kind"`
	Limit  int    `json:"limit,omitempty" jsonschema:"max rows (default 100, max 1000)"`
}

// TelemetryRow is one telemetry sample.
type TelemetryRow struct {
	TS        int64  `json:"ts"`
	Seq       int64  `json:"seq"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Value     any    `json:"value"`
	Text      string `json:"text"`
	ValueType string `json:"value_type"`
}

// QueryTelemetryOutput is the structured result of query_telemetry.
type QueryTelemetryOutput struct {
	Rows []TelemetryRow `json:"rows"`
}

// queryTelemetry returns telemetry rows for a node: recent (newest-first) when
// no window is given, or a [since,until] window (oldest-first) otherwise.
func (s *Server) queryTelemetry(_ context.Context, _ *mcp.CallToolRequest, in QueryTelemetryInput) (*mcp.CallToolResult, QueryTelemetryOutput, error) {
	n, errRes := s.resolveNode(in.Device)
	if errRes != nil {
		return errRes, QueryTelemetryOutput{}, nil
	}
	limit := clampLimit(in.Limit)

	var rows []store.DataRow
	var err error
	if in.Since == 0 && in.Until == 0 {
		rows, err = s.st.RecentData(n.ID, limit)
	} else {
		rows, err = s.st.QueryData(n.ID, in.Since, in.Until, in.Kind)
		if len(rows) > limit {
			rows = rows[:limit]
		}
	}
	if err != nil {
		return errorResultf("query telemetry for %q: %v", n.ID, err), QueryTelemetryOutput{}, nil
	}

	out := QueryTelemetryOutput{Rows: make([]TelemetryRow, 0, len(rows))}
	for _, r := range rows {
		out.Rows = append(out.Rows, TelemetryRow{
			TS: r.TS, Seq: r.Seq, Kind: r.Kind, Name: r.Name,
			Value: r.Value, Text: r.Text, ValueType: r.ValueType,
		})
	}
	return textResult(fmt.Sprintf("%s: %d telemetry row(s)", n.ID, len(out.Rows))), out, nil
}
