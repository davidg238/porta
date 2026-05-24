package debug

import (
	"fmt"
	"sync"

	"github.com/davidg238/jast-gw/store"
)

// moduleKey uniquely identifies a module on a device.
type moduleKey struct {
	DeviceID string
	Module   string
}

// Manager coordinates debug sessions, source maps, and device state.
type Manager struct {
	store      *store.Store
	mu         sync.Mutex
	sourceMaps map[moduleKey]*SourceMap
	sources    map[moduleKey]string // ST source text
}

// NewManager creates a debug manager backed by the given store.
func NewManager(st *store.Store) *Manager {
	return &Manager{
		store:      st,
		sourceMaps: make(map[moduleKey]*SourceMap),
		sources:    make(map[moduleKey]string),
	}
}

// StoreSourceMap saves a parsed source map and ST source text for a device+module.
func (m *Manager) StoreSourceMap(deviceID, module string, stmapJSON []byte, source string) error {
	sm, err := ParseSourceMap(stmapJSON)
	if err != nil {
		return fmt.Errorf("parse source map: %w", err)
	}
	key := moduleKey{deviceID, module}
	m.mu.Lock()
	m.sourceMaps[key] = sm
	m.sources[key] = source
	m.mu.Unlock()
	return nil
}

// GetSourceMap returns the source map for a device+module, or nil.
func (m *Manager) GetSourceMap(deviceID, module string) *SourceMap {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sourceMaps[moduleKey{deviceID, module}]
}

// GetSource returns the ST source text for a device+module.
func (m *Manager) GetSource(deviceID, module string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sources[moduleKey{deviceID, module}]
}

// SetBreakpoint sets a breakpoint at an ST line, converting to PC range via source map.
// It searches all functions in the module's source map for the given line.
func (m *Manager) SetBreakpoint(deviceID, module string, stLine int) error {
	sm := m.GetSourceMap(deviceID, module)
	if sm == nil {
		return fmt.Errorf("no source map for %s/%s", deviceID, module)
	}

	for fnName := range sm.Functions {
		pcStart, pcEnd, ok := sm.LineToPCRange(fnName, stLine)
		if ok {
			return m.store.SetDebugBreakpoint(deviceID, module, stLine, pcStart, pcEnd)
		}
	}
	return fmt.Errorf("line %d not found in source map for %s", stLine, module)
}

// ClearBreakpoint removes a breakpoint.
func (m *Manager) ClearBreakpoint(deviceID, module string, stLine int) error {
	return m.store.ClearDebugBreakpoint(deviceID, module, stLine)
}

// PCToSTLine translates a bytecode PC offset to an ST source line.
func (m *Manager) PCToSTLine(deviceID, module, funcName string, pc int) (int, bool) {
	sm := m.GetSourceMap(deviceID, module)
	if sm == nil {
		return 0, false
	}
	return sm.PCToLine(funcName, pc)
}

// QueueDebugCommand queues a debug command for the device.
func (m *Manager) QueueDebugCommand(deviceID, command string) error {
	return m.store.QueueDebugCommand(deviceID, command)
}

// QueueBreakpointToDevice queues a dbg:break command with PC offsets.
func (m *Manager) QueueBreakpointToDevice(deviceID, module string, stLine int) error {
	bps, err := m.store.ListDebugBreakpoints(deviceID)
	if err != nil {
		return err
	}
	for _, bp := range bps {
		if bp.Module == module && bp.STLine == stLine {
			cmd := fmt.Sprintf("dbg:break %d %d", bp.PCStart, bp.PCEnd)
			return m.store.QueueDebugCommand(deviceID, cmd)
		}
	}
	return fmt.Errorf("breakpoint not found for line %d", stLine)
}
