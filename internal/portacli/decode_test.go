package portacli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/davidg238/porta/devsdk/apiclient"
)

// recordingRunner satisfies exec.Runner; returns canned output and records argv.
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
	_, err := d.Decode("BLOB")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "No such file") {
		t.Errorf("error should surface stderr, got %q", err.Error())
	}
}

func TestJagDecoderErrorEmptyOutput(t *testing.T) {
	rr := &recordingRunner{out: nil, err: errors.New("exit status 1")}
	d := jagDecoder{r: rr}
	_, err := d.Decode("BLOB")
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.HasSuffix(err.Error(), ": ") {
		t.Errorf("error should not end with a dangling colon, got %q", err.Error())
	}
}

// localDecoder: ok=true returns a canned 2-line trace; ok=false fails (no snapshot).
type localDecoder struct{ ok bool }

func (d localDecoder) Decode(blob string) (string, error) {
	if !d.ok {
		return "", errors.New("no snapshot")
	}
	return "UNHANDLED EXCEPTION: OUT_OF_BOUNDS\n  at main.foo", nil
}

func TestRenderPanicDecoded(t *testing.T) {
	var b bytes.Buffer
	renderPanic(&b, apiclient.DataRow{TS: 100, Kind: "panic", Text: "BLOB"}, localDecoder{ok: true})
	s := b.String()
	if !strings.Contains(s, "‼ PANIC") || !strings.Contains(s, "  at main.foo") {
		t.Errorf("got %q", s)
	}
	if strings.Contains(s, "jag decode BLOB") {
		t.Errorf("decoded output should not show the raw blob: %q", s)
	}
}

// trailingNLDecoder returns a trace that ends with a newline.
type trailingNLDecoder struct{}

func (trailingNLDecoder) Decode(string) (string, error) {
	return "UNHANDLED EXCEPTION\n  at main.foo\n", nil
}

func TestRenderPanicNoTrailingBlankLine(t *testing.T) {
	var b bytes.Buffer
	renderPanic(&b, apiclient.DataRow{TS: 100, Kind: "panic", Text: "BLOB"}, trailingNLDecoder{})
	s := b.String()
	// Exactly one trailing newline; no dangling indented blank line.
	if strings.HasSuffix(s, "  \n") {
		t.Errorf("dangling indented blank line: %q", s)
	}
	if !strings.HasSuffix(s, "at main.foo\n") {
		t.Errorf("got %q", s)
	}
}

func TestRenderPanicFallback(t *testing.T) {
	var b bytes.Buffer
	renderPanic(&b, apiclient.DataRow{TS: 100, Kind: "panic", Text: "BLOB"}, localDecoder{ok: false})
	s := b.String()
	if !strings.Contains(s, "no local snapshot") || !strings.Contains(s, "jag decode BLOB") {
		t.Errorf("got %q", s)
	}
}

func TestPanicSummary(t *testing.T) {
	ok := panicSummary(apiclient.DataRow{Text: "BLOB"}, localDecoder{ok: true})
	if ok != "UNHANDLED EXCEPTION: OUT_OF_BOUNDS" {
		t.Errorf("ok summary = %q", ok)
	}
	bad := panicSummary(apiclient.DataRow{Text: "BLOB"}, localDecoder{ok: false})
	if !strings.Contains(bad, "no local snapshot") {
		t.Errorf("fallback summary = %q", bad)
	}
}
