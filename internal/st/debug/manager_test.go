package debug

import (
	"testing"

	"github.com/davidg238/porta/internal/st/store"
)

func testManager(t *testing.T) *Manager {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return NewManager(st)
}

func TestManagerStoreSourceMap(t *testing.T) {
	m := testManager(t)
	err := m.StoreSourceMap("aabb", "sensor", []byte(testStmap), "| x |\nx := 1.")
	if err != nil {
		t.Fatalf("StoreSourceMap: %v", err)
	}
	sm := m.GetSourceMap("aabb", "sensor")
	if sm == nil {
		t.Fatal("expected source map")
	}
	if sm.Source != "sensor.st" {
		t.Errorf("Source = %q", sm.Source)
	}
	src := m.GetSource("aabb", "sensor")
	if src != "| x |\nx := 1." {
		t.Errorf("Source text = %q", src)
	}
}

func TestManagerSetBreakpoint(t *testing.T) {
	m := testManager(t)
	_ = m.StoreSourceMap("aabb", "sensor", []byte(testStmap), "")

	err := m.SetBreakpoint("aabb", "sensor", 2)
	if err != nil {
		t.Fatalf("SetBreakpoint: %v", err)
	}

	bps, _ := m.store.ListDebugBreakpoints("aabb")
	if len(bps) != 1 {
		t.Fatalf("expected 1 bp, got %d", len(bps))
	}
	if bps[0].PCStart != 3 || bps[0].PCEnd != 6 {
		t.Errorf("bp PC range = [%d, %d], want [3, 6]", bps[0].PCStart, bps[0].PCEnd)
	}
}

func TestManagerTranslateState(t *testing.T) {
	m := testManager(t)
	_ = m.StoreSourceMap("aabb", "sensor", []byte(testStmap), "line 1\nline 2\nline 3")

	line, ok := m.PCToSTLine("aabb", "sensor", "sensor", 4)
	if !ok {
		t.Fatal("PCToSTLine returned false")
	}
	if line != 2 {
		t.Errorf("line = %d, want 2", line)
	}
}
