// Copyright (c) 2026 Ekorau LLC

package toolchain

import (
	"bytes"
	"strings"
	"testing"

	"github.com/davidg238/porta/devsdk/exec"
)

// fakeRunner records invocations and returns canned results.
// (Mirrors the one in devsdk/exec/exec_test.go which cannot be imported here.)
type fakeRunner struct {
	calls   [][]string
	results map[string]runResult // keyed by argv[0]
}
type runResult struct {
	stdout []byte
	err    error
}

func (f *fakeRunner) Run(name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	r := f.results[name]
	return r.stdout, r.err
}

func TestSDKVersionParsesToitVersion(t *testing.T) {
	fr := &fakeRunner{results: map[string]runResult{"toit": {stdout: []byte("v2.0.0-alpha.192\n")}}}
	ex := exec.NewExecutor(fr, &bytes.Buffer{}, false)
	v, err := SDKVersion(ex)
	if err != nil {
		t.Fatal(err)
	}
	if v != "v2.0.0-alpha.192" {
		t.Errorf("got %q, want v2.0.0-alpha.192", v)
	}
}

func TestCheckSDK(t *testing.T) {
	if err := CheckSDK("v2.0.0-alpha.192", "v2.0.0-alpha.192"); err != nil {
		t.Errorf("match should pass: %v", err)
	}
	err := CheckSDK("v2.0.0-alpha.192", "v2.0.0-alpha.999")
	if err == nil {
		t.Fatal("mismatch should error")
	}
	if !strings.Contains(err.Error(), "v2.0.0-alpha.192") || !strings.Contains(err.Error(), "v2.0.0-alpha.999") {
		t.Errorf("error should name both versions: %v", err)
	}
}
