// Copyright (c) 2026 Ekorau LLC

// node_profiles.go renders the per-node Profiles panel: an append-only list of
// profile result sessions (seq · age · app · label · bytes) each with a decode
// hint handing the blob (by seq) to the node's dev tool. porta performs NO
// decode — the blob is opaque and node-kind-defined.
package web

import (
	"fmt"
	"html/template"
	"net/http"

	"github.com/davidg238/porta/internal/control"
	"github.com/davidg238/porta/internal/store"
)

type profileRowVM struct {
	Seq        int64
	Age        string
	App        string
	Label      string
	Bytes      int64
	DecodeHref template.URL
}

type profilesVM struct {
	ID    string
	Rows  []profileRowVM
	Empty string
	// Session is the armed session's status line (empty App when nothing armed).
	Session profileSessionVM
}

type profileSessionVM struct {
	App    string
	Label  string
	Status string // operator-facing state label, e.g. "stale / timed-out — no result"
	State  string // raw state token (awaiting/running/stale/fulfilled) for styling
	Armed  bool
}

func (h *Handler) renderNodeProfiles(w http.ResponseWriter, n *store.Node) {
	rows, err := control.ProfileResultsRecent(h.st, n.ID, 50)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	now := h.now()
	out := make([]profileRowVM, 0, len(rows))
	for _, r := range rows { // already newest-first from DESC query
		href := fmt.Sprintf("nodus://profile?node=%s&seq=%d", n.ID, r.Seq)
		out = append(out, profileRowVM{
			Seq: r.Seq, Age: control.RelativeAge(r.TS, now), App: r.App, Label: r.Label,
			Bytes: r.ByteLen, DecodeHref: template.URL(href),
		})
	}
	vm := profilesVM{
		ID: n.ID, Rows: out,
		Empty: "no profiles — porta profile start <node> <app>",
	}
	if status, err := control.ProfileSessionStatus(h.st, n.ID, now); err == nil && status.Session != nil {
		vm.Session = profileSessionVM{
			App: status.Session.App, Label: status.Session.Label,
			Status: status.State.Label(), State: string(status.State), Armed: true,
		}
	}
	h.render(w, "node-profiles", vm)
}
