package apisrv

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// postCmd sends a command envelope to the handler's mux and returns the recorder.
func postCmd(t *testing.T, h *Handler, sel, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest("POST", "/api/nodes/"+sel+"/commands", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestPostCommandVerbs(t *testing.T) {
	cases := []struct {
		name, body, wantVerb string
	}{
		{"set", `{"verb":"set","args":{"app":"sampler","key":"interval","value":30}}`, "set"},
		{"console", `{"verb":"set-console","args":{"state":"on"}}`, "set-console"},
		{"poll", `{"verb":"set-poll-interval","args":{"interval":"30s"}}`, "set-poll-interval"},
		{"power", `{"verb":"set-power-mode","args":{"mode":"always-on"}}`, "set-power-mode"},
		{"stop", `{"verb":"stop","args":{"name":"blink"}}`, "stop"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, st := newTestHandler(t)
			st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
			rec := postCmd(t, h, "aabbccddeeff", c.body)
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			cmd, err := st.NextUndelivered("aabbccddeeff")
			if err != nil || cmd == nil || cmd.Verb != c.wantVerb {
				t.Fatalf("queued=%+v err=%v want verb %q", cmd, err, c.wantVerb)
			}
		})
	}
}

func TestPostCommandUnknownVerb(t *testing.T) {
	h, st := newTestHandler(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	rec := postCmd(t, h, "aabbccddeeff", `{"verb":"explode","args":{}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestPostCommandUnknownNode(t *testing.T) {
	h, _ := newTestHandler(t)
	rec := postCmd(t, h, "ghost", `{"verb":"set-console","args":{"state":"on"}}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d", rec.Code)
	}
}
