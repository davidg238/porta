// Copyright (c) 2026 Ekorau LLC

// Package handler implements the porta TFTP resource surface as a
// tftp.Dispatcher backed by the store.
package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/davidg238/porta/internal/command"
	"github.com/davidg238/porta/internal/config"
	"github.com/davidg238/porta/internal/serverstat"
	"github.com/davidg238/porta/internal/store"
	"github.com/davidg238/porta/internal/telemetry"
	"github.com/davidg238/porta/internal/tftp"
)

// faultReset is porta's policy set of "noteworthy" reset categories — the ones
// that trigger a data_log event. This is policy, not platform semantics: porta
// owns which neutral categories matter, never how a node derived them.
var faultReset = map[string]bool{"watchdog": true, "panic": true, "brownout": true}

// parseResetHealth extracts the neutral reset category and optional raw code
// from a report's health blob. Absent fields yield ("", nil).
func parseResetHealth(health json.RawMessage) (string, *int64) {
	var hb struct {
		Reset     string `json:"reset"`
		ResetCode *int64 `json:"reset_code"`
	}
	_ = json.Unmarshal(health, &hb) // best-effort; absent/garbled → zero value
	return hb.Reset, hb.ResetCode
}

// nodeConfigName extracts the node-owned name from a node_config echo block, or
// "" if absent (an unnamed node omits the key) — the empty string is COALESCEd
// in the store so it never clobbers porta's mirrored/auto-assigned name.
func nodeConfigName(cfg json.RawMessage) string {
	var c struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(cfg, &c)
	return c.Name
}

// Handler dispatches TFTP resources against the store.
type Handler struct {
	store *store.Store
	now   func() int64
	log   func(format string, args ...any) // injectable; defaults to log.Printf
	stats *serverstat.Stats                // optional; nil in tests that don't care
}

// New creates a Handler. now supplies the current epoch seconds (injectable
// for tests).
func New(st *store.Store, now func() int64) *Handler {
	return &Handler{store: st, now: now, log: log.Printf}
}

// SetLog replaces the handler's log sink (used by tests; production code
// keeps the default log.Printf).
func (h *Handler) SetLog(fn func(format string, args ...any)) { h.log = fn }

// SetStats attaches the process stats holder so the dispatcher can record
// report outcomes. Optional — nil leaves counting disabled.
func (h *Handler) SetStats(s *serverstat.Stats) { h.stats = s }

// parseResource splits "base?k=v&k2=v2" into base + params. A bare key maps to "".
func parseResource(raw string) (string, map[string]string) {
	params := map[string]string{}
	q := strings.Index(raw, "?")
	if q < 0 {
		return raw, params
	}
	base := raw[:q]
	for _, kv := range strings.Split(raw[q+1:], "&") {
		if kv == "" {
			continue
		}
		if eq := strings.Index(kv, "="); eq < 0 {
			params[kv] = ""
		} else {
			params[kv[:eq]] = kv[eq+1:]
		}
	}
	return base, params
}

// Read serves an RRQ. Touches the node on ?id=. The "commands" branch is the
// single drain chokepoint: err != nil → TFTP ERROR; (nil,nil) → empty body
// (queue drained); len>0 → the command.
func (h *Handler) Read(resource, peer string) ([]byte, error) {
	base, params := parseResource(resource)
	if id, ok := params["id"]; ok && id != "" {
		if err := h.store.TouchNode(id, peer, h.now()); err != nil {
			return nil, err
		}
	}
	switch base {
	case "commands":
		return h.readCommands(params["id"])
	case "debug":
		return h.readDebug(params["id"])
	case "payload":
		return h.readPayload(params)
	default:
		return nil, fmt.Errorf("file not found: %s", base)
	}
}

// readCommands is the chokepoint. Every return is one of: (nil, err) for a real
// error → TFTP ERROR; (nil, nil) for an empty queue → drain sentinel; (bytes,
// nil) for a command. No error path can fall through to an empty body.
func (h *Handler) readCommands(id string) ([]byte, error) {
	if id == "" {
		return nil, fmt.Errorf("commands: missing id")
	}
	cmd, err := h.store.NextUndelivered(id)
	if err != nil {
		return nil, err
	}
	if cmd == nil {
		return nil, nil // drain sentinel
	}
	return command.EncodeWire(cmd.Verb, cmd.Args), nil
}

// readDebug is the debug-request chokepoint, mirroring readCommands: (nil,err)
// → TFTP ERROR; (nil,nil) → drained; (bytes,nil) → one dbg: request line.
func (h *Handler) readDebug(id string) ([]byte, error) {
	if id == "" {
		return nil, fmt.Errorf("debug: missing id")
	}
	r, err := h.store.NextUndeliveredDebugRequest(id)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, nil // drain sentinel
	}
	return []byte(r.Line), nil
}

func (h *Handler) readPayload(params map[string]string) ([]byte, error) {
	crc, err := strconv.ParseInt(params["crc"], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("payload: invalid crc %q", params["crc"])
	}
	img, err := h.store.Payload(crc)
	if err != nil {
		return nil, err
	}
	if img == nil {
		return nil, fmt.Errorf("payload not found: crc=%d", crc)
	}
	return img, nil
}

// AcceptWrite gates WRQs: report?id=, data?id=, and debug?id= are accepted.
// Everything else (missing id, unknown base) → TFTP ERROR.
func (h *Handler) AcceptWrite(resource, peer string) error {
	base, params := parseResource(resource)
	if base != "report" && base != "data" && base != "debug" && base != "profile" {
		return fmt.Errorf("access denied: %s", base)
	}
	if params["id"] == "" {
		return fmt.Errorf("access denied: %s missing id", base)
	}
	return nil
}

// Write ingests a completed WRQ body: report → observed_state + reconcile;
// data → JSONL telemetry ingest; debug → dbg: response lines. Anything else is rejected.
func (h *Handler) Write(resource, peer string, data []byte) error {
	base, params := parseResource(resource)
	id := params["id"]
	if id == "" {
		return fmt.Errorf("access denied")
	}
	var err error
	switch base {
	case "report":
		err = h.writeReport(id, peer, data)
	case "data":
		err = h.writeData(id, peer, data)
	case "debug":
		err = h.writeDebug(id, peer, data)
	case "profile":
		err = h.writeProfile(id, peer, data)
	default:
		return fmt.Errorf("access denied: %s", base)
	}
	// Record the report-write outcome and — importantly — log a rejection. A
	// rejected write used to be silent on porta's side (only the node panicked),
	// which is how the doubled-JSON storm hid for weeks.
	if h.stats != nil && base == "report" {
		if err != nil {
			h.stats.ReportRejected()
		} else {
			h.stats.ReportOK()
		}
	}
	if err != nil {
		h.log("porta: %s write rejected from %s (id=%s): %v", base, peer, id, err)
	}
	return err
}

// writeReport is the previous Write body, refactored out.
func (h *Handler) writeReport(id, peer string, data []byte) error {
	if err := h.store.TouchNode(id, peer, h.now()); err != nil {
		return err
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return fmt.Errorf("report: bad json: %w", err)
	}
	field := func(k string) json.RawMessage {
		if v, ok := obj[k]; ok {
			return v
		}
		return json.RawMessage("{}")
	}
	observed := fmt.Sprintf(`{"apps":%s,"config":%s}`, field("apps"), field("config"))
	health := string(field("health"))
	if err := h.store.InsertReport(id, observed, health, h.now()); err != nil {
		return err
	}
	// Self-reported firmware identity (additive; absent keys decode to "").
	strField := func(k string) string {
		raw, ok := obj[k]
		if !ok {
			return ""
		}
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return ""
		}
		return s
	}
	if err := h.store.UpdateNodeIdentity(id, strField("chip"), strField("sdk"), strField("kind")); err != nil {
		h.log("porta: identity update error for %s: %v", id, err)
	}
	// Reset reason: store the latest, and emit a data_log event the first time a
	// fault category appears (change-detection dedup against the stored value).
	reset, resetCode := parseResetHealth(field("health"))
	if faultReset[reset] {
		// On a GetNode error we treat it as "no prior" (prior == nil), erring toward
		// emitting the diagnostic event rather than suppressing it.
		prior, _ := h.store.GetNode(id)
		if prior == nil || prior.LastReset != reset {
			var v any
			vtype := "string"
			if resetCode != nil {
				v = *resetCode
				vtype = "int"
			}
			if err := h.store.InsertData(id, h.now(), 0, "reset", reset, v, reset, vtype, ""); err != nil {
				h.log("porta: reset event insert error for %s: %v", id, err)
			}
		}
	}
	if err := h.store.UpdateNodeReset(id, reset, resetCode); err != nil {
		h.log("porta: reset update error for %s: %v", id, err)
	}
	// Effective-config echo: the node sends node_config only on cold boot + on
	// change. Persist the block verbatim and mirror the node-owned name; absent
	// → leave the cache (and name) unchanged (steady-state report).
	if cfg, ok := obj["node_config"]; ok && len(cfg) > 0 && string(cfg) != "null" {
		name := nodeConfigName(cfg)
		if err := h.store.UpdateNodeConfig(id, string(cfg), name); err != nil {
			h.log("porta: node-config update error for %s: %v", id, err)
		}
	}
	h.reconcileAfterReport(id, field("config"))
	return nil
}

// writeData ingests a JSONL telemetry body. Best-effort per line: blank
// lines, truncated tails, and non-object lines are skipped (no error). A
// non-scalar "value" inserts a row with Value=nil, ValueType=NULL
// (graceful degradation). A real SQL failure on TouchNode propagates;
// per-row InsertData failures are logged and the loop continues.
// Parity with examples/toit-gateway/handler.toit's DataWriter_.
func (h *Handler) writeData(id, peer string, data []byte) error {
	if err := h.store.TouchNode(id, peer, h.now()); err != nil {
		return err
	}
	now := h.now()
	accepted := int64(0)
	for _, raw := range bytes.Split(data, []byte("\n")) {
		e, ok := telemetry.ParseLine(raw)
		if !ok {
			continue
		}
		ts := e.TS
		if !e.HasTS {
			ts = now
		}
		seq := e.Seq
		if !e.HasSeq {
			seq = accepted
		}
		accepted++
		kind := e.Kind
		if kind == "" {
			kind = "log"
		}
		if err := h.store.InsertData(id, ts, seq, kind, e.Name, e.Value, e.Text, e.ValueType, e.Level); err != nil {
			h.log("porta: data ingest insert error for %s seq=%d: %v", id, seq, err)
			continue
		}
	}
	return nil
}

// writeDebug ingests newline-delimited dbg: response lines into debug_response.
// Best-effort per line: blank lines skipped; per-row insert errors logged.
func (h *Handler) writeDebug(id, peer string, data []byte) error {
	if err := h.store.TouchNode(id, peer, h.now()); err != nil {
		return err
	}
	now := h.now()
	seq := int64(0)
	for _, raw := range bytes.Split(data, []byte("\n")) {
		line := strings.TrimRight(string(raw), "\r")
		if line == "" {
			continue
		}
		if err := h.store.InsertDebugResponse(id, now, seq, line); err != nil {
			h.log("porta: debug response insert error for %s seq=%d: %v", id, seq, err)
			continue
		}
		seq++
	}
	return nil
}

// writeProfile ingests one profiler blob (the whole WRQ body, opaque to porta)
// into profile_result, tagged with the node's current profile session app/label
// for correlation. porta never parses the blob — decode is node-kind-defined.
func (h *Handler) writeProfile(id, peer string, data []byte) error {
	if err := h.store.TouchNode(id, peer, h.now()); err != nil {
		return err
	}
	app, label := "", ""
	if sess, err := h.store.GetProfileSession(id); err == nil && sess != nil {
		app, label = sess.App, sess.Label
	}
	_, err := h.store.InsertProfileResult(id, app, label, h.now(), data)
	return err
}

// reconcileAfterReport is the post-report self-heal hook. Best-effort:
// every error path (panic, SQL, decode) is caught and logged; nothing
// propagates to the TFTP layer.
func (h *Handler) reconcileAfterReport(id string, configRaw json.RawMessage) {
	defer func() {
		if r := recover(); r != nil {
			h.log("porta: reconcile panic for %s: %v", id, r)
		}
	}()
	dec := json.NewDecoder(bytes.NewReader(configRaw))
	dec.UseNumber()
	var observed map[string]map[string]any
	if err := dec.Decode(&observed); err != nil {
		h.log("porta: reconcile decode error for %s: %v", id, err)
		return
	}
	// "config":null decodes successfully to a nil map. Treating that as an
	// empty observed would make every desired key look diverged and trigger
	// a re-issue storm on every report. A nil observed means "node didn't
	// send config" → skip reconcile entirely. Note: a report that OMITS
	// the config key falls back to "{}" via the field() default in Write()
	// and reconcile runs against an empty observed (parity with the Toit
	// reference) — the in-flight guard keeps that bounded to one re-issue
	// per cycle. The asymmetry between null and missing is intentional.
	if observed == nil {
		return
	}
	cmds, err := h.store.CommandLog(id)
	if err != nil {
		h.log("porta: reconcile command-log error for %s: %v", id, err)
		return
	}
	for _, r := range config.Reconcile(cmds, observed) {
		if _, err := h.store.EnqueueCommand(id, r.Verb, r.Args, "gateway-reconcile", h.now()); err != nil {
			h.log("porta: reconcile enqueue error for %s %s.%s: %v", id, r.App, r.Key, err)
			continue
		}
		h.log("porta: reconcile re-issued %s.%s for %s (observed diverged)", r.App, r.Key, id)
	}
}

// Complete marks a command or debug request delivered after a successful RRQ
// transfer — never on pop, never for payload transfers, never on failure.
func (h *Handler) Complete(op uint16, resource, peer string, ok bool) {
	if !ok || op != tftp.OpRRQ {
		return
	}
	base, params := parseResource(resource)
	id := params["id"]
	if id == "" {
		return
	}
	switch base {
	case "commands":
		cmd, err := h.store.NextUndelivered(id)
		if err != nil || cmd == nil {
			return // nothing to mark (drain-sentinel transfer or transient error)
		}
		_ = h.store.MarkDelivered(cmd.ID, h.now())
	case "debug":
		r, err := h.store.NextUndeliveredDebugRequest(id)
		if err != nil || r == nil {
			return
		}
		_ = h.store.MarkDebugRequestDelivered(r.ID, h.now())
	}
}
