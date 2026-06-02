# Control-plane HTTP API (S1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an authenticated JSON HTTP API (`/api/…`) to the porta server so a CLI (and future language tooling) can drive the gateway over the network instead of opening the SQLite db directly — writes via a uniform command envelope + a multipart image endpoint, plus the reads a CLI needs.

**Architecture:** A new `internal/apisrv` package — a sibling of `internal/web` and `internal/mcpsrv` — registers JSON routes on the shared `httpsrv.Server.Mux` (port 6970, inheriting the CIDR allowlist) and is wired in `serve.go` with `apisrv.New(st).Register(srv.Mux)`. It is a **thin adapter over `internal/control` + `internal/store`**: no business logic, so control/store stays the single writer. Routing uses Go 1.22+ method-pattern handlers (`"POST /api/nodes/{sel}/commands"`, `r.PathValue("sel")`); `{sel}` is a node id or name resolved via `control.ResolveNodeID`. Every response is a `{ok,data,error}` envelope plus a meaningful HTTP status.

**Tech Stack:** Go 1.26, net/http (stdlib ServeMux method patterns), `internal/control`, `internal/store`, `internal/command`. Spec: `docs/specs/2026-06-02-control-plane-api-design.md`.

**Verified real signatures (use exactly these):**
- `control.ResolveNodeID(st *store.Store, nodeArg string) (string, error)` — resolves id or name; error if unknown.
- `control.Set(st, id, app, key string, value any, issuedBy string, now int64) (int64, error)`
- `control.SetConsole(st, id string, on bool, issuedBy string, now int64) (int64, error)`
- `control.SetPowerMode(st, id, mode, issuedBy string, now int64) (int64, error)`
- `control.SetPollInterval(st, id string, secs int64, issuedBy string, now int64) (int64, error)`
- `control.SetMaxOffline(st, id string, secs int64) error`
- `control.Rename(st, id, name string) error`
- `control.Uninstall(st, id, name, issuedBy string, now int64) (int64, error)`
- `control.Install(st, id, name string, img io.Reader, opts control.InstallOpts, issuedBy string, now int64) (int64, error)` where `InstallOpts{CRC, IntervalS int64; Triggers []string; Runlevel int; Lifecycle string}`
- `control.AppsFromObserved(observed string) ([]control.App, error)` — `App{Name string; CRC, Runlevel int64}`
- `control.DesiredVsObserved(st, id, app string) ([]control.ConfigRow, error)`
- `store.GetNode(id string) (*store.Node, error)` — `Node{ID,Name,SourceAddr,Kind string; LastSeen,LastReportAt sql.NullInt64; PollIntervalS,MaxOfflineS int64; ObservedState,Chip,Sdk string}`; `Node.Online(now int64) bool`
- `store.ListNodes() ([]store.Node, error)`
- `store.RecentCommandsForDevice(id string, limit int) ([]store.Command, error)` — `Command{ID int64; Verb,Args string; IssuedAt int64; IssuedBy string; DeliveredAt sql.NullInt64}`
- `command.ParseDurationSeconds(s string) (int64, error)` (used by web; returns whole seconds)
- Registration pattern in `serve.go`: `web.New(st).Register(srv.Mux)` / `mcpsrv.New(st).Register(srv.Mux)`.

**Scope note (verbs):** the JSON command endpoint covers the five **image-less** verbs — `set`, `set-console`, `set-poll-interval`, `set-power-mode`, `stop`. There is intentionally **no `run` verb** in the JSON envelope: the run path always needs the image, which is the multipart `/containers` endpoint (there is no `control` function to re-run a previously-uploaded payload). This refines the spec's §4 table (which listed a bare `run`).

---

### Task 1: Package foundation — Handler, envelope helpers, sel resolver

**Files:**
- Create: `internal/apisrv/apisrv.go`
- Test: `internal/apisrv/apisrv_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/apisrv/apisrv_test.go`:

```go
package apisrv

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/davidg238/porta/internal/store"
)

// newTestHandler builds an apisrv.Handler over a fresh temp store with a fixed
// clock, returning both so tests can seed nodes and assert queued commands.
func newTestHandler(t *testing.T) (*Handler, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/d.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	h := New(st)
	h.now = func() int64 { return 1000 }
	return h, st
}

func TestWriteOK(t *testing.T) {
	rec := httptest.NewRecorder()
	writeOK(rec, map[string]any{"command_id": 7})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type=%q", ct)
	}
	var env struct {
		OK    bool           `json:"ok"`
		Data  map[string]int `json:"data"`
		Error string         `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if !env.OK || env.Data["command_id"] != 7 || env.Error != "" {
		t.Errorf("env=%+v", env)
	}
}

func TestWriteErr(t *testing.T) {
	rec := httptest.NewRecorder()
	writeErr(rec, http.StatusNotFound, "no such node")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d", rec.Code)
	}
	var env struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	json.Unmarshal(rec.Body.Bytes(), &env)
	if env.OK || env.Error != "no such node" {
		t.Errorf("env=%+v", env)
	}
}

func TestResolveSel(t *testing.T) {
	h, st := newTestHandler(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	st.SetNodeName("aabbccddeeff", "blinky")
	// By id and by name both resolve.
	for _, sel := range []string{"aabbccddeeff", "blinky"} {
		rec := httptest.NewRecorder()
		id, ok := h.resolveSel(rec, sel)
		if !ok || id != "aabbccddeeff" {
			t.Errorf("sel %q → id=%q ok=%v", sel, id, ok)
		}
	}
	// Unknown selector writes a 404 envelope and returns ok=false.
	rec := httptest.NewRecorder()
	if _, ok := h.resolveSel(rec, "ghost"); ok || rec.Code != http.StatusNotFound {
		t.Errorf("unknown sel: ok=%v code=%d", ok, rec.Code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/apisrv/`
Expected: build failure — package/`New`/`Handler`/`writeOK`/`writeErr`/`resolveSel` undefined.

- [ ] **Step 3: Implement the foundation**

Create `internal/apisrv/apisrv.go`:

```go
// Package apisrv exposes porta's control plane as an authenticated JSON HTTP
// API on the shared operator listener, so a CLI (and future language tooling)
// can drive the gateway over the network instead of opening the store directly.
// It is a thin adapter over internal/control + internal/store; control/store
// stays the single writer. Every response is a {ok,data,error} envelope plus a
// meaningful HTTP status.
package apisrv

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/davidg238/porta/internal/control"
	"github.com/davidg238/porta/internal/store"
)

// Handler holds the store and a clock. now is injectable for tests.
type Handler struct {
	st  *store.Store
	now func() int64
}

// New builds a Handler over st with a wall-clock now (Unix seconds).
func New(st *store.Store) *Handler {
	return &Handler{st: st, now: func() int64 { return time.Now().Unix() }}
}

// Register mounts the API routes on mux. Routes use Go 1.22+ method patterns;
// the shared mux's CIDR allowlist middleware (applied by httpsrv) covers them.
func (h *Handler) Register(mux *http.ServeMux) {
	// Routes are added by subsequent tasks.
}

// envelope is the uniform response shape, echoing jast-gw's Response.
type envelope struct {
	OK    bool   `json:"ok"`
	Data  any    `json:"data"`
	Error string `json:"error"`
}

// writeOK emits a 200 {ok:true,data,error:""} response.
func writeOK(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, envelope{OK: true, Data: data})
}

// writeErr emits a non-2xx {ok:false,data:null,error} response.
func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, envelope{OK: false, Error: msg})
}

func writeJSON(w http.ResponseWriter, status int, env envelope) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(env)
}

// resolveSel resolves a {sel} path value (node id or name) to a node id.
// On failure it writes a 404 envelope and returns ok=false.
func (h *Handler) resolveSel(w http.ResponseWriter, sel string) (string, bool) {
	id, err := control.ResolveNodeID(h.st, sel)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return "", false
	}
	return id, true
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/apisrv/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/apisrv/apisrv.go internal/apisrv/apisrv_test.go
git commit -m "feat(porta): apisrv foundation — handler, JSON envelope, sel resolver"
```

---

### Task 2: `POST /api/nodes/{sel}/commands` — verb dispatch

**Files:**
- Create: `internal/apisrv/commands.go`
- Modify: `internal/apisrv/apisrv.go` (add the route to `Register`)
- Test: `internal/apisrv/commands_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/apisrv/commands_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/apisrv/ -run TestPostCommand`
Expected: FAIL — route not registered (404/405) / handler missing.

- [ ] **Step 3: Implement the dispatch and register the route**

Create `internal/apisrv/commands.go`:

```go
package apisrv

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/davidg238/porta/internal/command"
	"github.com/davidg238/porta/internal/control"
)

// commandReq is the uniform command envelope: a verb plus verb-specific args.
type commandReq struct {
	Verb string          `json:"verb"`
	Args json.RawMessage `json:"args"`
}

// handleCommand dispatches one of the five image-less verbs to control.*.
func (h *Handler) handleCommand(w http.ResponseWriter, r *http.Request) {
	id, ok := h.resolveSel(w, r.PathValue("sel"))
	if !ok {
		return
	}
	var req commandReq
	// UseNumber keeps integer-shaped config values from becoming floats.
	dec := json.NewDecoder(bytes.NewReader(readBody(r)))
	dec.UseNumber()
	if err := dec.Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	cmdID, err := h.dispatch(id, req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeOK(w, map[string]any{"command_id": cmdID})
}

// dispatch maps a verb+args to the matching control call. Errors are caller-
// facing (bad args, unknown verb, validation rejections).
func (h *Handler) dispatch(id string, req commandReq) (int64, error) {
	now := h.now()
	switch req.Verb {
	case "set":
		var a struct {
			App   string `json:"app"`
			Key   string `json:"key"`
			Value any    `json:"value"`
		}
		if err := decodeArgs(req.Args, &a); err != nil {
			return 0, err
		}
		if a.App == "" || a.Key == "" {
			return 0, fmt.Errorf("set requires app and key")
		}
		return control.Set(h.st, id, a.App, a.Key, a.Value, "api", now)
	case "set-console":
		var a struct {
			State string `json:"state"`
		}
		if err := decodeArgs(req.Args, &a); err != nil {
			return 0, err
		}
		if a.State != "on" && a.State != "off" {
			return 0, fmt.Errorf("set-console state must be on or off")
		}
		return control.SetConsole(h.st, id, a.State == "on", "api", now)
	case "set-poll-interval":
		var a struct {
			Interval string `json:"interval"`
		}
		if err := decodeArgs(req.Args, &a); err != nil {
			return 0, err
		}
		secs, err := command.ParseDurationSeconds(a.Interval)
		if err != nil {
			return 0, err
		}
		return control.SetPollInterval(h.st, id, secs, "api", now)
	case "set-power-mode":
		var a struct {
			Mode string `json:"mode"`
		}
		if err := decodeArgs(req.Args, &a); err != nil {
			return 0, err
		}
		return control.SetPowerMode(h.st, id, a.Mode, "api", now)
	case "stop":
		var a struct {
			Name string `json:"name"`
		}
		if err := decodeArgs(req.Args, &a); err != nil {
			return 0, err
		}
		if a.Name == "" {
			return 0, fmt.Errorf("stop requires name")
		}
		return control.Uninstall(h.st, id, a.Name, "api", now)
	default:
		return 0, fmt.Errorf("unknown verb %q", req.Verb)
	}
}

// decodeArgs unmarshals the verb's args object (UseNumber for value typing).
func decodeArgs(raw json.RawMessage, dst any) error {
	if len(raw) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("invalid args: %w", err)
	}
	return nil
}
```

Add the body helper and route. In `internal/apisrv/apisrv.go`, add to imports `"io"` and append this helper at the end of the file:

```go
// readBody reads the full request body, swallowing the error (an empty/short
// body simply fails JSON decode downstream with a clear message).
func readBody(r *http.Request) []byte {
	b, _ := io.ReadAll(r.Body)
	return b
}
```

And in `Register`, replace the placeholder comment with:

```go
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/nodes/{sel}/commands", h.handleCommand)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/apisrv/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/apisrv/commands.go internal/apisrv/apisrv.go internal/apisrv/commands_test.go
git commit -m "feat(porta): apisrv POST /commands — image-less verb dispatch"
```

---

### Task 3: `POST /api/nodes/{sel}/containers` — multipart image install

**Files:**
- Create: `internal/apisrv/containers.go`
- Modify: `internal/apisrv/apisrv.go` (`Register`)
- Test: `internal/apisrv/containers_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/apisrv/containers_test.go`:

```go
package apisrv

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

// postContainer builds a multipart install request with the given image bytes
// and form fields, and returns the recorder.
func postContainer(t *testing.T, h *Handler, sel string, img []byte, fields map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("image", "app.bin")
	fw.Write(img)
	for k, v := range fields {
		mw.WriteField(k, v)
	}
	mw.Close()

	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest("POST", "/api/nodes/"+sel+"/containers", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestPostContainerInstall(t *testing.T) {
	h, st := newTestHandler(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	rec := postContainer(t, h, "aabbccddeeff", []byte("IMAGEBYTES"),
		map[string]string{"name": "blink", "lifecycle": "run-loop", "runlevel": "3"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	cmd, err := st.NextUndelivered("aabbccddeeff")
	if err != nil || cmd == nil || cmd.Verb != "run" {
		t.Fatalf("expected queued run, got %+v (err %v)", cmd, err)
	}
}

func TestPostContainerOversizeRejected(t *testing.T) {
	h, st := newTestHandler(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	// An image one byte over the cap trips MaxBytesReader → ParseMultipartForm
	// fails → 400 (never reaches control.Install).
	big := make([]byte, maxUpload+1)
	rec := postContainer(t, h, "aabbccddeeff", big, map[string]string{"name": "blink"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("oversize upload status=%d, want 400", rec.Code)
	}
	if cmd, _ := st.NextUndelivered("aabbccddeeff"); cmd != nil {
		t.Fatalf("oversize upload must not queue a command, got %+v", cmd)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/apisrv/ -run TestPostContainer`
Expected: FAIL — route/handler missing.

- [ ] **Step 3: Implement the install handler and register the route**

Create `internal/apisrv/containers.go` (mirrors `web.postInstall`, incl. the `MaxBytesReader` cap):

```go
package apisrv

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/davidg238/porta/internal/command"
	"github.com/davidg238/porta/internal/control"
)

// maxUpload caps an uploaded image, enforced via http.MaxBytesReader (the real
// size limit; ParseMultipartForm's arg is only the in-memory threshold).
const maxUpload = 8 << 20

// handleContainerInstall accepts a multipart .bin upload, registers it as the
// payload, and enqueues a run via control.Install.
func (h *Handler) handleContainerInstall(w http.ResponseWriter, r *http.Request) {
	id, ok := h.resolveSel(w, r.PathValue("sel"))
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
	if err := r.ParseMultipartForm(maxUpload); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	file, hdr, err := r.FormFile("image")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "image file required")
		return
	}
	defer file.Close()

	name := r.FormValue("name")
	if name == "" {
		name = strings.TrimSuffix(hdr.Filename, ".bin")
	}
	if name == "" {
		writeErr(w, http.StatusBadRequest, "container name required")
		return
	}

	opts := control.InstallOpts{Lifecycle: r.FormValue("lifecycle"), Runlevel: 3}
	if rl := r.FormValue("runlevel"); rl != "" {
		n, err := strconv.Atoi(rl)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runlevel must be an integer")
			return
		}
		opts.Runlevel = n
	}
	if iv := r.FormValue("interval"); iv != "" {
		secs, err := command.ParseDurationSeconds(iv)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		opts.IntervalS = secs
	}
	if r.MultipartForm != nil {
		opts.Triggers = r.MultipartForm.Value["trigger"] // repeatable field
	}

	cmdID, err := control.Install(h.st, id, name, file, opts, "api", h.now())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeOK(w, map[string]any{"command_id": cmdID, "size": hdr.Size})
}
```

In `Register` (apisrv.go), add the route:

```go
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/nodes/{sel}/commands", h.handleCommand)
	mux.HandleFunc("POST /api/nodes/{sel}/containers", h.handleContainerInstall)
}
```

> Response is `{command_id, size}`; the CRC32 is computed inside `control.Install`
> and stored in the queued `run` command's args (visible via the command-log
> read), so it is not recomputed here (refines spec §4's `{…,crc}`).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/apisrv/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/apisrv/containers.go internal/apisrv/apisrv.go internal/apisrv/containers_test.go
git commit -m "feat(porta): apisrv POST /containers — multipart image install"
```

---

### Task 4: `PATCH /api/nodes/{sel}` — node-management settings

**Files:**
- Create: `internal/apisrv/nodes.go`
- Modify: `internal/apisrv/apisrv.go` (`Register`)
- Test: `internal/apisrv/nodes_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/apisrv/nodes_test.go`:

```go
package apisrv

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func patchNode(t *testing.T, h *Handler, sel, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest("PATCH", "/api/nodes/"+sel, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestPatchNodeRename(t *testing.T) {
	h, st := newTestHandler(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	rec := patchNode(t, h, "aabbccddeeff", `{"name":"newname"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	n, _ := st.GetNode("aabbccddeeff")
	if n.Name != "newname" {
		t.Errorf("name=%q", n.Name)
	}
}

func TestPatchNodeMaxOffline(t *testing.T) {
	h, st := newTestHandler(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	rec := patchNode(t, h, "aabbccddeeff", `{"max_offline_s":600}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	n, _ := st.GetNode("aabbccddeeff")
	if n.MaxOfflineS != 600 {
		t.Errorf("max_offline_s=%d", n.MaxOfflineS)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/apisrv/ -run TestPatchNode`
Expected: FAIL — route/handler missing.

- [ ] **Step 3: Implement the PATCH handler and register the route**

Create `internal/apisrv/nodes.go`:

```go
package apisrv

import (
	"encoding/json"
	"net/http"

	"github.com/davidg238/porta/internal/control"
)

// nodePatch carries optional node-management settings; pointer fields let the
// handler apply only what was sent.
type nodePatch struct {
	Name        *string `json:"name"`
	MaxOfflineS *int64  `json:"max_offline_s"`
}

// handlePatchNode applies rename / max-offline node settings (gateway-side,
// not device commands).
func (h *Handler) handlePatchNode(w http.ResponseWriter, r *http.Request) {
	id, ok := h.resolveSel(w, r.PathValue("sel"))
	if !ok {
		return
	}
	var p nodePatch
	if err := json.Unmarshal(readBody(r), &p); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if p.Name != nil {
		if err := control.Rename(h.st, id, *p.Name); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if p.MaxOfflineS != nil {
		if err := control.SetMaxOffline(h.st, id, *p.MaxOfflineS); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	writeOK(w, map[string]any{})
}
```

In `Register` (apisrv.go), add the route:

```go
	mux.HandleFunc("PATCH /api/nodes/{sel}", h.handlePatchNode)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/apisrv/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/apisrv/nodes.go internal/apisrv/apisrv.go internal/apisrv/nodes_test.go
git commit -m "feat(porta): apisrv PATCH /nodes/{sel} — rename + max-offline"
```

---

### Task 5: `GET /api/nodes` — fleet list with identity

**Files:**
- Modify: `internal/apisrv/nodes.go` (add list handler + view type)
- Modify: `internal/apisrv/apisrv.go` (`Register`)
- Test: `internal/apisrv/nodes_test.go` (add)

- [ ] **Step 1: Write the failing test**

Add to `internal/apisrv/nodes_test.go`:

```go
func TestGetNodesList(t *testing.T) {
	h, st := newTestHandler(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	st.SetNodeName("aabbccddeeff", "blinky")
	st.UpdateNodeIdentity("aabbccddeeff", "esp32c6", "v2.0.0-alpha.192")

	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest("GET", "/api/nodes", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Nodes []struct {
				ID, Name, Kind, IP, Chip, Sdk string
			} `json:"nodes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Data.Nodes) != 1 {
		t.Fatalf("nodes=%+v", env.Data.Nodes)
	}
	n := env.Data.Nodes[0]
	if n.ID != "aabbccddeeff" || n.Name != "blinky" || n.Chip != "esp32c6" || n.Sdk != "v2.0.0-alpha.192" {
		t.Errorf("node=%+v", n)
	}
}
```

Add `"encoding/json"` to the `nodes_test.go` import block.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/apisrv/ -run TestGetNodesList`
Expected: FAIL — route/handler missing.

- [ ] **Step 3: Implement the list handler and register the route**

Add to `internal/apisrv/nodes.go` (add `"github.com/davidg238/porta/internal/store"` to its imports):

```go
// nodeListItem is one row of GET /api/nodes.
type nodeListItem struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	IP       string `json:"ip"`
	LastSeen int64  `json:"last_seen"`
	Online   bool   `json:"online"`
	Chip     string `json:"chip"`
	Sdk      string `json:"sdk"`
}

// handleListNodes returns the fleet list, including self-reported identity.
func (h *Handler) handleListNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := h.st.ListNodes()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	now := h.now()
	out := make([]nodeListItem, 0, len(nodes))
	for i := range nodes {
		n := &nodes[i]
		out = append(out, nodeListItem{
			ID: n.ID, Name: n.Name, Kind: n.Kind, IP: n.SourceAddr,
			LastSeen: n.LastSeen.Int64, Online: n.Online(now),
			Chip: n.Chip, Sdk: n.Sdk,
		})
	}
	writeOK(w, map[string]any{"nodes": out})
}
```

`nodes.go` needs **no** `store` import: the list handler calls `h.st.ListNodes()`
and ranges the result via `&nodes[i]` without ever writing a `store.` qualifier.
Keep nodes.go's imports as `encoding/json`, `net/http`, and `internal/control`
(unchanged from Task 4); `nodeListItem` adds nothing new.

In `Register` (apisrv.go), add:

```go
	mux.HandleFunc("GET /api/nodes", h.handleListNodes)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/apisrv/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/apisrv/nodes.go internal/apisrv/apisrv.go internal/apisrv/nodes_test.go
git commit -m "feat(porta): apisrv GET /nodes — fleet list with chip/sdk identity"
```

---

### Task 6: `GET /api/nodes/{sel}` — node detail with identity, apps, config

**Files:**
- Modify: `internal/apisrv/nodes.go` (detail handler + view types + firstApp helper)
- Modify: `internal/apisrv/apisrv.go` (`Register`)
- Test: `internal/apisrv/nodes_test.go` (add)

- [ ] **Step 1: Write the failing test**

Add to `internal/apisrv/nodes_test.go`:

```go
func TestGetNodeDetail(t *testing.T) {
	h, st := newTestHandler(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	st.SetNodeName("aabbccddeeff", "blinky")
	st.UpdateNodeIdentity("aabbccddeeff", "esp32", "v2.0.0-alpha.192")

	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest("GET", "/api/nodes/blinky", nil) // resolve by name
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			ID, Name, Chip, Sdk string
			PollIntervalS       int64 `json:"poll_interval_s"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.ID != "aabbccddeeff" || env.Data.Chip != "esp32" || env.Data.Sdk != "v2.0.0-alpha.192" {
		t.Errorf("detail=%+v", env.Data)
	}
}

func TestGetNodeDetailUnknown(t *testing.T) {
	h, _ := newTestHandler(t)
	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest("GET", "/api/nodes/ghost", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d", rec.Code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/apisrv/ -run TestGetNodeDetail`
Expected: FAIL — route/handler missing.

- [ ] **Step 3: Implement the detail handler and register the route**

Add to `internal/apisrv/nodes.go` (now add `"github.com/davidg238/porta/internal/control"` is already imported; add `"sort"`):

```go
// nodeDetail is GET /api/nodes/{sel}: identity + observed apps + config
// (desired-vs-observed for the first app, mirroring the web detail page) +
// timings. The SDK guard in `porta run` reads chip/sdk from here.
type nodeDetail struct {
	ID            string               `json:"id"`
	Name          string               `json:"name"`
	Kind          string               `json:"kind"`
	IP            string               `json:"ip"`
	Online        bool                 `json:"online"`
	Chip          string               `json:"chip"`
	Sdk           string               `json:"sdk"`
	PollIntervalS int64                `json:"poll_interval_s"`
	MaxOfflineS   int64                `json:"max_offline_s"`
	LastSeen      int64                `json:"last_seen"`
	LastReportAt  int64                `json:"last_report_at"`
	Apps          []control.App        `json:"apps"`
	ConfigApp     string               `json:"config_app"`
	Config        []control.ConfigRow  `json:"config"`
}

// handleNodeDetail returns one node's full detail.
func (h *Handler) handleNodeDetail(w http.ResponseWriter, r *http.Request) {
	id, ok := h.resolveSel(w, r.PathValue("sel"))
	if !ok {
		return
	}
	n, err := h.st.GetNode(id)
	if err != nil || n == nil {
		writeErr(w, http.StatusNotFound, "node not found")
		return
	}
	apps, _ := control.AppsFromObserved(n.ObservedState)
	confApp := firstAppName(apps)
	var cfg []control.ConfigRow
	if confApp != "" {
		cfg, _ = control.DesiredVsObserved(h.st, id, confApp)
	}
	writeOK(w, nodeDetail{
		ID: n.ID, Name: n.Name, Kind: n.Kind, IP: n.SourceAddr,
		Online: n.Online(h.now()), Chip: n.Chip, Sdk: n.Sdk,
		PollIntervalS: n.PollIntervalS, MaxOfflineS: n.MaxOfflineS,
		LastSeen: n.LastSeen.Int64, LastReportAt: n.LastReportAt.Int64,
		Apps: apps, ConfigApp: confApp, Config: cfg,
	})
}

// firstAppName returns the lexically-first observed app name (the app whose
// config the detail view surfaces), or "" if none.
func firstAppName(apps []control.App) string {
	names := make([]string, 0, len(apps))
	for _, a := range apps {
		names = append(names, a.Name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return ""
	}
	return names[0]
}
```

In `Register` (apisrv.go), add (place **after** the `GET /api/nodes` route; Go's
ServeMux distinguishes the two patterns by specificity):

```go
	mux.HandleFunc("GET /api/nodes/{sel}", h.handleNodeDetail)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/apisrv/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/apisrv/nodes.go internal/apisrv/apisrv.go internal/apisrv/nodes_test.go
git commit -m "feat(porta): apisrv GET /nodes/{sel} — node detail with identity + config"
```

---

### Task 7: `GET /api/nodes/{sel}/commands` — command log

**Files:**
- Create: `internal/apisrv/reads.go`
- Modify: `internal/apisrv/apisrv.go` (`Register`)
- Test: `internal/apisrv/reads_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/apisrv/reads_test.go`:

```go
package apisrv

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/apisrv/ -run TestGetNodeCommands`
Expected: FAIL — route/handler missing.

- [ ] **Step 3: Implement the command-log handler and register the route**

Create `internal/apisrv/reads.go`:

```go
package apisrv

import "net/http"

// commandLogItem is one row of GET /api/nodes/{sel}/commands.
type commandLogItem struct {
	ID        int64  `json:"id"`
	Verb      string `json:"verb"`
	Args      string `json:"args"`
	IssuedAt  int64  `json:"issued_at"`
	IssuedBy  string `json:"issued_by"`
	Delivered bool   `json:"delivered"`
}

// commandLogLimit bounds how many recent commands the log read returns.
const commandLogLimit = 50

// handleNodeCommands returns the recent command log for one node.
func (h *Handler) handleNodeCommands(w http.ResponseWriter, r *http.Request) {
	id, ok := h.resolveSel(w, r.PathValue("sel"))
	if !ok {
		return
	}
	cmds, err := h.st.RecentCommandsForDevice(id, commandLogLimit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]commandLogItem, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, commandLogItem{
			ID: c.ID, Verb: c.Verb, Args: c.Args,
			IssuedAt: c.IssuedAt, IssuedBy: c.IssuedBy,
			Delivered: c.DeliveredAt.Valid,
		})
	}
	writeOK(w, map[string]any{"commands": out})
}
```

In `Register` (apisrv.go), add:

```go
	mux.HandleFunc("GET /api/nodes/{sel}/commands", h.handleNodeCommands)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/apisrv/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/apisrv/reads.go internal/apisrv/apisrv.go internal/apisrv/reads_test.go
git commit -m "feat(porta): apisrv GET /nodes/{sel}/commands — command log read"
```

---

### Task 8: Wire apisrv into the server + whole-tree verification

**Files:**
- Modify: `internal/portacli/serve.go` (register apisrv on the shared mux)

- [ ] **Step 1: Register apisrv alongside web + mcp**

In `internal/portacli/serve.go`, add the import (with the other internal imports):

```go
	"github.com/davidg238/porta/internal/apisrv"
```

and, immediately after the existing `web.New(st).Register(srv.Mux)` /
`mcpsrv.New(st).Register(srv.Mux)` lines, add:

```go
				apisrv.New(st).Register(srv.Mux)
```

(Match the exact indentation of the two existing `.Register(srv.Mux)` lines.)

- [ ] **Step 2: Build + vet + test the whole tree**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all green. (The apisrv routes now live on the allowlisted listener; the
CIDR allowlist is already covered by `httpsrv` tests and applies uniformly, so no
new allowlist test is needed here.)

- [ ] **Step 3: Manual smoke (optional, needs a running server)**

```bash
go run ./cmd/porta serve --http-port 6970 &   # in one shell
curl -s localhost:6970/api/nodes | jq .         # {ok:true,data:{nodes:[...]}}
# (with a known node id/name NODE):
curl -s -X POST localhost:6970/api/nodes/NODE/commands \
  -H 'content-type: application/json' \
  -d '{"verb":"set-console","args":{"state":"on"}}' | jq .
```

Expected: JSON envelopes; the POST returns `{ok:true,data:{command_id:…}}` and the
command appears in `curl -s localhost:6970/api/nodes/NODE/commands`.

- [ ] **Step 4: Commit**

```bash
git add internal/portacli/serve.go
git commit -m "feat(porta): mount control-plane API (/api) on the operator listener"
```

---

## Final verification

- [ ] `go build ./... && go vet ./... && go test ./...` — all green.
- [ ] Every endpoint in the spec §4 is implemented: `POST …/commands` (Task 2),
  `POST …/containers` (Task 3), `PATCH …` (Task 4), `GET /api/nodes` (Task 5),
  `GET /api/nodes/{sel}` (Task 6), `GET …/commands` (Task 7), wired (Task 8).
- [ ] All writes stamp `issued_by="api"` (Tasks 2–3 pass `"api"`; verified by
  Task 7's test asserting `IssuedBy=="api"`).

## Notes for the implementer

- **Routing specificity:** Go 1.22+ `ServeMux` resolves `GET /api/nodes` vs
  `GET /api/nodes/{sel}` vs `GET /api/nodes/{sel}/commands` by pattern specificity,
  so registration order does not matter for correctness; the plan groups them for
  readability.
- **`store` import in nodes.go:** it is only needed once the detail handler (Task 6)
  references `control.App`/`control.ConfigRow` (from `control`, not `store`). In
  fact nodes.go never needs a direct `store.` reference — do not add a `store`
  import; let `go vet`/compile guide imports per task.
- **Refinements vs spec** (both already noted in-task): no bare `run` verb in the
  JSON envelope (run requires the image → `/containers`); the install response is
  `{command_id, size}` (CRC lives in the queued command's args). Update spec §4 if
  you want it to match exactly; neither changes behaviour.
- **No streaming, no new deps, no web/MCP changes** — apisrv only adds routes and
  calls existing `control`/`store` functions.
```
