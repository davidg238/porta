package apisrv

import "net/http"

// commandLogItem is one row of GET /api/nodes/{sel}/commands.
type commandLogItem struct {
	ID        int64  `json:"id"`
	Verb      string `json:"verb"`
	Args      string `json:"args"`
	IssuedAt  int64  `json:"issued_at"`
	IssuedBy  string `json:"issued_by"`
	Delivered bool   `json:"delivered"`
}

// commandLogLimit bounds how many recent commands the log read returns.
const commandLogLimit = 50

// handleNodeCommands returns the recent command log for one node.
func (h *Handler) handleNodeCommands(w http.ResponseWriter, r *http.Request) {
	id, ok := h.resolveSel(w, r.PathValue("sel"))
	if !ok {
		return
	}
	cmds, err := h.st.RecentCommandsForDevice(id, commandLogLimit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]commandLogItem, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, commandLogItem{
			ID: c.ID, Verb: c.Verb, Args: c.Args,
			IssuedAt: c.IssuedAt, IssuedBy: c.IssuedBy,
			Delivered: c.DeliveredAt.Valid,
		})
	}
	writeOK(w, map[string]any{"commands": out})
}
