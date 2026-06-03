package toolchain

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// uuidRunner returns a fixed UUID for `toit tool snapshot uuid`.
type uuidRunner struct{ uuid string }

func (u uuidRunner) Run(name string, args ...string) ([]byte, error) {
	return []byte(u.uuid + "\n"), nil
}

func TestRetainSnapshotCopiesToCache(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("PORTA_SNAPSHOT_DIR", cache)

	snap := filepath.Join(t.TempDir(), "app.snapshot")
	if err := os.WriteFile(snap, []byte("SNAPBYTES"), 0o600); err != nil {
		t.Fatal(err)
	}

	ex := NewExecutor(uuidRunner{uuid: "abcd-uuid"}, &bytes.Buffer{}, false)
	uuid, err := RetainSnapshot(ex, snap)
	if err != nil {
		t.Fatal(err)
	}
	if uuid != "abcd-uuid" {
		t.Errorf("uuid = %q", uuid)
	}
	got, err := os.ReadFile(filepath.Join(cache, "abcd-uuid.snapshot"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "SNAPBYTES" {
		t.Errorf("cached snapshot = %q", got)
	}
}

func TestRetainSnapshotEmptyUUID(t *testing.T) {
	t.Setenv("PORTA_SNAPSHOT_DIR", t.TempDir())
	snap := filepath.Join(t.TempDir(), "app.snapshot")
	if err := os.WriteFile(snap, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	ex := NewExecutor(uuidRunner{uuid: ""}, &bytes.Buffer{}, false)
	if _, err := RetainSnapshot(ex, snap); err == nil {
		t.Fatal("expected error on empty uuid")
	}
}
