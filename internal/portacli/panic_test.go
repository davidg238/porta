package portacli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/davidg238/porta/internal/apiclient"
)

// echoPanicDecoder echoes the blob so tests can assert which row was selected.
type echoPanicDecoder struct{}

func (echoPanicDecoder) Decode(blob string) (string, error) { return "trace for " + blob, nil }

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

func TestRunPanicShowMostRecent(t *testing.T) {
	f := &fakeReader{window: []apiclient.DataRow{
		{ID: 10, TS: 100, Kind: "panic", Text: "A"},
		{ID: 11, TS: 200, Kind: "panic", Text: "B"},
	}}
	var out bytes.Buffer
	if err := runPanicShow(&out, f, echoPanicDecoder{}, "dev", 86400, 0, func() int64 { return 1000 }); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "‼ PANIC") || !strings.Contains(s, "trace for B") {
		t.Errorf("expected the most-recent row (ID 11, blob B), got %q", s)
	}
	if strings.Contains(s, "trace for A") {
		t.Errorf("should not have shown the older row (ID 10): %q", s)
	}
}

func TestRunPanicShowByID(t *testing.T) {
	f := &fakeReader{window: []apiclient.DataRow{
		{ID: 10, TS: 100, Kind: "panic", Text: "A"},
		{ID: 11, TS: 200, Kind: "panic", Text: "B"},
	}}
	var out bytes.Buffer
	if err := runPanicShow(&out, f, echoPanicDecoder{}, "dev", 86400, 10, func() int64 { return 1000 }); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "trace for A") {
		t.Errorf("expected the row selected by id=10 (blob A), got %q", s)
	}
	if strings.Contains(s, "trace for B") {
		t.Errorf("should not have shown a different row: %q", s)
	}
}

func TestRunPanicShowUnknownID(t *testing.T) {
	f := &fakeReader{window: []apiclient.DataRow{
		{ID: 10, TS: 100, Kind: "panic", Text: "A"},
	}}
	var out bytes.Buffer
	if err := runPanicShow(&out, f, fakeDecoder{ok: true}, "dev", 86400, 999, func() int64 { return 1000 }); err == nil {
		t.Fatal("expected error for unknown id")
	}
}

func TestRunPanicShowEmpty(t *testing.T) {
	f := &fakeReader{}
	var out bytes.Buffer
	if err := runPanicShow(&out, f, fakeDecoder{ok: true}, "dev", 86400, 0, func() int64 { return 1000 }); err == nil {
		t.Fatal("expected error when there are no panics")
	}
}
