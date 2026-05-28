// internal/portacli/monitor_test.go
package portacli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/davidg238/porta/internal/store"
)

func seededStore(t *testing.T, dev string) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/m.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	st.InsertData(dev, 100, 0, "metric", "pm", int64(13), "", "int")
	st.InsertData(dev, 101, 1, "metric", "t", float64(20.5), "", "float")
	st.InsertData(dev, 102, 2, "metric", "door", int64(1), "", "bool")
	st.InsertData(dev, 103, 3, "metric", "mode", nil, "auto", "string")
	st.InsertData(dev, 104, 4, "log", "", nil, "started blink", "")
	return st
}

func TestRunMonitorRangePrintsAllScalars(t *testing.T) {
	dev := "aabbccddeeff"
	st := seededStore(t, dev)
	var out bytes.Buffer
	now := func() int64 { return 200 }
	if err := runMonitor(context.Background(), &out, st, dev, 200, "", false, now, 10*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 5 {
		t.Fatalf("got %d lines, want 5: %q", len(lines), out.String())
	}
	wants := []string{
		"100  metric  pm=13",
		"101  metric  t=20.5",
		"102  metric  door=true",
		"103  metric  mode=auto",
		"104  log     started blink",
	}
	for i, w := range wants {
		if lines[i] != w {
			t.Errorf("line %d = %q, want %q", i, lines[i], w)
		}
	}
}

func TestRunMonitorKindFilter(t *testing.T) {
	dev := "ffeeddccbbaa"
	st := seededStore(t, dev)
	var out bytes.Buffer
	now := func() int64 { return 200 }
	if err := runMonitor(context.Background(), &out, st, dev, 200, "log", false, now, 10*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], "started blink") {
		t.Errorf("kind=log filter: lines=%v", lines)
	}
}

func TestRunMonitorFollowExitsOnCancel(t *testing.T) {
	dev := "112233445566"
	st := seededStore(t, dev)
	var out bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	// Cancel after a few poll intervals — the loop must return promptly.
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()
	now := func() int64 { return 200 }
	done := make(chan error, 1)
	go func() {
		done <- runMonitor(ctx, &out, st, dev, 200, "", true, now, 10*time.Millisecond)
	}()
	select {
	case err := <-done:
		if err != nil && err != context.Canceled {
			t.Fatalf("runMonitor returned %v, want nil or context.Canceled", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("runMonitor --follow did not exit after cancel")
	}
}
