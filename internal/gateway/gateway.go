// Package gateway ties the TFTP server to the SQLite store.
// It handles the device poll cycle: extract EUI-64 from poll filename,
// register the device, pop commands from the store's queue, and log results.
package gateway

import (
	"fmt"
	"log"
	"strings"

	"github.com/davidg238/porta/internal/store"
	"github.com/davidg238/porta/internal/tftp"
)

// Gateway connects the TFTP server to the SQLite store.
type Gateway struct {
	Store  *store.Store
	server *tftp.Server
}

// New creates a Gateway with default handlers for /commands and /results.
func New(st *store.Store) *Gateway {
	g := &Gateway{
		Store:  st,
		server: tftp.NewServer(),
	}
	// Register default handlers — these are placeholders that return empty
	// responses. The real per-device handlers are registered dynamically in
	// HandleDevicePacket when we know the device ID.
	g.server.RegisterGet("/commands", func() []byte { return nil })
	g.server.RegisterPut("/results", func(path string, data []byte) {})
	g.server.RegisterGet("/debug", func() []byte { return nil })
	g.server.RegisterPut("/debug_result", func(path string, data []byte) {})
	return g
}

// HandleDevicePacket is the main entry point for each received UDP packet.
// It extracts the device ID from RRQ/WRQ paths, records the device in the
// store, registers per-device handlers, rewrites the packet path to strip
// query params, and delegates to the TFTP server.
func (g *Gateway) HandleDevicePacket(pkt []byte, sourceAddr string) [][]byte {
	op, err := tftp.ParseOpcode(pkt)
	if err != nil {
		return nil
	}

	switch op {
	case tftp.OpRRQ, tftp.OpWRQ:
		path, _, parseErr := tftp.ParseRequest(pkt)
		if parseErr != nil {
			return [][]byte{tftp.BuildError(0, "malformed request")}
		}

		deviceID := extractDeviceID(path)
		if deviceID != "" {
			// Record device in store.
			if err := g.Store.DeviceSeen(deviceID, sourceAddr, "", 0); err != nil {
				log.Printf("gateway: DeviceSeen(%q): %v", deviceID, err)
			}

			// Use per-device paths (e.g., /commands/aabb) so each device gets
			// its own transfer state in the TFTP server maps. This prevents
			// concurrent polls from colliding.
			basePath := stripQuery(path)
			devicePath := basePath + "/" + deviceID

			if op == tftp.OpRRQ {
				if basePath == "/debug" {
					g.server.RegisterGet(devicePath, debugCommandHandler(deviceID, g.Store))
				} else {
					g.server.RegisterGet(devicePath, commandsHandler(deviceID, g.Store))
				}
			} else {
				if basePath == "/debug_result" {
					g.server.RegisterPut(devicePath, debugResultHandler(deviceID, g.Store))
				} else {
					g.server.RegisterPut(devicePath, resultsHandler(deviceID, g.Store))
				}
			}

			pkt = rewritePath(pkt, devicePath)
		}
	}

	return g.server.HandlePacket(pkt)
}

// extractDeviceID parses ?id=<hex> from a TFTP path.
func extractDeviceID(path string) string {
	idx := strings.Index(path, "?")
	if idx < 0 {
		return ""
	}
	query := path[idx+1:]
	for _, part := range strings.Split(query, "&") {
		if strings.HasPrefix(part, "id=") {
			return part[3:]
		}
	}
	return ""
}

// stripQuery removes everything from ? onward.
func stripQuery(path string) string {
	if idx := strings.Index(path, "?"); idx >= 0 {
		return path[:idx]
	}
	return path
}

// rewritePath replaces the path in an RRQ/WRQ packet with newPath.
func rewritePath(pkt []byte, newPath string) []byte {
	// RRQ/WRQ format: opcode(2) + path + \0 + rest...
	// Find the first null byte after the opcode to locate end of path.
	pathStart := 2
	pathEnd := -1
	for i := pathStart; i < len(pkt); i++ {
		if pkt[i] == 0 {
			pathEnd = i
			break
		}
	}
	if pathEnd < 0 {
		return pkt
	}

	// Build new packet: opcode + newPath + rest (from null onward).
	out := make([]byte, 0, 2+len(newPath)+len(pkt)-pathEnd)
	out = append(out, pkt[:2]...)       // opcode
	out = append(out, []byte(newPath)...)
	out = append(out, pkt[pathEnd:]...) // \0 + mode + \0 + options...
	return out
}

// commandsHandler returns a GetHandler closure that pops a command from the
// store for the given device.
func commandsHandler(deviceID string, st *store.Store) tftp.GetHandler {
	return func() []byte {
		cmd, err := st.PopCommand(deviceID)
		if err != nil {
			log.Printf("gateway: PopCommand(%q): %v", deviceID, err)
			return nil
		}
		if cmd == nil {
			return nil
		}
		// Convert store.Command to tftp.Command for JSON encoding.
		tCmd := &tftp.Command{
			Verb:    cmd.Verb,
			Payload: cmd.Payload,
		}
		return tftp.CommandToJSON(tCmd)
	}
}

// resultsHandler returns a PutHandler closure that logs received data to the
// store for the given device.
func resultsHandler(deviceID string, st *store.Store) tftp.PutHandler {
	return func(path string, data []byte) {
		if err := st.LogData(deviceID, data); err != nil {
			log.Printf("gateway: LogData(%q): %v", deviceID, err)
		}
	}
}

// debugCommandHandler returns a GetHandler that pops a debug command from the store.
func debugCommandHandler(deviceID string, st *store.Store) tftp.GetHandler {
	return func() []byte {
		cmd, err := st.PopDebugCommand(deviceID)
		if err != nil {
			log.Printf("gateway: PopDebugCommand(%q): %v", deviceID, err)
			return nil
		}
		if cmd == "" {
			return nil
		}
		return []byte(cmd)
	}
}

// debugResultHandler returns a PutHandler that processes debug responses from the device.
func debugResultHandler(deviceID string, st *store.Store) tftp.PutHandler {
	return func(path string, data []byte) {
		msg := string(data)
		if strings.HasPrefix(msg, "dbg:paused ") {
			parts := strings.Fields(msg)
			if len(parts) >= 5 {
				reason := parts[1]
				var pc, line int
				fmt.Sscanf(parts[2], "%d", &pc)
				fmt.Sscanf(parts[3], "%d", &line)
				function := parts[4]
				if err := st.UpdateDebugState(deviceID, "paused", reason, pc, function, "", line); err != nil {
					log.Printf("gateway: UpdateDebugState(%q): %v", deviceID, err)
				}
			}
		} else if strings.HasPrefix(msg, "dbg:resumed") {
			if err := st.UpdateDebugState(deviceID, "running", "", 0, "", "", 0); err != nil {
				log.Printf("gateway: UpdateDebugState(%q): %v", deviceID, err)
			}
		}
		if err := st.LogData(deviceID, data); err != nil {
			log.Printf("gateway: LogData(%q): %v", deviceID, err)
		}
	}
}
