// Copyright (c) 2026 Ekorau LLC

package apisrv

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetNodeCommandsUnknownNode(t *testing.T) {
	h, _ := newTestHandler(t)
	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest("GET", "/api/nodes/ghost/commands", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestGetNodeCommands(t *testing.T) {
	h, st := newTestHandler(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	// Queue one command via the API so the log has a row.
	postCmd(t, h, "aabbccddeeff", `{"verb":"set-console","args":{"state":"on"}}`)

	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest("GET", "/api/nodes/aabbccddeeff/commands", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	var env struct {
		Data struct {
			Commands []struct {
				ID       int64  `json:"id"`
				Verb     string `json:"verb"`
				IssuedBy string `json:"issued_by"`
			} `json:"commands"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Data.Commands) != 1 || env.Data.Commands[0].Verb != "set-console" || env.Data.Commands[0].IssuedBy != "api" {
		t.Fatalf("commands=%+v", env.Data.Commands)
	}
}
