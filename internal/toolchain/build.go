package toolchain

import (
	"os"
	"path/filepath"

	"github.com/davidg238/porta/devsdk/exec"
)

// Build compiles a Toit app to a snapshot, relocates it to a 32-bit binary
// container image, and returns the image bytes, the on-disk snapshot path, and a
// cleanup func the caller must invoke (it removes the temp dir holding the
// snapshot). The snapshot is retained until cleanup so the caller can pass it to
// RetainSnapshot for panic decoding. All current ESP32 chips are 32-bit, so the
// relocation is `-m32 --format=binary`; the image couples to the active SDK
// version, checked separately via CheckSDK.
func Build(ex *exec.Executor, appPath string) (img []byte, snapshotPath string, cleanup func(), err error) {
	noop := func() {}
	dir, err := os.MkdirTemp("", "porta-build-")
	if err != nil {
		return nil, "", noop, err
	}
	cleanup = func() { os.RemoveAll(dir) }

	snap := filepath.Join(dir, "app.snapshot")
	bin := filepath.Join(dir, "app.bin")

	if _, err := ex.Run("compile "+filepath.Base(appPath), "toit",
		"compile", "--snapshot", "-o", snap, appPath); err != nil {
		cleanup()
		return nil, "", noop, err
	}
	if _, err := ex.Run("relocate (esp32, -m32)", "toit",
		"tool", "snapshot-to-image", "-m32", "--format=binary", "-o", bin, snap); err != nil {
		cleanup()
		return nil, "", noop, err
	}
	b, err := os.ReadFile(bin)
	if err != nil {
		cleanup()
		return nil, "", noop, err
	}
	return b, snap, cleanup, nil
}
