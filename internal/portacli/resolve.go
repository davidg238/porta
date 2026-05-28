package portacli

import (
	"fmt"

	"github.com/davidg238/porta/internal/store"
)

// isMAC reports whether s is exactly 12 lowercase hex digits.
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
	if isMAC(nodeArg) {
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
