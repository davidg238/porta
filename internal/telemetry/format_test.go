// Copyright (c) 2026 Ekorau LLC

// internal/telemetry/format_test.go
package telemetry

import (
	"math"
	"testing"

	"github.com/davidg238/porta/internal/store"
)

func TestFormatLineMetricInt(t *testing.T) {
	r := store.DataRow{TS: 100, Seq: 0, Kind: "metric", Name: "n", Value: int64(7), ValueType: "int"}
	if got := FormatLine(r); got != FormatTS(100)+"  metric  n=7" {
		t.Errorf("got %q, want %q", got, FormatTS(100)+"  metric  n=7")
	}
}

func TestFormatLineMetricFloat(t *testing.T) {
	r := store.DataRow{TS: 101, Seq: 1, Kind: "metric", Name: "pm", Value: float64(13.5), ValueType: "float"}
	if got := FormatLine(r); got != FormatTS(101)+"  metric  pm=13.5" {
		t.Errorf("got %q, want %q", got, FormatTS(101)+"  metric  pm=13.5")
	}
}

func TestFormatLineMetricFloatWholeNumberAddsDecimal(t *testing.T) {
	// NUMERIC affinity stored 13.0 as INTEGER → QueryData returned int64(13);
	// value_type "float" must still render with a decimal point.
	r := store.DataRow{TS: 102, Seq: 0, Kind: "metric", Name: "pm", Value: int64(13), ValueType: "float"}
	if got := FormatLine(r); got != FormatTS(102)+"  metric  pm=13.0" {
		t.Errorf("got %q, want %q", got, FormatTS(102)+"  metric  pm=13.0")
	}
}

func TestFormatLineMetricFloatNaNInfRenderNull(t *testing.T) {
	// NaN/±Inf are unreachable via the JSON wire (RFC 7159 forbids the
	// literals) but an in-process InsertData caller could bind them; render
	// "null" rather than the malformed "NaN.0" / "+Inf.0".
	cases := []struct {
		name string
		v    float64
	}{
		{"NaN", math.NaN()},
		{"+Inf", math.Inf(1)},
		{"-Inf", math.Inf(-1)},
	}
	for _, c := range cases {
		r := store.DataRow{TS: 200, Kind: "metric", Name: "pm", Value: c.v, ValueType: "float"}
		want := FormatTS(200)+"  metric  pm=null"
		if got := FormatLine(r); got != want {
			t.Errorf("%s: got %q, want %q", c.name, got, want)
		}
	}
}

func TestFormatLineMetricBool(t *testing.T) {
	rt := store.DataRow{TS: 103, Kind: "metric", Name: "door", Value: int64(1), ValueType: "bool"}
	if got := FormatLine(rt); got != FormatTS(103)+"  metric  door=true" {
		t.Errorf("got %q, want %q", got, FormatTS(103)+"  metric  door=true")
	}
	rf := store.DataRow{TS: 104, Kind: "metric", Name: "door", Value: int64(0), ValueType: "bool"}
	if got := FormatLine(rf); got != FormatTS(104)+"  metric  door=false" {
		t.Errorf("got %q, want %q", got, FormatTS(104)+"  metric  door=false")
	}
}

func TestFormatLineMetricString(t *testing.T) {
	r := store.DataRow{TS: 105, Kind: "metric", Name: "mode", Text: "auto", ValueType: "string"}
	if got := FormatLine(r); got != FormatTS(105)+"  metric  mode=auto" {
		t.Errorf("got %q, want %q", got, FormatTS(105)+"  metric  mode=auto")
	}
}

func TestFormatLineLog(t *testing.T) {
	r := store.DataRow{TS: 106, Kind: "log", Text: "started blink", ValueType: ""}
	if got := FormatLine(r); got != FormatTS(106)+"  log     started blink" {
		t.Errorf("got %q, want %q", got, FormatTS(106)+"  log     started blink")
	}
}

func TestFormatLineDegradedRendersNull(t *testing.T) {
	// Metric whose ValueType is "" (e.g. value was a non-scalar at ingest) —
	// graceful: render name=null.
	r := store.DataRow{TS: 107, Kind: "metric", Name: "x", Value: nil, ValueType: ""}
	if got := FormatLine(r); got != FormatTS(107)+"  metric  x=null" {
		t.Errorf("got %q, want %q", got, FormatTS(107)+"  metric  x=null")
	}
}

func TestFormatLinePrintAndLevel(t *testing.T) {
	cases := []struct {
		row  store.DataRow
		want string
	}{
		{store.DataRow{TS: 5, Kind: "print", Text: "raw"}, FormatTS(5)+"  print   raw"},
		{store.DataRow{TS: 5, Kind: "log", Level: "warn", Text: "stall"}, FormatTS(5)+"  log     [warn] stall"},
		{store.DataRow{TS: 5, Kind: "log", Text: "plain"}, FormatTS(5)+"  log     plain"},
	}
	for _, c := range cases {
		if got := FormatLine(c.row); got != c.want {
			t.Errorf("FormatLine(%+v) = %q, want %q", c.row, got, c.want)
		}
	}
}
