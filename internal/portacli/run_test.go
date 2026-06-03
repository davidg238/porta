package portacli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/davidg238/porta/devsdk/apiclient"
	"github.com/davidg238/porta/devsdk/exec"
	"github.com/davidg238/porta/internal/apisrv"
	"github.com/davidg238/porta/internal/store"
)

// stubRunner: `toit version` → fixed SDK; `snapshot uuid` → fixed uuid (or empty
// when badUUID); compile/-o and snapshot-to-image/-o write canned files.
type stubRunner struct {
	sdk     string
	badUUID bool
}

func (s stubRunner) Run(name string, args ...string) ([]byte, error) {
	for i := 0; i < len(args); i++ {
		if args[i] == "version" {
			return []byte(s.sdk + "\n"), nil
		}
		if args[i] == "uuid" {
			if s.badUUID {
				return []byte("\n"), nil
			}
			return []byte("deadbeef-uuid\n"), nil
		}
	}
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-o" {
			if runArgsContain(args, "compile") {
				_ = os.WriteFile(args[i+1], []byte("SNAP"), 0o600)
			}
			if runArgsContain(args, "snapshot-to-image") {
				_ = os.WriteFile(args[i+1], []byte("IMG"), 0o600)
			}
		}
	}
	return nil, nil
}

func runArgsContain(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

// newRunClientServer stands up the REAL apisrv.Handler over a temp store behind
// an httptest server and returns a client pointed at it plus the store (so tests
// can seed identity and assert what landed). Mirrors mutate_test's harness.
func newRunClientServer(t *testing.T) (*apiclient.Client, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/d.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	mux := http.NewServeMux()
	apisrv.New(st).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return apiclient.New(srv.URL), st
}

func TestRunDeployHappyPath(t *testing.T) {
	c, st := newRunClientServer(t)
	t.Setenv("PORTA_SNAPSHOT_DIR", t.TempDir())
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	st.UpdateNodeIdentity("aabbccddeeff", "esp32", "v2.0.0-alpha.192")
	ex := exec.NewExecutor(stubRunner{sdk: "v2.0.0-alpha.192"}, &bytes.Buffer{}, false)

	var buf bytes.Buffer
	err := runDeploy(&buf, c, ex, "aabbccddeeff", "/tmp/app.toit",
		deployOpts{Name: "blink", Lifecycle: "run-loop", Triggers: []string{"boot"}, Runlevel: 3}, false)
	if err != nil {
		t.Fatalf("runDeploy: %v", err)
	}
	// A run command must now be queued, stamped issued_by="api".
	cmd, err := st.NextUndelivered("aabbccddeeff")
	if err != nil || cmd == nil || cmd.Verb != "run" {
		t.Fatalf("expected queued run, got %+v (err %v)", cmd, err)
	}
	rows, _ := st.RecentCommandsForDevice("aabbccddeeff", 10)
	if len(rows) == 0 || rows[0].IssuedBy != "api" {
		t.Errorf("expected issued_by=api, got %+v", rows)
	}
	// Confirmation leads with the resolved node id.
	if got := buf.String(); !strings.Contains(got, "aabbccddeeff: built blink") || !strings.Contains(got, "enqueued run") {
		t.Errorf("confirmation = %q", got)
	}
}

func TestRunDeployBlocksOnUnknownIdentity(t *testing.T) {
	c, st := newRunClientServer(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000) // no UpdateNodeIdentity → sdk=""
	ex := exec.NewExecutor(stubRunner{sdk: "v2.0.0-alpha.192"}, &bytes.Buffer{}, false)
	// Blocked even with force=true (force overrides mismatch, not unknown identity).
	if err := runDeploy(&bytes.Buffer{}, c, ex, "aabbccddeeff", "/tmp/app.toit",
		deployOpts{Name: "blink", Lifecycle: "run-once"}, true); err == nil {
		t.Fatal("expected block on unknown identity even with force")
	}
}

func TestRunDeployRefusesSDKMismatch(t *testing.T) {
	c, st := newRunClientServer(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	st.UpdateNodeIdentity("aabbccddeeff", "esp32", "v2.0.0-alpha.192")
	ex := exec.NewExecutor(stubRunner{sdk: "v9.9.9"}, &bytes.Buffer{}, false)
	if err := runDeploy(&bytes.Buffer{}, c, ex, "aabbccddeeff", "/tmp/app.toit",
		deployOpts{Name: "blink", Lifecycle: "run-once"}, false); err == nil {
		t.Fatal("expected SDK mismatch refusal")
	}
	// --force overrides the mismatch and the run lands.
	if err := runDeploy(&bytes.Buffer{}, c, ex, "aabbccddeeff", "/tmp/app.toit",
		deployOpts{Name: "blink", Lifecycle: "run-once"}, true); err != nil {
		t.Errorf("--force should override: %v", err)
	}
	cmd, _ := st.NextUndelivered("aabbccddeeff")
	if cmd == nil || cmd.Verb != "run" {
		t.Fatalf("expected queued run after --force, got %+v", cmd)
	}
}

func TestRunDeploySetsPowerMode(t *testing.T) {
	c, st := newRunClientServer(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	st.UpdateNodeIdentity("aabbccddeeff", "esp32", "v2.0.0-alpha.192")
	ex := exec.NewExecutor(stubRunner{sdk: "v2.0.0-alpha.192"}, &bytes.Buffer{}, false)

	err := runDeploy(&bytes.Buffer{}, c, ex, "aabbccddeeff", "/tmp/app.toit",
		deployOpts{Name: "blink", Lifecycle: "run-loop", PowerMode: "always-on"}, false)
	if err != nil {
		t.Fatalf("runDeploy: %v", err)
	}
	// Two commands should be queued: the run (install) and a set-power-mode.
	rows, err := st.RecentCommandsForDevice("aabbccddeeff", 10)
	if err != nil {
		t.Fatal(err)
	}
	var sawRun, sawPower bool
	for _, r := range rows {
		switch r.Verb {
		case "run":
			sawRun = true
		case "set-power-mode":
			sawPower = true
		}
	}
	if !sawRun || !sawPower {
		t.Errorf("expected both run and set-power-mode queued, got %+v", rows)
	}
}

func TestNewRunCmdRegistersFlags(t *testing.T) {
	cmd := newRunCmd()
	if cmd.Use == "" || cmd.Flags().Lookup("device") == nil {
		t.Fatal("run cmd missing device flag")
	}
	for _, f := range []string{"name", "lifecycle", "trigger", "runlevel", "power-mode", "force", "verbose"} {
		if cmd.Flags().Lookup(f) == nil {
			t.Errorf("missing --%s flag", f)
		}
	}
}

func TestRunDeployRetainsSnapshot(t *testing.T) {
	c, st := newRunClientServer(t)
	cache := t.TempDir()
	t.Setenv("PORTA_SNAPSHOT_DIR", cache)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	st.UpdateNodeIdentity("aabbccddeeff", "esp32", "v2.0.0-alpha.192")
	ex := exec.NewExecutor(stubRunner{sdk: "v2.0.0-alpha.192"}, &bytes.Buffer{}, false)

	var buf bytes.Buffer
	if err := runDeploy(&buf, c, ex, "aabbccddeeff", "/tmp/app.toit",
		deployOpts{Name: "blink", Lifecycle: "run-loop", Triggers: []string{"boot"}, Runlevel: 3}, false); err != nil {
		t.Fatalf("runDeploy: %v", err)
	}
	if _, err := os.Stat(cache + "/deadbeef-uuid.snapshot"); err != nil {
		t.Errorf("expected retained snapshot in cache: %v", err)
	}
}

func TestRunDeployRetentionFailureIsNonFatal(t *testing.T) {
	c, st := newRunClientServer(t)
	t.Setenv("PORTA_SNAPSHOT_DIR", t.TempDir())
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	st.UpdateNodeIdentity("aabbccddeeff", "esp32", "v2.0.0-alpha.192")
	ex := exec.NewExecutor(stubRunner{sdk: "v2.0.0-alpha.192", badUUID: true}, &bytes.Buffer{}, false)

	var buf bytes.Buffer
	if err := runDeploy(&buf, c, ex, "aabbccddeeff", "/tmp/app.toit",
		deployOpts{Name: "blink", Lifecycle: "run-loop", Triggers: []string{"boot"}, Runlevel: 3}, false); err != nil {
		t.Fatalf("runDeploy should succeed despite retention failure: %v", err)
	}
	if !strings.Contains(buf.String(), "warning") {
		t.Errorf("expected a retention warning, got %q", buf.String())
	}
	if cmd, err := st.NextUndelivered("aabbccddeeff"); err != nil || cmd == nil || cmd.Verb != "run" {
		t.Fatalf("expected queued run despite retention failure, got %+v (err %v)", cmd, err)
	}
}
