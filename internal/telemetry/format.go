// Copyright (c) 2026 Ekorau LLC

// internal/telemetry/format.go
package telemetry

import (
	"fmt"
	"math"
	"strconv"

	"github.com/davidg238/porta/internal/store"
)

// FormatLine renders one data_log row for `porta monitor`, with two
// fixed-width kind columns ("log    " / "metric "). Parity with
// examples/toit-gateway/gateway.toit:215-225.
func FormatLine(r store.DataRow) string {
	if r.Kind != "metric" {
		return fmt.Sprintf("%d  log     %s", r.TS, r.Text)
	}
	rendered := renderMetric(r)
	return fmt.Sprintf("%d  metric  %s=%s", r.TS, r.Name, rendered)
}

func renderMetric(r store.DataRow) string {
	switch r.ValueType {
	case "string":
		return r.Text
	case "bool":
		if asInt64(r.Value) != 0 {
			return "true"
		}
		return "false"
	case "float":
		// NUMERIC affinity may have stored a whole-number float as INTEGER,
		// so r.Value can be int64 13 with ValueType "float" — coerce to
		// float64 and render with a guaranteed decimal point.
		f := asFloat64(r.Value)
		// NaN/±Inf have no JSON-legal rendering and FormatFloat would emit
		// "NaN"/"+Inf" → the ".0" patch below mangles them further; degrade.
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return "null"
		}
		// strconv.FormatFloat with -1 precision drops trailing zeros, so
		// 13.0 → "13"; reinstate the ".0" tail when the rendered form has
		// no decimal point and no exponent.
		s := strconv.FormatFloat(f, 'f', -1, 64)
		if !containsAny(s, ".eE") {
			s += ".0"
		}
		return s
	case "int":
		return strconv.FormatInt(asInt64(r.Value), 10)
	default:
		// Degraded — value was non-scalar at ingest, or unknown type tag.
		return "null"
	}
}

// asInt64 coerces an any (int64 or float64 from sql scan) to int64.
func asInt64(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case float64:
		return int64(n)
	default:
		return 0
	}
}

// asFloat64 coerces an any (int64 or float64) to float64.
func asFloat64(v any) float64 {
	switch n := v.(type) {
	case int64:
		return float64(n)
	case float64:
		return n
	default:
		return 0
	}
}

func containsAny(s, chars string) bool {
	for i := 0; i < len(s); i++ {
		for j := 0; j < len(chars); j++ {
			if s[i] == chars[j] {
				return true
			}
		}
	}
	return false
}
