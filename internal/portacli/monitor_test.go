// Copyright (c) 2026 Ekorau LLC

// internal/portacli/monitor_test.go
package portacli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/davidg238/porta/devsdk/apiclient"
	"github.com/davidg238/porta/internal/apisrv"
	"github.com/davidg238/porta/internal/store"
)

// fakeReader is an in-memory telemetryReader. Window returns `window`; each
// After call pops the next batch from `after` and records the cursor it saw.
type fakeReader struct {
	window     []apiclient.DataRow
	windowKind string
	after      [][]apiclient.DataRow
	afterCalls []int64
}

func (f *fakeReader) QueryTelemetryWindow(sel string, since, until int64, kind string, limit int) ([]apiclient.DataRow, error) {
	f.windowKind = kind
	return f.window, nil
}

func (f *fakeReader) QueryTelemetryAfter(sel string, after int64, kind string, limit int) ([]apiclient.DataRow, error) {
	f.afterCalls = append(f.afterCalls, after)
	if len(f.after) == 0 {
		return nil, nil
	}
	batch := f.after[0]
	f.after = f.after[1:]
	return batch, nil
}

func dr(id, ts int64, name string, value any, vtype string) apiclient.DataRow {
	return apiclient.DataRow{ID: id, TS: ts, Seq: id, Kind: "metric", Name: name, Value: value, ValueType: vtype}
}

func TestRunMonitorWindowPrintsAllScalars(t *testing.T) {
	f := &fakeReader{window: []apiclient.DataRow{
		dr(1, 100, "pm", int64(13), "int"),
		dr(2, 101, "t", float64(20.5), "float"),
		dr(3, 102, "door", int64(1), "bool"),
		{ID: 4, TS: 103, Kind: "metric", Name: "mode", Value: nil, Text: "auto", ValueType: "string"},
		{ID: 5, TS: 104, Kind: "log", Text: "started blink"},
	}}
	var out bytes.Buffer
	now := func() int64 { return 200 }
	if err := runMonitor(context.Background(), &out, f, "dev", 200, "", false, now, 10*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"100  metric  pm=13",
		"101  metric  t=20.5",
		"102  metric  door=true",
		"103  metric  mode=auto",
		"104  log     started blink",
	}
	got := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d: %q", len(got), len(want), out.String())
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRunMonitorFollowDedupsByID(t *testing.T) {
	f := &fakeReader{
		window: []apiclient.DataRow{dr(1, 100, "pm", int64(13), "int"), dr(2, 101, "pm", int64(14), "int")},
		after:  [][]apiclient.DataRow{{dr(3, 102, "pm", int64(15), "int")}},
	}
	var out bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	now := func() int64 { return 200 }
	done := make(chan error, 1)
	go func() {
		done <- runMonitor(ctx, &out, f, "dev", 200, "", true, now, 5*time.Millisecond)
	}()
	// Give the loop time to poll at least twice, then cancel.
	time.Sleep(40 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runMonitor returned %v, want nil", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("runMonitor --follow did not exit after cancel")
	}
	// id=3 printed exactly once (no re-print across polls).
	if n := strings.Count(out.String(), "pm=15"); n != 1 {
		t.Fatalf("pm=15 printed %d times, want 1\n%s", n, out.String())
	}
	// First After poll uses the window's max id (2), not 0.
	if len(f.afterCalls) == 0 || f.afterCalls[0] != 2 {
		t.Fatalf("first after cursor = %v, want 2", f.afterCalls)
	}
}

func TestRunMonitorKindFilterPassedThrough(t *testing.T) {
	f := &fakeReader{window: []apiclient.DataRow{{ID: 1, TS: 100, Kind: "log", Text: "hi"}}}
	var out bytes.Buffer
	now := func() int64 { return 200 }
	if err := runMonitor(context.Background(), &out, f, "dev", 200, "log", false, now, 10*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "hi") {
		t.Errorf("out = %q", out.String())
	}
	if f.windowKind != "log" {
		t.Errorf("window kind = %q, want log", f.windowKind)
	}
}

// monitorTestServer stands up a real apisrv over a temp store and returns the
// store + the httptest server URL. Distinct name avoids collision with
// newClientServer (mutate_test.go) and newRunClientServer (run_test.go).
func monitorTestServer(t *testing.T) (*store.Store, string) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/mon.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	mux := http.NewServeMux()
	apisrv.New(st).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return st, srv.URL
}

func TestMonitorCmdE2EOverAPI(t *testing.T) {
	st, srvURL := monitorTestServer(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	// Use a current-ish timestamp so the default 1h look-back window includes it.
	ts := nowSec() - 30
	st.InsertData("aabbccddeeff", ts, 0, "metric", "pm", int64(42), "", "int")

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"--server", srvURL, "monitor", "-d", "aabbccddeeff", "--since", "1h"})
	if err := root.Execute(); err != nil {
		t.Fatalf("monitor cmd: %v", err)
	}
	if !strings.Contains(out.String(), "metric  pm=42") {
		t.Fatalf("out = %q", out.String())
	}
}
