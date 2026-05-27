package debugui

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"

	"github.com/davidg238/porta/internal/store"
)

// SourceData holds data for the source panel template.
type SourceData struct {
	Lines     []SourceLine
	Module    string
	HasSource bool
}

// SourceLine is one line of ST source with metadata.
type SourceLine struct {
	Num          int
	Text         string
	IsCurrent    bool
	IsBreakpoint bool
}

// StackFrame is one frame in the call stack.
type StackFrame struct {
	Function string
	Module   string
	Line     int
}

// LocalVar is a variable name/value pair.
type LocalVar struct {
	Name  string
	Value string
}

// StatusData holds data for the status bar.
type StatusData struct {
	DeviceName string
	Status     string // "running" or "paused"
	Location   string // e.g. "Counter>>run line 7"
}

var tmpl = template.Must(template.New("").Parse(`
{{define "status"}}
<div id="debug-status" class="status-bar">
  <span class="device-name">{{.DeviceName}}</span>
  <span class="status {{.Status}}">{{.Status}}</span>
  {{if .Location}}<span class="location">{{.Location}}</span>{{end}}
</div>
{{end}}

{{define "source"}}
<div id="debug-source" class="panel source-panel">
  {{if not .HasSource}}
    <div class="empty">No source loaded. Run compile_and_push first.</div>
  {{else}}
    <div class="source-header">{{.Module}}</div>
    <pre class="source-code">{{range .Lines}}<div class="line{{if .IsCurrent}} current{{end}}{{if .IsBreakpoint}} breakpoint{{end}}"><span class="gutter">{{if .IsBreakpoint}}<span class="bp-dot">●</span>{{else}} {{end}}{{printf "%3d" .Num}}</span> {{.Text}}</div>{{end}}</pre>
  {{end}}
</div>
{{end}}

{{define "stack"}}
<div id="debug-stack" class="panel stack-panel">
  <div class="panel-header">Stack</div>
  {{if not .}}
    <div class="empty">Not paused</div>
  {{else}}
    {{range .}}<div class="frame">{{.Function}} <span class="line-ref">line {{.Line}}</span></div>{{end}}
  {{end}}
</div>
{{end}}

{{define "locals"}}
<div id="debug-locals" class="panel locals-panel">
  <div class="panel-header">Locals</div>
  {{if not .}}
    <div class="empty">Not paused</div>
  {{else}}
    <table class="locals-table">
      {{range .}}<tr><td class="var-name">{{.Name}}</td><td class="var-value">{{.Value}}</td></tr>{{end}}
    </table>
  {{end}}
</div>
{{end}}
`))

// RenderStatus renders the status bar HTML fragment.
func RenderStatus(data StatusData) string {
	var buf bytes.Buffer
	tmpl.ExecuteTemplate(&buf, "status", data)
	return buf.String()
}

// RenderSource renders the source panel from raw source text, current line, and breakpoint set.
func RenderSource(module, source string, currentLine int, bpLines map[int]bool) string {
	var data SourceData
	data.Module = module
	if source == "" {
		data.HasSource = false
	} else {
		data.HasSource = true
		for i, text := range strings.Split(source, "\n") {
			num := i + 1
			data.Lines = append(data.Lines, SourceLine{
				Num:          num,
				Text:         text,
				IsCurrent:    num == currentLine,
				IsBreakpoint: bpLines[num],
			})
		}
	}
	var buf bytes.Buffer
	tmpl.ExecuteTemplate(&buf, "source", data)
	return buf.String()
}

// RenderStack renders the stack panel from a list of frames.
func RenderStack(frames []StackFrame) string {
	var buf bytes.Buffer
	tmpl.ExecuteTemplate(&buf, "stack", frames)
	return buf.String()
}

// RenderLocals renders the locals panel from a list of variables.
func RenderLocals(vars []LocalVar) string {
	var buf bytes.Buffer
	tmpl.ExecuteTemplate(&buf, "locals", vars)
	return buf.String()
}

// BuildSourceData collects source panel data from the debug manager state.
func BuildSourceData(source, module string, currentLine int, bps []store.DebugBreakpoint) (string, int, map[int]bool) {
	bpLines := make(map[int]bool)
	for _, bp := range bps {
		if bp.Module == module {
			bpLines[bp.STLine] = true
		}
	}
	return source, currentLine, bpLines
}

// FormatLocation returns a human-readable location string.
func FormatLocation(function, module string, line int) string {
	if function == "" {
		return ""
	}
	return fmt.Sprintf("%s>>%s line %d", module, function, line)
}
