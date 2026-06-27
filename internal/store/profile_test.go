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
