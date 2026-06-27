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
	h.render(w, "node-profiles", profilesVM{
		ID: n.ID, Rows: out,
		Empty: "no profiles — porta profile start <node> <app>",
	})
}
