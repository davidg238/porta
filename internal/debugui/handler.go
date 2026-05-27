package debugui

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strings"

	"github.com/davidg238/porta/internal/debug"
	"github.com/davidg238/porta/internal/store"
)

//go:embed static/debug.html
var staticFS embed.FS

var pageTmpl = template.Must(template.New("page").Parse(string(mustRead())))

func mustRead() []byte {
	data, err := staticFS.ReadFile("static/debug.html")
	if err != nil {
		panic(err)
	}
	return data
}

// Handler serves the debug viewer page and SSE events.
type Handler struct {
	hub *Hub
	st  *store.Store
	dbg *debug.Manager
}

// NewHandler creates a Handler wired to the hub, store, and debug manager.
func NewHandler(hub *Hub, st *store.Store, dbg *debug.Manager) *Handler {
	return &Handler{hub: hub, st: st, dbg: dbg}
}

// Register mounts the debug viewer routes on the given mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/debug", h.handlePage)
	mux.HandleFunc("/debug/events", h.handleSSE)
}

// handlePage renders the full debug viewer page with current state.
func (h *Handler) handlePage(w http.ResponseWriter, r *http.Request) {
	device := r.URL.Query().Get("device")
	if device == "" {
		h.handleDeviceList(w, r)
		return
	}

	eui, err := h.st.ResolveDevice(device)
	if err != nil {
		http.Error(w, fmt.Sprintf("unknown device: %s", device), 404)
		return
	}

	// Resolve display name
	deviceName := device
	devs, _ := h.st.ListDevices()
	for _, d := range devs {
		if d.EUI64 == eui && d.Name != "" {
			deviceName = d.Name
			break
		}
	}

	// Get current debug state
	ds, _ := h.st.GetDebugState(eui)

	module := ""
	var currentLine int
	status := "running"
	location := ""

	if ds != nil {
		status = ds.Status
		if ds.CurrentModule != "" {
			module = ds.CurrentModule
		}
		if ds.Status == "paused" {
			currentLine = ds.CurrentSTLine
			location = FormatLocation(ds.CurrentFunction, ds.CurrentModule, ds.CurrentSTLine)
		}
	}

	// Build panels
	source := ""
	if module != "" {
		source = h.dbg.GetSource(eui, module)
	}
	bps, _ := h.st.ListDebugBreakpoints(eui)

	_, cl, bpLines := BuildSourceData(source, module, currentLine, bps)
	statusHTML := RenderStatus(StatusData{DeviceName: deviceName, Status: status, Location: location})
	sourceHTML := RenderSource(module, source, cl, bpLines)
	stackHTML := RenderStack(nil)
	localsHTML := RenderLocals(nil)

	data := struct {
		DeviceName string
		DeviceEUI  string
		StatusHTML template.HTML
		SourceHTML template.HTML
		StackHTML  template.HTML
		LocalsHTML template.HTML
	}{
		DeviceName: deviceName,
		DeviceEUI:  eui,
		StatusHTML: template.HTML(statusHTML),
		SourceHTML: template.HTML(sourceHTML),
		StackHTML:  template.HTML(stackHTML),
		LocalsHTML: template.HTML(localsHTML),
	}

	var buf bytes.Buffer
	if err := pageTmpl.Execute(&buf, data); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(buf.Bytes())
}

// handleSSE streams debug events for a specific device.
func (h *Handler) handleSSE(w http.ResponseWriter, r *http.Request) {
	device := r.URL.Query().Get("device")
	if device == "" {
		http.Error(w, "device parameter required", 400)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", 500)
		return
	}

	ch := h.hub.Subscribe(device)
	defer h.hub.Unsubscribe(device, ch)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher.Flush()

	log.Printf("debugui: SSE client connected for device %s", device)

	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				return
			}
			// SSE data fields cannot contain newlines — each line must be a separate data: line
			fmt.Fprintf(w, "event: %s\n", evt.Name)
			for _, line := range strings.Split(evt.HTML, "\n") {
				fmt.Fprintf(w, "data: %s\n", line)
			}
			fmt.Fprint(w, "\n")
			flusher.Flush()
		case <-r.Context().Done():
			log.Printf("debugui: SSE client disconnected for device %s", device)
			return
		}
	}
}

// handleDeviceList shows a simple device selector when no device is specified.
func (h *Handler) handleDeviceList(w http.ResponseWriter, r *http.Request) {
	devs, err := h.st.ListDevices()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!DOCTYPE html><html><head><title>ST Debug</title>
<style>body{font-family:system-ui;background:#1e1e1e;color:#d4d4d4;padding:2rem;}
a{color:#569cd6;text-decoration:none;font-size:1.2rem;}a:hover{text-decoration:underline;}
.device{padding:0.5rem 0;}</style></head><body><h2>Select Device</h2>`)
	for _, d := range devs {
		name := d.EUI64
		if d.Name != "" {
			name = d.Name
		}
		fmt.Fprintf(w, `<div class="device"><a href="/debug?device=%s">%s</a> <span style="color:#666">(%s)</span></div>`, d.EUI64, name, d.EUI64)
	}
	fmt.Fprint(w, `</body></html>`)
}
