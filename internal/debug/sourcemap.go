// Package debug provides remote debugging support for Berry VM devices.
package debug

import (
	"encoding/json"
	"fmt"
	"sort"
)

// SourceMap holds the mapping between bytecode PC offsets and ST source lines.
type SourceMap struct {
	Source    string
	Functions map[string][]PCLine // function name → sorted list of (pc, line)
}

// PCLine is a single entry: bytecode offset and corresponding ST source line.
type PCLine struct {
	PC   int
	Line int
}

// ParseSourceMap parses a .stmap JSON file.
func ParseSourceMap(data []byte) (*SourceMap, error) {
	var raw struct {
		Source    string              `json:"source"`
		Functions map[string][][2]int `json:"functions"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse stmap: %w", err)
	}

	sm := &SourceMap{
		Source:    raw.Source,
		Functions: make(map[string][]PCLine, len(raw.Functions)),
	}
	for name, entries := range raw.Functions {
		pclines := make([]PCLine, len(entries))
		for i, e := range entries {
			pclines[i] = PCLine{PC: e[0], Line: e[1]}
		}
		sort.Slice(pclines, func(i, j int) bool { return pclines[i].PC < pclines[j].PC })
		sm.Functions[name] = pclines
	}
	return sm, nil
}

// LineToPCRange returns the bytecode PC range [start, end] for a given ST source line
// within a function. Returns (0, 0, false) if the line is not found.
// If this is the last entry, pc_end is -1 (meaning "to end of function").
func (sm *SourceMap) LineToPCRange(funcName string, line int) (pcStart, pcEnd int, ok bool) {
	entries, exists := sm.Functions[funcName]
	if !exists {
		return 0, 0, false
	}

	for i, e := range entries {
		if e.Line == line {
			pcStart = e.PC
			if i+1 < len(entries) {
				pcEnd = entries[i+1].PC - 1
			} else {
				pcEnd = -1 // last entry — extends to end of function
			}
			return pcStart, pcEnd, true
		}
	}
	return 0, 0, false
}

// PCToLine returns the ST source line for a given bytecode PC offset within a function.
func (sm *SourceMap) PCToLine(funcName string, pc int) (int, bool) {
	entries, exists := sm.Functions[funcName]
	if !exists || len(entries) == 0 {
		return 0, false
	}

	// Find the last entry with PC <= target
	line := entries[0].Line
	for _, e := range entries {
		if e.PC > pc {
			break
		}
		line = e.Line
	}
	return line, true
}
