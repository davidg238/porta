package apisrv

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/davidg238/porta/internal/command"
	"github.com/davidg238/porta/internal/control"
)

// commandReq is the uniform command envelope: a verb plus verb-specific args.
type commandReq struct {
	Verb string          `json:"verb"`
	Args json.RawMessage `json:"args"`
}

// handleCommand dispatches one of the five image-less verbs to control.*.
func (h *Handler) handleCommand(w http.ResponseWriter, r *http.Request) {
	id, ok := h.resolveSel(w, r.PathValue("sel"))
	if !ok {
		return
	}
	var req commandReq
	// UseNumber keeps integer-shaped config values from becoming floats.
	dec := json.NewDecoder(bytes.NewReader(readBody(r)))
	dec.UseNumber()
	if err := dec.Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	cmdID, err := h.dispatch(id, req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeOK(w, map[string]any{"command_id": cmdID})
}

// dispatch maps a verb+args to the matching control call. Errors are caller-
// facing (bad args, unknown verb, validation rejections).
func (h *Handler) dispatch(id string, req commandReq) (int64, error) {
	now := h.now()
	switch req.Verb {
	case "set":
		var a struct {
			App   string `json:"app"`
			Key   string `json:"key"`
			Value any    `json:"value"`
		}
		if err := decodeArgs(req.Args, &a); err != nil {
			return 0, err
		}
		if a.App == "" || a.Key == "" {
			return 0, fmt.Errorf("set requires app and key")
		}
		val, err := coerceScalar(a.Value)
		if err != nil {
			return 0, err
		}
		return control.Set(h.st, id, a.App, a.Key, val, "api", now)
	case "set-console":
		var a struct {
			State string `json:"state"`
		}
		if err := decodeArgs(req.Args, &a); err != nil {
			return 0, err
		}
		if a.State != "on" && a.State != "off" {
			return 0, fmt.Errorf("set-console state must be on or off")
		}
		return control.SetConsole(h.st, id, a.State == "on", "api", now)
	case "set-poll-interval":
		var a struct {
			Interval string `json:"interval"`
		}
		if err := decodeArgs(req.Args, &a); err != nil {
			return 0, err
		}
		secs, err := command.ParseDurationSeconds(a.Interval)
		if err != nil {
			return 0, err
		}
		return control.SetPollInterval(h.st, id, secs, "api", now)
	case "set-power-mode":
		var a struct {
			Mode string `json:"mode"`
		}
		if err := decodeArgs(req.Args, &a); err != nil {
			return 0, err
		}
		return control.SetPowerMode(h.st, id, a.Mode, "api", now)
	case "stop":
		var a struct {
			Name string `json:"name"`
		}
		if err := decodeArgs(req.Args, &a); err != nil {
			return 0, err
		}
		if a.Name == "" {
			return 0, fmt.Errorf("stop requires name")
		}
		return control.Uninstall(h.st, id, a.Name, "api", now)
	default:
		return 0, fmt.Errorf("unknown verb %q", req.Verb)
	}
}

// coerceScalar converts a json.Number (produced by UseNumber decoding) to int64
// or float64 so command.Set accepts it. Non-Number values pass through unchanged.
func coerceScalar(v any) (any, error) {
	n, ok := v.(json.Number)
	if !ok {
		return v, nil // string, bool, or already-typed value
	}
	if i, err := n.Int64(); err == nil {
		return i, nil
	}
	if f, err := n.Float64(); err == nil {
		return f, nil
	}
	return nil, fmt.Errorf("set: value %q is not a valid number", n)
}

// decodeArgs unmarshals the verb's args object (UseNumber for value typing).
func decodeArgs(raw json.RawMessage, dst any) error {
	if len(raw) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("invalid args: %w", err)
	}
	return nil
}
