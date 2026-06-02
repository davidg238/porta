package toolchain

import (
	"os"
	"path/filepath"
)

// Build compiles a Toit app to a snapshot, relocates it to a 32-bit binary
// container image, and returns the image bytes. All current ESP32 chips are
// 32-bit, so the relocation is `-m32 --format=binary` (the recipe nodus uses in
// host/build-envelope.sh); the image couples to the active SDK version, checked
// separately via CheckSDK. Temp artifacts are cleaned up.
func Build(ex *Executor, appPath string) ([]byte, error) {
	dir, err := os.MkdirTemp("", "porta-build-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	snap := filepath.Join(dir, "app.snapshot")
	img := filepath.Join(dir, "app.bin")

	if _, err := ex.Run("compile "+filepath.Base(appPath), "toit",
		"compile", "--snapshot", "-o", snap, appPath); err != nil {
		return nil, err
	}
	if _, err := ex.Run("relocate (esp32, -m32)", "toit",
		"tool", "snapshot-to-image", "-m32", "--format=binary", "-o", img, snap); err != nil {
		return nil, err
	}
	return os.ReadFile(img)
}
