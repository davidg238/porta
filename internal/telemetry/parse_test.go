// internal/telemetry/parse_test.go
package telemetry

import "testing"

func TestParseLineMetricInt(t *testing.T) {
	e, ok := ParseLine([]byte(`{"ts":100,"seq":0,"kind":"metric","name":"pm","value":13}`))
	if !ok {
		t.Fatal("ok=false, want true")
	}
	if !e.HasTS || e.TS != 100 {
		t.Errorf("TS=%d hasTS=%v, want 100 true", e.TS, e.HasTS)
	}
	if !e.HasSeq || e.Seq != 0 {
		t.Errorf("Seq=%d hasSeq=%v, want 0 true", e.Seq, e.HasSeq)
	}
	if e.Kind != "metric" || e.Name != "pm" {
		t.Errorf("Kind=%q Name=%q", e.Kind, e.Name)
	}
	v, ok := e.Value.(int64)
	if !ok || v != 13 {
		t.Errorf("Value=%v (%T), want int64(13)", e.Value, e.Value)
	}
	if e.ValueType != "int" {
		t.Errorf("ValueType=%q, want int", e.ValueType)
	}
}

func TestParseLineMetricFloat(t *testing.T) {
	e, ok := ParseLine([]byte(`{"ts":101,"kind":"metric","name":"t","value":20.5}`))
	if !ok {
		t.Fatal("ok=false")
	}
	v, ok := e.Value.(float64)
	if !ok || v != 20.5 {
		t.Errorf("Value=%v (%T), want float64(20.5)", e.Value, e.Value)
	}
	if e.ValueType != "float" {
		t.Errorf("ValueType=%q, want float", e.ValueType)
	}
}

func TestParseLineMetricWholeFloat(t *testing.T) {
	// A literal "13.0" must stay float (the dot is a syntactic signal).
	e, ok := ParseLine([]byte(`{"kind":"metric","name":"w","value":13.0}`))
	if !ok {
		t.Fatal("ok=false")
	}
	v, ok := e.Value.(float64)
	if !ok || v != 13.0 {
		t.Errorf("Value=%v (%T), want float64(13.0)", e.Value, e.Value)
	}
	if e.ValueType != "float" {
		t.Errorf("ValueType=%q, want float (literal 13.0 must stay float)", e.ValueType)
	}
}

func TestParseLineMetricBool(t *testing.T) {
	e, ok := ParseLine([]byte(`{"kind":"metric","name":"door","value":true}`))
	if !ok {
		t.Fatal("ok=false")
	}
	v, ok := e.Value.(int64)
	if !ok || v != 1 {
		t.Errorf("Value=%v (%T), want int64(1)", e.Value, e.Value)
	}
	if e.ValueType != "bool" {
		t.Errorf("ValueType=%q, want bool", e.ValueType)
	}
	e, _ = ParseLine([]byte(`{"kind":"metric","name":"door","value":false}`))
	v, _ = e.Value.(int64)
	if v != 0 {
		t.Errorf("false → %d, want 0", v)
	}
}

func TestParseLineMetricString(t *testing.T) {
	e, ok := ParseLine([]byte(`{"kind":"metric","name":"mode","value":"auto"}`))
	if !ok {
		t.Fatal("ok=false")
	}
	if e.Value != nil {
		t.Errorf("Value=%v, want nil (string lives in Text)", e.Value)
	}
	if e.Text != "auto" {
		t.Errorf("Text=%q, want auto", e.Text)
	}
	if e.ValueType != "string" {
		t.Errorf("ValueType=%q, want string", e.ValueType)
	}
}

func TestParseLineLog(t *testing.T) {
	e, ok := ParseLine([]byte(`{"kind":"log","text":"hello"}`))
	if !ok {
		t.Fatal("ok=false")
	}
	if e.Kind != "log" || e.Text != "hello" {
		t.Errorf("Kind=%q Text=%q", e.Kind, e.Text)
	}
	if e.ValueType != "" {
		t.Errorf("ValueType=%q, want \"\"", e.ValueType)
	}
}

func TestParseLineDefaultsAndKindFallback(t *testing.T) {
	e, ok := ParseLine([]byte(`{"text":"hi"}`))
	if !ok {
		t.Fatal("ok=false")
	}
	if e.HasTS {
		t.Errorf("HasTS=true, want false (TS absent)")
	}
	if e.HasSeq {
		t.Errorf("HasSeq=true, want false (Seq absent)")
	}
	if e.Kind != "" {
		t.Errorf("Kind=%q, want \"\" (caller substitutes)", e.Kind)
	}
}

func TestParseLineTruncatedSkipped(t *testing.T) {
	_, ok := ParseLine([]byte(`{"kind":"metric","name":"pm","value":`))
	if ok {
		t.Error("truncated line ok=true, want false")
	}
}

func TestParseLineNonObjectSkipped(t *testing.T) {
	_, ok := ParseLine([]byte(`42`))
	if ok {
		t.Error("non-object ok=true, want false")
	}
	_, ok = ParseLine([]byte(`[1,2]`))
	if ok {
		t.Error("array root ok=true, want false")
	}
}

func TestParseLineNonScalarValueDegrades(t *testing.T) {
	e, ok := ParseLine([]byte(`{"kind":"metric","name":"x","value":[1,2]}`))
	if !ok {
		t.Fatal("ok=false, want true (degraded but ingestible)")
	}
	if e.Value != nil {
		t.Errorf("Value=%v, want nil (degraded)", e.Value)
	}
	if e.ValueType != "" {
		t.Errorf("ValueType=%q, want \"\" (degraded)", e.ValueType)
	}
}

func TestParseLineBlankSkipped(t *testing.T) {
	if _, ok := ParseLine([]byte("")); ok {
		t.Error("empty line ok=true, want false")
	}
	if _, ok := ParseLine([]byte("   ")); ok {
		t.Error("whitespace ok=true, want false")
	}
}
