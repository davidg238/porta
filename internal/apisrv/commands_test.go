package apisrv

import (
	"encoding/json"
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
			var resp struct {
				OK   bool `json:"ok"`
				Data struct {
					CommandID int64 `json:"command_id"`
				} `json:"data"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if !resp.OK || resp.Data.CommandID <= 0 {
				t.Fatalf("want ok+command_id>0, got ok=%v command_id=%d", resp.OK, resp.Data.CommandID)
			}
			cmd, err := st.NextUndelivered("aabbccddeeff")
			if err != nil || cmd == nil || cmd.Verb != c.wantVerb {
				t.Fatalf("queued=%+v err=%v want verb %q", cmd, err, c.wantVerb)
			}
		})
	}
}

// TestCoerceScalar locks the precision-preserving behaviour: integer-shaped
// json.Numbers stay int64, float-shaped become float64, and non-Numbers pass
// through unchanged (spec §3 / B2 review hardenings).
func TestCoerceScalar(t *testing.T) {
	cases := []struct {
		name  string
		input any
		check func(t *testing.T, got any)
	}{
		{
			name:  "integer json.Number becomes int64",
			input: json.Number("30"),
			check: func(t *testing.T, got any) {
				v, ok := got.(int64)
				if !ok {
					t.Fatalf("type=%T, want int64", got)
				}
				if v != 30 {
					t.Errorf("value=%d, want 30", v)
				}
			},
		},
		{
			name:  "float json.Number becomes float64",
			input: json.Number("3.5"),
			check: func(t *testing.T, got any) {
				v, ok := got.(float64)
				if !ok {
					t.Fatalf("type=%T, want float64", got)
				}
				if v != 3.5 {
					t.Errorf("value=%v, want 3.5", v)
				}
			},
		},
		{
			name:  "plain string passes through",
			input: "hello",
			check: func(t *testing.T, got any) {
				v, ok := got.(string)
				if !ok {
					t.Fatalf("type=%T, want string", got)
				}
				if v != "hello" {
					t.Errorf("value=%q, want hello", v)
				}
			},
		},
		{
			name:  "bool passes through",
			input: true,
			check: func(t *testing.T, got any) {
				v, ok := got.(bool)
				if !ok {
					t.Fatalf("type=%T, want bool", got)
				}
				if !v {
					t.Errorf("value=%v, want true", v)
				}
			},
		},
		{
			name:  "nil passes through as nil",
			input: nil,
			check: func(t *testing.T, got any) {
				if got != nil {
					t.Fatalf("got=%v (%T), want nil", got, got)
				}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := coerceScalar(c.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			c.check(t, got)
		})
	}
}

// TestPostCommandValidationError verifies that a control-layer validation
// rejection surfaces as a 400 response with a non-empty error message.
//
// set-power-mode validates the mode value at enqueue time via command.SetPowerMode
// (rejects anything other than "deep-sleep" or "always-on"), making it the ideal
// probe. The other verbs — set, set-console, set-poll-interval, stop — validate
// their own structural args before reaching control but do not have a mode enum
// that control rejects; their validation is inline in dispatch().
func TestPostCommandValidationError(t *testing.T) {
	h, st := newTestHandler(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	rec := postCmd(t, h, "aabbccddeeff", `{"verb":"set-power-mode","args":{"mode":"turbo"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	var env struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.OK {
		t.Error("ok should be false on validation error")
	}
	if env.Error == "" {
		t.Error("error message should be non-empty")
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
