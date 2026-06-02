package toolchain

import (
	"bytes"
	"os"
	"testing"
)

// fileWritingRunner extends fakeRunner: when it sees a `-o <path>` arg, it
// writes canned image bytes there (simulating snapshot-to-image output).
type fileWritingRunner struct {
	calls    [][]string
	imgBytes []byte
}

func (f *fileWritingRunner) Run(name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-o" && len(f.imgBytes) > 0 && hasArg(args, "snapshot-to-image") {
			if err := os.WriteFile(args[i+1], f.imgBytes, 0o600); err != nil {
				return nil, err
			}
		}
	}
	return nil, nil
}
func hasArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func TestBuildCompilesAndRelocates(t *testing.T) {
	fr := &fileWritingRunner{imgBytes: []byte("IMAGEBYTES")}
	ex := NewExecutor(fr, &bytes.Buffer{}, false)
	img, err := Build(ex, "/tmp/app.toit")
	if err != nil {
		t.Fatal(err)
	}
	if string(img) != "IMAGEBYTES" {
		t.Errorf("got %q, want IMAGEBYTES", img)
	}
	// Expect a compile step then a snapshot-to-image -m32 --format=binary step.
	if len(fr.calls) != 2 {
		t.Fatalf("calls=%v", fr.calls)
	}
	if !hasArg(fr.calls[0], "compile") || !hasArg(fr.calls[0], "--snapshot") {
		t.Errorf("first call not a snapshot compile: %v", fr.calls[0])
	}
	c2 := fr.calls[1]
	if !hasArg(c2, "snapshot-to-image") || !hasArg(c2, "-m32") || !hasArg(c2, "--format=binary") {
		t.Errorf("second call not snapshot-to-image -m32 binary: %v", c2)
	}
}
