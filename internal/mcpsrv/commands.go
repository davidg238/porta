package mcpsrv

import (
	"context"
	"fmt"

	"github.com/davidg238/porta/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// CommandLogInput selects the fleet-wide audit (Device empty) or one node's
// full command log (Device set). Limit applies only to the fleet-wide path
// (RecentCommands); default 100, cap 1000.
type CommandLogInput struct {
	Device string `json:"device,omitempty" jsonschema:"node MAC or name; omit for fleet-wide audit"`
	Limit  int    `json:"limit,omitempty" jsonschema:"max rows for fleet-wide audit (default 100, max 1000)"`
}

// CommandEntry is one command-queue row.
type CommandEntry struct {
	ID          int64  `json:"id"`
	Device      string `json:"device"`
	Verb        string `json:"verb"`
	Args        string `json:"args"`
	IssuedBy    string `json:"issued_by"`
	IssuedAt    int64  `json:"issued_at"`
	DeliveredAt int64  `json:"delivered_at"`
}

// CommandLogOutput is the structured result of command_log.
type CommandLogOutput struct {
	Commands []CommandEntry `json:"commands"`
}

// entryFromCommand maps a store.Command and its device id to a CommandEntry.
func entryFromCommand(c store.Command, device string) CommandEntry {
	return CommandEntry{
		ID:          c.ID,
		Device:      device,
		Verb:        c.Verb,
		Args:        c.Args,
		IssuedBy:    c.IssuedBy,
		IssuedAt:    c.IssuedAt,
		DeliveredAt: c.DeliveredAt.Int64, // 0 when undelivered (NULL)
	}
}

// commandLog returns fleet-wide recent commands when Device is empty, or one
// node's full command log when Device is set.
func (s *Server) commandLog(_ context.Context, _ *mcp.CallToolRequest, in CommandLogInput) (*mcp.CallToolResult, CommandLogOutput, error) {
	out := CommandLogOutput{Commands: []CommandEntry{}}

	if in.Device == "" {
		logged, err := s.st.RecentCommands(clampLimit(in.Limit))
		if err != nil {
			return errorResultf("recent commands: %v", err), CommandLogOutput{}, nil
		}
		for _, lc := range logged {
			out.Commands = append(out.Commands, entryFromCommand(lc.Command, lc.DeviceID))
		}
		return textResult(fmt.Sprintf("%d command(s) (fleet)", len(out.Commands))), out, nil
	}

	n, errRes := s.resolveNode(in.Device)
	if errRes != nil {
		return errRes, CommandLogOutput{}, nil
	}
	cmds, err := s.st.CommandLog(n.ID)
	if err != nil {
		return errorResultf("command log for %q: %v", n.ID, err), CommandLogOutput{}, nil
	}
	for _, c := range cmds {
		out.Commands = append(out.Commands, entryFromCommand(c, n.ID))
	}
	return textResult(fmt.Sprintf("%s: %d command(s)", n.ID, len(out.Commands))), out, nil
}
