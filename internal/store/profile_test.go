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

	if err := st.UpsertProfileSession("aabbccddeeff", "myapp", "before-fix", 30, 1000); err != nil {
		t.Fatal(err)
	}
	sess, err := st.GetProfileSession("aabbccddeeff")
	if err != nil || sess == nil {
		t.Fatalf("session: %v %v", sess, err)
	}
	if sess.App != "myapp" || sess.Label != "before-fix" {
		t.Fatalf("session mismatch: %+v", sess)
	}
	if sess.DurationS != 30 || sess.StartedAt != 1000 {
		t.Fatalf("session window mismatch: %+v", sess)
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

// TestProfileSessionDurationMigration proves that a DB created before the
// duration_s column existed gains it on Open (additive ALTER), and the column
// is usable afterwards.
func TestProfileSessionDurationMigration(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/old.db"

	// Stand up an "old" DB: a profile_session table WITHOUT duration_s.
	old, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.db.Exec(`DROP TABLE profile_session`); err != nil {
		t.Fatal(err)
	}
	if _, err := old.db.Exec(`CREATE TABLE profile_session (
	  device_id TEXT PRIMARY KEY, app TEXT, label TEXT, started_at INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := old.db.Exec(`INSERT INTO profile_session (device_id, app, label, started_at)
	  VALUES ('n1','app','lbl',1000)`); err != nil {
		t.Fatal(err)
	}
	old.Close()

	// Reopen: migration must add duration_s. Existing row reads back with 0.
	st, err := Open(path)
	if err != nil {
		t.Fatalf("reopen (migration): %v", err)
	}
	defer st.Close()
	sess, err := st.GetProfileSession("n1")
	if err != nil || sess == nil {
		t.Fatalf("session after migration: %v %v", sess, err)
	}
	if sess.DurationS != 0 {
		t.Fatalf("migrated row duration_s want 0, got %d", sess.DurationS)
	}
	// And the column accepts writes.
	if err := st.UpsertProfileSession("n1", "app", "lbl", 45, 2000); err != nil {
		t.Fatal(err)
	}
	sess, _ = st.GetProfileSession("n1")
	if sess.DurationS != 45 {
		t.Fatalf("post-migration write duration_s want 45, got %d", sess.DurationS)
	}
}

func TestLatestProfileResultTS(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// No results yet → 0.
	ts, err := st.LatestProfileResultTS("node1")
	if err != nil {
		t.Fatal(err)
	}
	if ts != 0 {
		t.Fatalf("want 0 for no results, got %d", ts)
	}

	if _, err := st.InsertProfileResult("node1", "app", "lbl", 1000, []byte{1}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertProfileResult("node1", "app", "lbl", 1005, []byte{2}); err != nil {
		t.Fatal(err)
	}
	// Other node's result must not leak.
	if _, err := st.InsertProfileResult("other", "app", "lbl", 9999, []byte{3}); err != nil {
		t.Fatal(err)
	}

	ts, err = st.LatestProfileResultTS("node1")
	if err != nil {
		t.Fatal(err)
	}
	if ts != 1005 {
		t.Fatalf("want newest ts 1005, got %d", ts)
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
