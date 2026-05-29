package portacli

import (
	"github.com/davidg238/porta/internal/control"
	"github.com/davidg238/porta/internal/store"
)

// isMAC reports whether s is exactly 12 lowercase hex digits.
// Kept here so existing portacli tests that call isMAC directly continue to compile.
func isMAC(s string) bool {
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

// resolveNodeID turns a CLI <node> (MAC or friendly name) into a node id.
func resolveNodeID(st *store.Store, nodeArg string) (string, error) {
	return control.ResolveNodeID(st, nodeArg)
}
