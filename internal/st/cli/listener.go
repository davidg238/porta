// Package cli implements a TCP/JSON-lines protocol listener for the jast CLI.
// Each connection sends one JSON object per line and receives one JSON response
// per line. This is how the jast2 Python CLI talks to the gateway over Tailscale.
package cli

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os/exec"
	"strings"
	"time"

	"github.com/davidg238/porta/internal/st/gateway"
	"github.com/davidg238/porta/internal/st/helpers"
	"github.com/davidg238/porta/internal/st/store"
)

// Request is the JSON structure sent by the CLI client.
type Request struct {
	Cmd     string `json:"cmd"`
	Device  string `json:"device,omitempty"`
	Verb    string `json:"verb,omitempty"`
	Payload string `json:"payload,omitempty"`
	Name    string `json:"name,omitempty"`
	Since   string `json:"since,omitempty"`
	Until   string `json:"until,omitempty"`
	Lines   int    `json:"lines,omitempty"`
}

// Response is the JSON structure sent back to the CLI client.
type Response struct {
	OK     bool        `json:"ok"`
	Data   interface{} `json:"data,omitempty"`
	Error  string      `json:"error,omitempty"`
	Stream bool        `json:"stream,omitempty"`
}

// Listener accepts TCP connections and dispatches JSON-lines commands.
type Listener struct {
	gw *gateway.Gateway
	ln net.Listener
}

// NewListener creates a Listener bound to the given gateway and net.Listener.
func NewListener(gw *gateway.Gateway, ln net.Listener) *Listener {
	return &Listener{gw: gw, ln: ln}
}

// Serve runs the accept loop. It blocks until the listener is closed.
func (l *Listener) Serve() {
	for {
		conn, err := l.ln.Accept()
		if err != nil {
			return // listener closed
		}
		go l.handleConn(conn)
	}
}

func (l *Listener) handleConn(conn net.Conn) {
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		var req Request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			sendResponse(conn, Response{OK: false, Error: "invalid JSON"})
			continue
		}
		resp := l.dispatch(req)
		sendResponse(conn, resp)
	}
}

func sendResponse(conn net.Conn, resp Response) {
	data, err := json.Marshal(resp)
	if err != nil {
		log.Printf("cli: marshal response: %v", err)
		return
	}
	data = append(data, '\n')
	conn.Write(data)
}

func (l *Listener) dispatch(req Request) Response {
	switch req.Cmd {
	case "devices":
		return l.cmdDevices()
	case "queue":
		return l.cmdQueue(req)
	case "name":
		return l.cmdName(req)
	case "status":
		return l.cmdWaitForVerb(req, "status")
	case "thread":
		return l.cmdWaitForVerb(req, "thread_status")
	case "modules":
		return l.cmdWaitForVerb(req, "modules")
	case "device-id":
		return l.cmdWaitForVerb(req, "device_id")
	case "data":
		return l.cmdData(req)
	case "network":
		return l.cmdNetwork()
	case "add":
		return l.cmdAdd(req)
	case "commission":
		return l.cmdCommission(req)
	default:
		return Response{OK: false, Error: fmt.Sprintf("unknown command %q", req.Cmd)}
	}
}

// deviceRow is the JSON representation of a device in the "devices" response.
type deviceRow struct {
	EUI64    string `json:"eui64"`
	Name     string `json:"name"`
	Role     string `json:"role"`
	State    string `json:"state"`
	LastSeen string `json:"last_seen"`
}

func (l *Listener) cmdDevices() Response {
	devs, err := l.gw.Store.ListDevices()
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}
	rows := make([]deviceRow, len(devs))
	for i, d := range devs {
		rows[i] = deviceRow{
			EUI64:    d.EUI64,
			Name:     d.Name,
			Role:     d.Role,
			State:    d.State,
			LastSeen: d.LastSeen.Format(time.RFC3339),
		}
	}
	return Response{OK: true, Data: rows}
}

func (l *Listener) cmdQueue(req Request) Response {
	if req.Device == "" || req.Verb == "" {
		return Response{OK: false, Error: "queue requires device and verb"}
	}
	eui, err := l.gw.Store.ResolveDevice(req.Device)
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}
	var payload []byte
	if req.Payload != "" {
		// Payload is hex-encoded binary (e.g. .bec bytecode)
		decoded, err := hex.DecodeString(req.Payload)
		if err != nil {
			// Fall back to raw string for non-hex payloads
			payload = []byte(req.Payload)
		} else {
			payload = decoded
		}
	}
	if err := l.gw.Store.QueueCommand(eui, req.Verb, payload); err != nil {
		return Response{OK: false, Error: err.Error()}
	}
	return Response{OK: true, Data: "queued"}
}

func (l *Listener) cmdName(req Request) Response {
	if req.Device == "" || req.Name == "" {
		return Response{OK: false, Error: "name requires device and name"}
	}
	// Device field is the EUI-64 (or current name) to rename.
	eui, err := l.gw.Store.ResolveDevice(req.Device)
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}
	if err := l.gw.Store.SetDeviceName(eui, req.Name); err != nil {
		return Response{OK: false, Error: err.Error()}
	}
	return Response{OK: true}
}

// cmdWaitForVerb queues a verb to the device, then waits up to 5 seconds
// for the result to appear in the data log.
func (l *Listener) cmdWaitForVerb(req Request, verb string) Response {
	if req.Device == "" {
		return Response{OK: false, Error: fmt.Sprintf("%s requires device", verb)}
	}
	result, err := helpers.WaitForVerb(l.gw.Store, req.Device, verb)
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}
	return Response{OK: true, Data: result}
}

func (l *Listener) cmdData(req Request) Response {
	if req.Device == "" {
		return Response{OK: false, Error: "data requires device"}
	}
	eui, err := l.gw.Store.ResolveDevice(req.Device)
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}

	since := time.Time{}
	until := time.Now()
	if req.Since != "" {
		t, err := time.Parse(time.RFC3339, req.Since)
		if err != nil {
			return Response{OK: false, Error: fmt.Sprintf("invalid since: %v", err)}
		}
		since = t
	}
	if req.Until != "" {
		t, err := time.Parse(time.RFC3339, req.Until)
		if err != nil {
			return Response{OK: false, Error: fmt.Sprintf("invalid until: %v", err)}
		}
		until = t
	}

	rows, err := l.gw.Store.QueryData(eui, since, until)
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}

	lines := make([]string, len(rows))
	for i, r := range rows {
		lines[i] = fmt.Sprintf("[%s] %s", r.Timestamp.Format(time.RFC3339), string(r.Payload))
	}
	return Response{OK: true, Data: lines}
}

// derivePSKd returns the last 8 hex chars of the EUI-64, uppercased.
func derivePSKd(eui64 string) string {
	clean := strings.ReplaceAll(eui64, ":", "")
	clean = strings.ReplaceAll(clean, " ", "")
	if len(clean) < 8 {
		return strings.ToUpper(clean)
	}
	return strings.ToUpper(clean[len(clean)-8:])
}

func (l *Listener) cmdAdd(req Request) Response {
	if req.Device == "" {
		return Response{OK: false, Error: "add requires device (EUI-64)"}
	}
	eui := strings.ReplaceAll(req.Device, " ", "")
	eui = strings.ToLower(eui)
	if err := l.gw.Store.AddDevice(eui); err != nil {
		return Response{OK: false, Error: err.Error()}
	}
	return Response{OK: true, Data: fmt.Sprintf("added %s (pending)", eui)}
}

func (l *Listener) cmdCommission(req Request) Response {
	// Start commissioner
	out, err := exec.Command("docker", "exec", "otbr", "ot-ctl",
		"commissioner", "start").CombinedOutput()
	if err != nil {
		return Response{OK: false,
			Error: fmt.Sprintf("commissioner start failed: %s %v",
				strings.TrimSpace(string(out)), err)}
	}

	// Wait for commissioner to become active
	time.Sleep(1 * time.Second)

	// Get devices to commission
	var devices []store.Device
	if req.Device != "" {
		// Single device
		eui := strings.ToLower(strings.ReplaceAll(req.Device, " ", ""))
		devices = []store.Device{{EUI64: eui}}
	} else {
		// All pending devices
		devices, err = l.gw.Store.ListPendingDevices()
		if err != nil {
			return Response{OK: false, Error: err.Error()}
		}
	}

	if len(devices) == 0 {
		return Response{OK: true, Data: "no pending devices"}
	}

	// Add each as a joiner
	var results []string
	for _, d := range devices {
		pskd := derivePSKd(d.EUI64)
		out, err := exec.Command("docker", "exec", "otbr", "ot-ctl",
			"commissioner", "joiner", "add", d.EUI64, pskd).CombinedOutput()
		outStr := strings.TrimSpace(string(out))
		if err != nil || !strings.Contains(outStr, "Done") {
			results = append(results,
				fmt.Sprintf("%s: FAILED (%s)", d.EUI64, outStr))
		} else {
			results = append(results,
				fmt.Sprintf("%s: joiner added (pskd=%s)", d.EUI64, pskd))
		}
	}

	return Response{OK: true, Data: strings.Join(results, "\n")}
}

func (l *Listener) cmdNetwork() Response {
	devs, err := l.gw.Store.ListDevices()
	if err != nil {
		return Response{OK: false, Error: err.Error()}
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Devices: %d\n", len(devs)))
	for _, d := range devs {
		age := time.Since(d.LastSeen).Truncate(time.Second)
		name := d.EUI64
		if d.Name != "" {
			name = d.Name
		}
		sb.WriteString(fmt.Sprintf("  %s  %s ago\n", name, age))
	}
	return Response{OK: true, Data: sb.String()}
}
