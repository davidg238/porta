package portacli

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/davidg238/porta/internal/store"
	"github.com/davidg238/porta/internal/toolchain"
)

// stubRunner: `toit version` → fixed SDK; snapshot-to-image -o → canned bytes.
type stubRunner struct{ sdk string }

func (s stubRunner) Run(name string, args ...string) ([]byte, error) {
	for i := 0; i < len(args); i++ {
		if args[i] == "version" {
			return []byte(s.sdk + "\n"), nil
		}
	}
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-o" {
			for _, a := range args {
				if a == "snapshot-to-image" {
					_ = os.WriteFile(args[i+1], []byte("IMG"), 0o600)
				}
			}
		}
	}
	return nil, nil
}

func newRunStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/d.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestRunDeployHappyPath(t *testing.T) {
	st := newRunStore(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	st.UpdateNodeIdentity("aabbccddeeff", "esp32", "v2.0.0-alpha.192")
	ex := toolchain.NewExecutor(stubRunner{sdk: "v2.0.0-alpha.192"}, &bytes.Buffer{}, false)

	var buf bytes.Buffer
	err := runDeploy(&buf, st, ex, "aabbccddeeff", "/tmp/app.toit",
		deployOpts{Name: "blink", Lifecycle: "run-loop", Triggers: []string{"boot"}, Runlevel: 3}, false, 2000)
	if err != nil {
		t.Fatalf("runDeploy: %v", err)
	}
	// A run command must now be queued.
	cmd, err := st.NextUndelivered("aabbccddeeff")
	if err != nil || cmd == nil || cmd.Verb != "run" {
		t.Fatalf("expected queued run, got %+v (err %v)", cmd, err)
	}
	// Confirm success message was written to out.
	if got := buf.String(); !strings.Contains(got, "enqueued run") {
		t.Errorf("expected confirmation containing %q, got %q", "enqueued run", got)
	}
}

func TestRunDeployBlocksOnUnknownIdentity(t *testing.T) {
	st := newRunStore(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000) // no UpdateNodeIdentity
	ex := toolchain.NewExecutor(stubRunner{sdk: "v2.0.0-alpha.192"}, &bytes.Buffer{}, false)
	err := runDeploy(&bytes.Buffer{}, st, ex, "aabbccddeeff", "/tmp/app.toit",
		deployOpts{Name: "blink", Lifecycle: "run-once"}, false, 2000)
	if err == nil {
		t.Fatal("expected block on unknown identity")
	}
}

func TestRunDeployRefusesSDKMismatch(t *testing.T) {
	st := newRunStore(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	st.UpdateNodeIdentity("aabbccddeeff", "esp32", "v2.0.0-alpha.192")
	ex := toolchain.NewExecutor(stubRunner{sdk: "v9.9.9"}, &bytes.Buffer{}, false)
	err := runDeploy(&bytes.Buffer{}, st, ex, "aabbccddeeff", "/tmp/app.toit",
		deployOpts{Name: "blink", Lifecycle: "run-once"}, false, 2000)
	if err == nil {
		t.Fatal("expected SDK mismatch refusal")
	}
	// --force overrides.
	if err := runDeploy(&bytes.Buffer{}, st, ex, "aabbccddeeff", "/tmp/app.toit",
		deployOpts{Name: "blink", Lifecycle: "run-once"}, true, 2000); err != nil {
		t.Errorf("--force should override: %v", err)
	}
}
