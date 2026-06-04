// Copyright (c) 2026 Ekorau LLC

package mcpserver

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCompileSTHTTPSuccess(t *testing.T) {
	wantBEC := []byte{0x01, 0x02, 0x03}
	wantMap := `{"source":"module.st","functions":{}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/compile" {
			t.Errorf("path = %s", r.URL.Path)
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["source"] != "x := 42." {
			t.Errorf("source = %v", req["source"])
		}
		if req["name"] != "module.st" {
			t.Errorf("name = %v", req["name"])
		}
		if req["symbols"] != false {
			t.Errorf("symbols = %v", req["symbols"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"bec":   base64.StdEncoding.EncodeToString(wantBEC),
			"stmap": wantMap,
		})
	}))
	defer srv.Close()

	cr, err := compileST(srv.URL, "x := 42.", "module.st", false)
	if err != nil {
		t.Fatal(err)
	}
	if string(cr.BEC) != string(wantBEC) {
		t.Errorf("BEC = %v, want %v", cr.BEC, wantBEC)
	}
	if string(cr.STMap) != wantMap {
		t.Errorf("STMap = %q", cr.STMap)
	}
}

func TestCompileSTHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "boom"})
	}))
	defer srv.Close()

	_, err := compileST(srv.URL, "bad", "module.st", false)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error = %v, want it to contain %q", err, "boom")
	}
}
