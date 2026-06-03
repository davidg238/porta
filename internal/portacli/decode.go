// internal/portacli/decode.go
package portacli

import (
	"fmt"
	"strings"

	"github.com/davidg238/porta/internal/toolchain"
)

// panicDecoder symbolicates a base64 trace blob into a readable stack trace.
// The seam keeps runMonitor and the panic commands unit-testable with a fake.
type panicDecoder interface {
	Decode(blob string) (string, error)
}

// jagDecoder shells out to `jag decode <blob>`, which resolves the blob's
// embedded program UUID against jag's local snapshot cache (populated by
// `porta run`, see toolchain.RetainSnapshot). It uses a plain Runner (not the
// narrating Executor) so decode adds no "→ jag decode …" noise to monitor output.
type jagDecoder struct{ r toolchain.Runner }

// newJagDecoder builds the production decoder over os/exec.
func newJagDecoder() jagDecoder { return jagDecoder{r: toolchain.ExecRunner{}} }

func (d jagDecoder) Decode(blob string) (string, error) {
	out, err := d.r.Run("jag", "decode", blob)
	if err != nil {
		return "", fmt.Errorf("jag decode: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}
