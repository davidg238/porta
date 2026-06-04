// Copyright (c) 2026 Ekorau LLC

package apiclient

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// telemetryStub serves a fixed {ok,data:{rows}} envelope and records the last
// query string so the test can assert the params the client sent.
func telemetryStub(t *testing.T, body string) (*Client, *string) {
	t.Helper()
	var lastQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL), &lastQuery
}

func TestQueryTelemetryWindowDecodesTypes(t *testing.T) {
	body := `{"ok":true,"data":{"rows":[
		{"id":1,"ts":100,"seq":0,"kind":"metric","name":"pm","value":13,"text":"","value_type":"int"},
		{"id":2,"ts":101,"seq":1,"kind":"metric","name":"t","value":20.5,"text":"","value_type":"float"},
		{"id":3,"ts":102,"seq":2,"kind":"metric","name":"door","value":1,"text":"","value_type":"bool"},
		{"id":4,"ts":103,"seq":3,"kind":"metric","name":"mode","value":null,"text":"auto","value_type":"string"},
		{"id":5,"ts":104,"seq":4,"kind":"log","name":"","value":null,"text":"started","value_type":""}
	]},"error":""}`
	c, lastQuery := telemetryStub(t, body)
	rows, err := c.QueryTelemetryWindow("dev", 50, 200, "metric", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 5 {
		t.Fatalf("got %d rows, want 5", len(rows))
	}
	if v, ok := rows[0].Value.(int64); !ok || v != 13 {
		t.Errorf("row0 value = %#v, want int64 13", rows[0].Value)
	}
	if v, ok := rows[1].Value.(float64); !ok || v != 20.5 {
		t.Errorf("row1 value = %#v, want float64 20.5", rows[1].Value)
	}
	if v, ok := rows[2].Value.(int64); !ok || v != 1 {
		t.Errorf("row2 (bool) value = %#v, want int64 1", rows[2].Value)
	}
	if rows[3].Value != nil || rows[3].Text != "auto" {
		t.Errorf("row3 (string) = %#v / %q, want nil / auto", rows[3].Value, rows[3].Text)
	}
	if rows[4].Value != nil || rows[4].ID != 5 {
		t.Errorf("row4 (log) = %#v / id %d", rows[4].Value, rows[4].ID)
	}
	if got := *lastQuery; got != "kind=metric&limit=10&since=50&until=200" {
		t.Errorf("window query = %q", got)
	}
}

func TestQueryTelemetryAfterQuery(t *testing.T) {
	c, lastQuery := telemetryStub(t, `{"ok":true,"data":{"rows":[]},"error":""}`)
	if _, err := c.QueryTelemetryAfter("dev", 7, "", 0); err != nil {
		t.Fatal(err)
	}
	if got := *lastQuery; got != "after=7" {
		t.Errorf("after query = %q, want after=7", got)
	}
}

func TestQueryTelemetryWindowOmitsZeroParams(t *testing.T) {
	c, lastQuery := telemetryStub(t, `{"ok":true,"data":{"rows":[]},"error":""}`)
	if _, err := c.QueryTelemetryWindow("dev", 0, 0, "", 0); err != nil {
		t.Fatal(err)
	}
	// until/kind/limit omitted when zero/empty; since always emitted.
	if got := *lastQuery; got != "since=0" {
		t.Errorf("bare window query = %q, want since=0", got)
	}
}

func TestQueryTelemetryServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte(`{"ok":false,"data":null,"error":"unknown node"}`))
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL)
	if _, err := c.QueryTelemetryWindow("nope", 0, 0, "", 0); err == nil || err.Error() != "unknown node" {
		t.Fatalf("err = %v, want \"unknown node\"", err)
	}
}

func TestQueryTelemetryPreservesLargeInt64(t *testing.T) {
	// 2^53+1 is not exactly representable as a float64 — it must round-trip as
	// int64 to survive, which is the whole point of the json.RawMessage path.
	body := `{"ok":true,"data":{"rows":[
		{"id":1,"ts":100,"seq":0,"kind":"metric","name":"big","value":9007199254740993,"text":"","value_type":"int"}
	]},"error":""}`
	c, _ := telemetryStub(t, body)
	rows, err := c.QueryTelemetryWindow("dev", 0, 0, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	v, ok := rows[0].Value.(int64)
	if !ok || v != 9007199254740993 {
		t.Errorf("value = %#v, want int64 9007199254740993", rows[0].Value)
	}
}
