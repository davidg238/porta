package store

import "testing"

func TestDebugRequestFIFOAndDeliver(t *testing.T) {
	st := openTmp(t)
	id1, err := st.EnqueueDebugRequest("dev", "dbg:methods", 100)
	if err != nil {
		t.Fatal(err)
	}
	st.EnqueueDebugRequest("dev", "dbg:continue", 101)

	r, _ := st.NextUndeliveredDebugRequest("dev")
	if r == nil || r.ID != id1 || r.Line != "dbg:methods" {
		t.Fatalf("FIFO wrong: %+v", r)
	}
	if err := st.MarkDebugRequestDelivered(r.ID, 200); err != nil {
		t.Fatal(err)
	}
	r, _ = st.NextUndeliveredDebugRequest("dev")
	if r == nil || r.Line != "dbg:continue" {
		t.Fatalf("after deliver, next should be continue: %+v", r)
	}
}

func TestDebugResponseAfterCursor(t *testing.T) {
	st := openTmp(t)
	st.InsertDebugResponse("dev", 10, 0, "dbg:ready")
	st.InsertDebugResponse("dev", 10, 1, "dbg:ok methods")
	all, _ := st.DebugResponsesAfter("dev", 0, 0)
	if len(all) != 2 || all[0].Line != "dbg:ready" {
		t.Fatalf("after(0) = %+v", all)
	}
	rest, _ := st.DebugResponsesAfter("dev", all[0].ID, 0)
	if len(rest) != 1 || rest[0].Line != "dbg:ok methods" {
		t.Fatalf("after(first) = %+v", rest)
	}
}
