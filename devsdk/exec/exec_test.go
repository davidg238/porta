package exec

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// fakeRunner records invocations and returns canned results.
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

func TestExecutorNarratesAndRuns(t *testing.T) {
	fr := &fakeRunner{results: map[string]runResult{"toit": {stdout: []byte("ok")}}}
	var log bytes.Buffer
	ex := NewExecutor(fr, &log, false)
	out, err := ex.Run("compile", "toit", "compile", "x.toit")
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "ok" {
		t.Errorf("out=%q, want ok", out)
	}
	if len(fr.calls) != 1 || fr.calls[0][0] != "toit" {
		t.Fatalf("calls=%v", fr.calls)
	}
	// Default (non-verbose) narration announces the step label and the argv.
	s := log.String()
	if !strings.Contains(s, "compile") || !strings.Contains(s, "toit compile x.toit") {
		t.Errorf("narration missing label/argv: %q", s)
	}
}

func TestExecutorReportsFailureWithCommand(t *testing.T) {
	fr := &fakeRunner{results: map[string]runResult{"toit": {err: errors.New("boom")}}}
	var log bytes.Buffer
	ex := NewExecutor(fr, &log, false)
	_, err := ex.Run("compile", "toit", "compile", "x.toit")
	if err == nil {
		t.Fatal("expected error")
	}
	// On failure the narration includes the rerunnable command.
	if !strings.Contains(log.String(), "toit compile x.toit") {
		t.Errorf("failure narration missing command: %q", log.String())
	}
}
