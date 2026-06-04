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

// SetConsole enqueues a set-console command.
func SetConsole(st *store.Store, id string, on bool, issuedBy string, now int64) (int64, error) {
	c := command.SetConsole(on)
	return st.EnqueueCommand(id, c.Verb, c.ArgsJSON, issuedBy, now)
}

// SetPowerMode enqueues a set-power-mode command (deep-sleep or always-on).
func SetPowerMode(st *store.Store, id, mode, issuedBy string, now int64) (int64, error) {
	c, err := command.SetPowerMode(mode)
	if err != nil {
		return 0, err
	}
	return st.EnqueueCommand(id, c.Verb, c.ArgsJSON, issuedBy, now)
}

// SetPollInterval caches the poll interval and enqueues the command.
func SetPollInterval(st *store.Store, id string, secs int64, issuedBy string, now int64) (int64, error) {
	if err := st.SetPollInterval(id, secs); err != nil {
		return 0, err
	}
	c := command.SetPollInterval(secs)
	return st.EnqueueCommand(id, c.Verb, c.ArgsJSON, issuedBy, now)
}

// SetMaxOffline sets the offline threshold (gateway-side only).
func SetMaxOffline(st *store.Store, id string, secs int64) error { return st.SetMaxOffline(id, secs) }

// Rename sets a node's friendly name.
func Rename(st *store.Store, id, name string) error { return st.SetNodeName(id, name) }

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

// IsMAC reports whether s is exactly 12 lowercase hex digits.
func IsMAC(s string) bool {
	if len(s) != 12 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// ResolveNodeID turns a <node> arg (MAC or friendly name) into a node id.
func ResolveNodeID(st *store.Store, nodeArg string) (string, error) {
	if IsMAC(nodeArg) {
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
