package portacli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/davidg238/porta/internal/apiclient"
)

func TestRunPanicListNewestFirst(t *testing.T) {
	// fakeReader returns rows ascending (oldest first); list must show newest first.
	f := &fakeReader{window: []apiclient.DataRow{
		{ID: 10, TS: 100, Kind: "panic", Text: "A"},
		{ID: 11, TS: 200, Kind: "panic", Text: "B"},
	}}
	var out bytes.Buffer
	if err := runPanicList(&out, f, fakeDecoder{ok: true}, "dev", 86400, 20, func() int64 { return 1000 }); err != nil {
		t.Fatal(err)
	}
	if f.windowKind != "panic" {
		t.Errorf("expected kind=panic filter, got %q", f.windowKind)
	}
	s := out.String()
	if !strings.Contains(s, "ID") || !strings.Contains(s, "11") || !strings.Contains(s, "OUT_OF_BOUNDS") {
		t.Errorf("got %q", s)
	}
	if i10, i11 := strings.Index(s, "\n10"), strings.Index(s, "\n11"); i11 < 0 || i10 < 0 || i11 > i10 {
		t.Errorf("expected newest (11) before oldest (10): %q", s)
	}
}

func TestRunPanicListFallbackSummary(t *testing.T) {
	f := &fakeReader{window: []apiclient.DataRow{
		{ID: 10, TS: 100, Kind: "panic", Text: "A"},
	}}
	var out bytes.Buffer
	if err := runPanicList(&out, f, fakeDecoder{ok: false}, "dev", 86400, 20, func() int64 { return 1000 }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no local snapshot") {
		t.Errorf("got %q", out.String())
	}
}

func TestRunPanicListEmpty(t *testing.T) {
	f := &fakeReader{}
	var out bytes.Buffer
	if err := runPanicList(&out, f, fakeDecoder{ok: true}, "dev", 86400, 20, func() int64 { return 1000 }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no panics") {
		t.Errorf("got %q", out.String())
	}
}

func TestRunPanicListLimitKeepsNewest(t *testing.T) {
	f := &fakeReader{window: []apiclient.DataRow{
		{ID: 1, TS: 100, Kind: "panic", Text: "A"},
		{ID: 2, TS: 200, Kind: "panic", Text: "B"},
		{ID: 3, TS: 300, Kind: "panic", Text: "C"},
	}}
	var out bytes.Buffer
	if err := runPanicList(&out, f, fakeDecoder{ok: true}, "dev", 86400, 2, func() int64 { return 1000 }); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if strings.Contains(s, "\n1\t") || !strings.Contains(s, "\n2") || !strings.Contains(s, "\n3") {
		t.Errorf("limit=2 should keep newest two (2,3) and drop 1: %q", s)
	}
}
