package portacli

import (
	"errors"
	"strings"
	"testing"
)

// recordingRunner satisfies toolchain.Runner; returns canned output and records argv.
type recordingRunner struct {
	out  []byte
	err  error
	argv []string
}

func (r *recordingRunner) Run(name string, args ...string) ([]byte, error) {
	r.argv = append([]string{name}, args...)
	return r.out, r.err
}

func TestJagDecoderSuccess(t *testing.T) {
	rr := &recordingRunner{out: []byte("UNHANDLED EXCEPTION: OUT_OF_BOUNDS\n  at main\n")}
	d := jagDecoder{r: rr}
	got, err := d.Decode("BLOB")
	if err != nil {
		t.Fatal(err)
	}
	if got != "UNHANDLED EXCEPTION: OUT_OF_BOUNDS\n  at main" {
		t.Errorf("got %q", got)
	}
	if strings.Join(rr.argv, " ") != "jag decode BLOB" {
		t.Errorf("argv = %v", rr.argv)
	}
}

func TestJagDecoderError(t *testing.T) {
	rr := &recordingRunner{out: []byte("No such file"), err: errors.New("exit status 1")}
	d := jagDecoder{r: rr}
	if _, err := d.Decode("BLOB"); err == nil {
		t.Fatal("expected error")
	}
}
