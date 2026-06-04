// Copyright (c) 2026 Ekorau LLC

package debug

import (
	"testing"
)

const testStmap = `{
  "source": "sensor.st",
  "functions": {
    "sensor": [[0, 1], [3, 2], [7, 5], [12, 8]],
    "sensor.Sensor.read": [[0, 10], [2, 11], [5, 12], [8, 14]]
  }
}`

func TestParseSourceMap(t *testing.T) {
	sm, err := ParseSourceMap([]byte(testStmap))
	if err != nil {
		t.Fatalf("ParseSourceMap: %v", err)
	}
	if sm.Source != "sensor.st" {
		t.Errorf("Source = %q, want %q", sm.Source, "sensor.st")
	}
	if len(sm.Functions) != 2 {
		t.Errorf("Functions count = %d, want 2", len(sm.Functions))
	}
}

func TestLineToPCRange(t *testing.T) {
	sm, err := ParseSourceMap([]byte(testStmap))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		fn      string
		line    int
		wantPC0 int
		wantPC1 int
		wantOK  bool
	}{
		{"sensor", 1, 0, 2, true},
		{"sensor", 2, 3, 6, true},
		{"sensor", 5, 7, 11, true},
		{"sensor", 8, 12, -1, true},
		{"sensor", 99, 0, 0, false},
		{"nosuch", 1, 0, 0, false},
		{"sensor.Sensor.read", 11, 2, 4, true},
	}

	for _, tt := range tests {
		pc0, pc1, ok := sm.LineToPCRange(tt.fn, tt.line)
		if ok != tt.wantOK {
			t.Errorf("LineToPCRange(%q, %d): ok=%v, want %v", tt.fn, tt.line, ok, tt.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if pc0 != tt.wantPC0 {
			t.Errorf("LineToPCRange(%q, %d): pc_start=%d, want %d", tt.fn, tt.line, pc0, tt.wantPC0)
		}
		if pc1 != tt.wantPC1 {
			t.Errorf("LineToPCRange(%q, %d): pc_end=%d, want %d", tt.fn, tt.line, pc1, tt.wantPC1)
		}
	}
}

func TestPCToLine(t *testing.T) {
	sm, err := ParseSourceMap([]byte(testStmap))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		fn     string
		pc     int
		want   int
		wantOK bool
	}{
		{"sensor", 0, 1, true},
		{"sensor", 2, 1, true},
		{"sensor", 3, 2, true},
		{"sensor", 7, 5, true},
		{"sensor", 15, 8, true},
		{"nosuch", 0, 0, false},
	}

	for _, tt := range tests {
		line, ok := sm.PCToLine(tt.fn, tt.pc)
		if ok != tt.wantOK {
			t.Errorf("PCToLine(%q, %d): ok=%v, want %v", tt.fn, tt.pc, ok, tt.wantOK)
			continue
		}
		if ok && line != tt.want {
			t.Errorf("PCToLine(%q, %d) = %d, want %d", tt.fn, tt.pc, line, tt.want)
		}
	}
}
