package toolchain

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// snapshotCacheDir is jag's decode snapshot cache; `jag decode` reads
// <dir>/<uuid>.snapshot. Defaults to ~/.local/state/toit/snapshots, overridable
// via $PORTA_SNAPSHOT_DIR (used by tests).
func snapshotCacheDir() string {
	if d := os.Getenv("PORTA_SNAPSHOT_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "toit", "snapshots")
}

// RetainSnapshot reads the snapshot's program UUID (`toit tool snapshot uuid`)
// and copies the snapshot into jag's decode cache as <uuid>.snapshot, so a later
// `jag decode <blob>` for a panic from this image symbolicates locally. Returns
// the UUID. Callers treat failures as non-fatal (decode is best-effort).
func RetainSnapshot(ex *Executor, snapshotPath string) (string, error) {
	out, err := ex.Run("snapshot uuid", "toit", "tool", "snapshot", "uuid", snapshotPath)
	if err != nil {
		return "", err
	}
	uuid := strings.TrimSpace(string(out))
	if uuid == "" {
		return "", fmt.Errorf("empty snapshot uuid for %s", snapshotPath)
	}
	dir := snapshotCacheDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	data, err := os.ReadFile(snapshotPath)
	if err != nil {
		return "", err
	}
	dst := filepath.Join(dir, uuid+".snapshot")
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return "", err
	}
	return uuid, nil
}
