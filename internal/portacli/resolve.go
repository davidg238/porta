// Copyright (c) 2026 Ekorau LLC

package portacli

import (
	"github.com/davidg238/porta/internal/control"
	"github.com/davidg238/porta/internal/store"
)

// resolveNodeID turns a CLI <node> (MAC or friendly name) into a node id.
func resolveNodeID(st *store.Store, nodeArg string) (string, error) {
	return control.ResolveNodeID(st, nodeArg)
}
