package toolchain

import (
	"fmt"
	"strings"
)

// SDKVersion returns the active Toit SDK version (`toit version`), trimmed.
func SDKVersion(ex *Executor) (string, error) {
	out, err := ex.Run("toit version", "toit", "version")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// CheckSDK errors when the active build SDK differs from the node's reported
// SDK — a relocated image only runs on the SDK it was built with.
func CheckSDK(nodeSDK, activeSDK string) error {
	if nodeSDK == activeSDK {
		return nil
	}
	return fmt.Errorf("SDK mismatch: node runs %q but build toolchain is %q — image would not run (use --force to override)",
		nodeSDK, activeSDK)
}
