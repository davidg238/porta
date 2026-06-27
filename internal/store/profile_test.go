// Copyright (c) 2026 Ekorau LLC

package store

import (
	"bytes"
	"testing"
)

func TestProfileResultSeqAndCorrelation(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if err := st.UpsertProfileSession("aabbccddeeff", "myapp", "before-fix", 1000); err != nil {
		t.Fatal(err)
	}
	sess, err := st.GetProfileSession("aabbccddeeff")
	if err != nil || sess == nil {
		t.Fatalf("session: %v %v", sess, err)
	}
	if sess.App != "myapp" || sess.Label != "before-fix" {
		t.Fatalf("session mismatch: %+v", sess)
	}

	seq1, err := st.InsertProfileResult("aabbccddeeff", sess.App, sess.Label, 1001, []byte{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	seq2, err := st.InsertProfileResult("aabbccddeeff", sess.App, sess.Label, 1002, []byte{4, 5})
	if err != nil {
		t.Fatal(err)
	}
	if seq1 != 1 || seq2 != 2 {
		t.Fatalf("per-node seq want 1,2 got %d,%d", seq1, seq2)
	}

	list, err := st.ProfileResults("aabbccddeeff", 0, 0)
	if err != nil || len(list) != 2 {
		t.Fatalf("list: %v len=%d", err, len(list))
	}
	if list[0].Blob != nil {
		t.Errorf("list view must omit blob")
	}
	if list[0].App != "myapp" || list[0].Label != "before-fix" || list[0].ByteLen != 3 {
		t.Errorf("row0 wrong: %+v", list[0])
	}

	one, err := st.GetProfileResult("aabbccddeeff", 1)
	if err != nil || one == nil {
		t.Fatalf("get: %v", err)
	}
	if !bytes.Equal(one.Blob, []byte{1, 2, 3}) {
		t.Errorf("blob mismatch: %v", one.Blob)
	}

	after, err := st.ProfileResults("aabbccddeeff", 1, 0)
	if err != nil || len(after) != 1 || after[0].Seq != 2 {
		t.Fatalf("afterSeq filter wrong: %v %+v", err, after)
	}
}

// TestProfileResultUniqueConstraint proves that UNIQUE(device_id, seq) on
// profile_result is enforced at the DDL level. InsertProfileResult auto-increments
// seq so it never collides through the public API; we bypass it with raw SQL to
// confirm the constraint exists and raises an error on duplicate (device_id, seq).
func TestProfileResultUniqueConstraint(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	_, err = st.db.Exec(`INSERT INTO profile_result (device_id, seq, ts, app, label, blob, byte_len) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"aabbccddeeff", 1, 1000, "myapp", "run1", []byte{0x01}, 1)
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}
	_, err = st.db.Exec(`INSERT INTO profile_result (device_id, seq, ts, app, label, blob, byte_len) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"aabbccddeeff", 1, 1001, "myapp", "run2", []byte{0x02}, 1)
	if err == nil {
		t.Error("second insert with same (device_id, seq) should have failed due to UNIQUE constraint")
	}
}

func TestProfileResultsRecent(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Insert 5 rows; then ask for newest 3 — must get seq 5,4,3 in that order.
	for i := 0; i < 5; i++ {
		if _, err := st.InsertProfileResult("node1", "app", "lbl", int64(1000+i), []byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}

	recent, err := st.ProfileResultsRecent("node1", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 3 {
		t.Fatalf("want 3 rows, got %d", len(recent))
	}
	// Newest-first: seq 5, 4, 3
	if recent[0].Seq != 5 || recent[1].Seq != 4 || recent[2].Seq != 3 {
		t.Errorf("want seqs 5,4,3 got %d,%d,%d", recent[0].Seq, recent[1].Seq, recent[2].Seq)
	}
	// Blob must be omitted in list view
	for _, r := range recent {
		if r.Blob != nil {
			t.Errorf("list view must omit blob, seq %d has blob", r.Seq)
		}
	}
}
