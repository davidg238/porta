// Copyright (c) 2026 Ekorau LLC

package gateway

import (
	"encoding/hex"
	"encoding/json"
)

// Command represents a queued command for a device (Smalltalk wire format).
type Command struct {
	Verb    string
	Payload []byte
}

// CommandToJSON encodes a Command as JSON matching the firmware format:
// {"verb":"...", "payload":"hex..."}
func CommandToJSON(cmd *Command) []byte {
	m := map[string]string{
		"verb":    cmd.Verb,
		"payload": hex.EncodeToString(cmd.Payload),
	}
	b, _ := json.Marshal(m)
	return b
}
