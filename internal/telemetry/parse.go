// Copyright (c) 2026 Ekorau LLC

// Package telemetry implements the porta gateway's telemetry plane:
// JSONL line parsing with type-faithful value_type inference, and the
// monitor row formatter. Pure logic — no I/O, no globals.
package telemetry

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Entry is a parsed JSONL telemetry line ready for store.InsertData. The
// HasTS / HasSeq booleans distinguish "absent" from "explicitly zero", so
// the caller can substitute the receive time / line index.
type Entry struct {
	TS        int64
	HasTS     bool
	Seq       int64
	HasSeq    bool
	Kind      string
	Name      string
	Value     any    // int64 / float64 / nil (the bound DB value)
	Text      string
	ValueType string // "int" | "float" | "bool" | "string" | ""
	Level     string // log stream only; "" when absent
}

// ParseLine decodes one JSONL line into an Entry. Returns ok=false for:
//   - blank/whitespace-only lines
//   - lines that fail json.Decode (truncated tail, malformed)
//   - lines that decode to anything other than a JSON object
//
// ok=true for a successful decode, even when "value" was a non-scalar
// (array/object/null) — the row still ingests with Value=nil and
// ValueType="" (graceful degradation).
func ParseLine(line []byte) (Entry, bool) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return Entry{}, false
	}
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.UseNumber()
	var raw map[string]any
	if err := dec.Decode(&raw); err != nil {
		return Entry{}, false
	}
	if raw == nil {
		return Entry{}, false
	}
	e := Entry{}
	if v, ok := raw["ts"]; ok {
		if n, ok := v.(json.Number); ok {
			if i, err := n.Int64(); err == nil {
				e.TS = i
				e.HasTS = true
			}
		}
	}
	if v, ok := raw["seq"]; ok {
		if n, ok := v.(json.Number); ok {
			if i, err := n.Int64(); err == nil {
				e.Seq = i
				e.HasSeq = true
			}
		}
	}
	if v, ok := raw["kind"].(string); ok {
		e.Kind = v
	}
	if v, ok := raw["name"].(string); ok {
		e.Name = v
	}
	if v, ok := raw["text"].(string); ok {
		e.Text = v
	}
	if v, ok := raw["level"].(string); ok {
		e.Level = v
	}
	classifyValue(&e, raw["value"])
	return e, true
}

// classifyValue inspects "value" and fills Value / ValueType / Text per the
// value_type inference rules (parity with examples/toit-gateway/handler.toit:160-184):
//
//	bool   → Value=int64(0|1), ValueType="bool"
//	number → int64 first, then float64; ValueType="int" or "float"
//	string → Text=raw, Value=nil, ValueType="string"
//	else (nil/array/object) → Value=nil, ValueType="" (degraded)
func classifyValue(e *Entry, raw any) {
	switch v := raw.(type) {
	case bool:
		if v {
			e.Value = int64(1)
		} else {
			e.Value = int64(0)
		}
		e.ValueType = "bool"
	case json.Number:
		s := v.String()
		if strings.ContainsAny(s, ".eE") {
			f, err := v.Float64()
			if err == nil {
				e.Value = f
				e.ValueType = "float"
			}
			return
		}
		if i, err := v.Int64(); err == nil {
			e.Value = i
			e.ValueType = "int"
			return
		}
		// Int parse failed (e.g. out of range) — fall back to float.
		if f, err := v.Float64(); err == nil {
			e.Value = f
			e.ValueType = "float"
		}
	case string:
		// A string in "value" overrides any "text" key.
		e.Text = v
		e.Value = nil
		e.ValueType = "string"
	default:
		// nil, []any, map[string]any → degraded.
		e.Value = nil
		e.ValueType = ""
	}
}
