// Copyright (c) 2026 Ekorau LLC

// Package control is porta's headless operations layer: it orchestrates
// command-queue writes and node resolution so the cobra CLI and the web UI
// share one implementation. Presentation (printing, HTML) stays in the
// callers; control returns structured results.
package control

import (
	"fmt"
	"io"

	"github.com/davidg238/porta/internal/command"
	"github.com/davidg238/porta/internal/store"
)

// InstallOpts mirrors the install knobs the CLI exposes.
type InstallOpts struct {
	CRC       int64 // 0 → compute from image
	IntervalS int64
	Triggers  []string
	Runlevel  int
	Lifecycle string // "" → run-once
}

// Set enqueues a per-app config write. issuedBy is "cli" or "web".
func Set(st *store.Store, id, app, key string, value any, issuedBy string, now int64) (int64, error) {
	c, err := command.Set(app, key, value)
	if err != nil {
		return 0, err
	}
	return st.EnqueueCommand(id, c.Verb, c.ArgsJSON, issuedBy, now)
}

// SetForward enqueues a set-forward command carrying the node's complete
// per-stream forwarding policy.
func SetForward(st *store.Store, id string, p command.ForwardPolicy, issuedBy string, now int64) (int64, error) {
	c, err := command.SetForward(p)
	if err != nil {
		return 0, err
	}
	return st.EnqueueCommand(id, c.Verb, c.ArgsJSON, issuedBy, now)
}

// SetMode enqueues an atomic set-mode command. porta validates the args
// (whole-or-reject) but does not originate them — nodus-cli does. The node
// re-validates and echoes the resulting config back in node_config.
func SetMode(st *store.Store, id string, args map[string]any, issuedBy string, now int64) (int64, error) {
	c, err := command.SetMode(args)
	if err != nil {
		return 0, err
	}
	return st.EnqueueCommand(id, c.Verb, c.ArgsJSON, issuedBy, now)
}

// SetName enqueues a set-name command. The name is node-owned; porta relays the
// command and later mirrors the echoed name for display.
func SetName(st *store.Store, id, name, issuedBy string, now int64) (int64, error) {
	c, err := command.SetName(name)
	if err != nil {
		return 0, err
	}
	return st.EnqueueCommand(id, c.Verb, c.ArgsJSON, issuedBy, now)
}

// Reboot enqueues a reboot command. Imperative one-shot (no observed-state
// convergence); the node applies it at the end of its next poll.
func Reboot(st *store.Store, id, issuedBy string, now int64) (int64, error) {
	c := command.Reboot()
	return st.EnqueueCommand(id, c.Verb, c.ArgsJSON, issuedBy, now)
}

// Debug enqueues a declarative debug session goal (action attach|detach).
func Debug(st *store.Store, id, name, action, issuedBy string, now int64) (int64, error) {
	c, err := command.Debug(name, action)
	if err != nil {
		return 0, err
	}
	return st.EnqueueCommand(id, c.Verb, c.ArgsJSON, issuedBy, now)
}

// Uninstall enqueues a stop command for the named container.
func Uninstall(st *store.Store, id, name, issuedBy string, now int64) (int64, error) {
	c := command.Stop(name)
	return st.EnqueueCommand(id, c.Verb, c.ArgsJSON, issuedBy, now)
}

// Install reads the image bytes, registers the payload under its CRC32-IEEE,
// and enqueues a run. Accepts a reader so a browser upload (no temp file) and
// the CLI (os.Open) both work.
func Install(st *store.Store, id, name string, img io.Reader, opts InstallOpts, issuedBy string, now int64) (int64, error) {
	data, err := io.ReadAll(img)
	if err != nil {
		return 0, err
	}
	crc := opts.CRC
	if crc == 0 {
		crc = int64(command.CRC32(data))
	}
	triggers, err := command.TriggersFromFlags(opts.Triggers, opts.IntervalS)
	if err != nil {
		return 0, err
	}
	runCmd, err := command.Run(command.RunSpec{
		Name: name, CRC: crc, Size: int64(len(data)),
		Triggers: triggers, Runlevel: opts.Runlevel, Lifecycle: opts.Lifecycle,
	})
	if err != nil {
		return 0, err
	}
	if err := st.RegisterPayload(crc, name, data); err != nil {
		return 0, err
	}
	return st.EnqueueCommand(id, runCmd.Verb, runCmd.ArgsJSON, issuedBy, now)
}

// DebugSend enqueues one dbg: request line onto the node's debug channel.
func DebugSend(st *store.Store, id, line, issuedBy string, now int64) (int64, error) {
	return st.EnqueueDebugRequest(id, line, now)
}

// DebugResponses returns dbg: response lines with id > after.
func DebugResponses(st *store.Store, id string, after int64, limit int) ([]store.DebugResponse, error) {
	return st.DebugResponsesAfter(id, after, limit)
}

// IsNodeID reports whether s is a node id per PROTOCOL.md §1: opaque
// lowercase hex, 12-16 digits (12-hex ESP32 MAC or 16-hex EUI-64).
func IsNodeID(s string) bool {
	if len(s) < 12 || len(s) > 16 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// ResolveNodeID turns a <node> arg (hex id or friendly name) into a node id.
func ResolveNodeID(st *store.Store, nodeArg string) (string, error) {
	if IsNodeID(nodeArg) {
		return nodeArg, nil
	}
	n, err := st.NodeByName(nodeArg)
	if err != nil {
		return "", err
	}
	if n == nil {
		return "", fmt.Errorf("no node named %q", nodeArg)
	}
	return n.ID, nil
}
