package store

import "testing"

func seedAfter(t *testing.T) *Store {
	t.Helper()
	st := openTestStore(t)
	// ids 1..4 in insertion order (AUTOINCREMENT).
	if err := st.InsertData("dev", 100, 0, "metric", "pm", int64(13), "", "int"); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertData("dev", 101, 1, "metric", "t", float64(20.5), "", "float"); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertData("dev", 102, 2, "log", "", nil, "hello", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertData("dev", 103, 3, "metric", "pm", int64(14), "", "int"); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertData("other", 104, 0, "metric", "pm", int64(99), "", "int"); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestQueryDataLimitedPopulatesID(t *testing.T) {
	st := seedAfter(t)
	rows, err := st.QueryDataLimited("dev", 0, 0, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Fatalf("got %d rows, want 4", len(rows))
	}
	for i, r := range rows {
		if r.ID != int64(i+1) {
			t.Errorf("rows[%d].ID = %d, want %d", i, r.ID, i+1)
		}
	}
}

func TestQueryDataAfterFiltersByID(t *testing.T) {
	st := seedAfter(t)
	rows, err := st.QueryDataAfter("dev", 2, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].ID != 3 || rows[1].ID != 4 {
		t.Fatalf("after=2 got %+v, want ids 3,4", rows)
	}
}

func TestQueryDataAfterKindAndLimit(t *testing.T) {
	st := seedAfter(t)
	rows, err := st.QueryDataAfter("dev", 0, "metric", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].ID != 1 || rows[1].ID != 2 {
		t.Fatalf("kind=metric limit=2 got %+v, want ids 1,2", rows)
	}
}

func TestQueryDataAfterScopedByDevice(t *testing.T) {
	st := seedAfter(t)
	rows, err := st.QueryDataAfter("dev", 0, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Fatalf("got %d rows, want 4 (other device excluded)", len(rows))
	}
}
