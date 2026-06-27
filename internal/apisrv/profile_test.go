// Copyright (c) 2026 Ekorau LLC

package apisrv

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/davidg238/porta/internal/store"
)

func TestProfileStartListGet(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	mux := http.NewServeMux()
	New(st).Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// start
	body := `{"verb":"profile","args":{"name":"myapp","action":"start","duration_s":30,"label":"run1"}}`
	resp, err := http.Post(srv.URL+"/api/nodes/aabbccddeeff/commands", "application/json", strings.NewReader(body))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("start POST: %v code=%d", err, resp.StatusCode)
	}
	// a blob arrives (simulate ingest directly through the store)
	if _, err := st.InsertProfileResult("aabbccddeeff", "myapp", "run1", 1234, []byte{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}

	// list
	lr, _ := http.Get(srv.URL + "/api/nodes/aabbccddeeff/profile")
	var lenv struct {
		Data struct {
			Results []struct {
				Seq, ByteLen int64
				App, Label   string
			}
		}
	}
	json.NewDecoder(lr.Body).Decode(&lenv)
	if len(lenv.Data.Results) != 1 || lenv.Data.Results[0].Seq != 1 || lenv.Data.Results[0].Label != "run1" {
		t.Fatalf("list wrong: %+v", lenv.Data.Results)
	}

	// get blob
	gr, _ := http.Get(srv.URL + "/api/nodes/aabbccddeeff/profile/1")
	var genv struct {
		Data struct {
			Blob string
		}
	}
	json.NewDecoder(gr.Body).Decode(&genv)
	raw, _ := base64.StdEncoding.DecodeString(genv.Data.Blob)
	if len(raw) != 4 {
		t.Fatalf("blob roundtrip wrong: %v", raw)
	}
}
