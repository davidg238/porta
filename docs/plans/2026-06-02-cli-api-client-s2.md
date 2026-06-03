# CLI-as-API-client (S2) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Re-point porta's 8 mutating CLI commands to drive the S1 control-plane HTTP API (`internal/apisrv`) over the network instead of opening the SQLite store directly, completing the "one writer for writes" invariant.

**Architecture:** A new cobra-free, store-free `internal/apiclient` package wraps the three S1 write endpoints (`POST /commands`, `POST /containers` multipart, `PATCH /nodes`). The 8 write commands lose their `openStore()`/`resolveNodeID()`/`EnsureNode()` calls and instead build an `apiclient.Client` against a `--server` URL, passing the raw `-d` selector for the server to resolve. The server gains `EnsureNode`-on-write (preserving bench pre-provisioning) and echoes the resolved `node_id` in write responses so CLI confirmation lines still lead with the resolved MAC. Reads, `run`, `monitor`, and `serve` stay db-backed (deferred to later phases).

**Tech Stack:** Go 1.22 (stdlib `net/http`, `mime/multipart`, `encoding/json`), cobra, `httptest` for host-only tests. No device, no schema, no wire change.

**Spec:** `docs/specs/2026-06-02-cli-api-client-s2-design.md`

**Branch:** create `feat/porta-cli-api-client` off `master` before Task 1.

---

## File Structure

- **Create** `internal/apiclient/client.go` — the write-side HTTP client: `Client`, `New`, the `{ok,data,error}` envelope decode helper (`do`), and three methods (`Command`, `Install`, `PatchNode`) + `InstallOpts`. One focused file.
- **Create** `internal/apiclient/client_test.go` — unit tests against an `httptest` stub: request method/path/body shape, multipart parts, status→error mapping, transport-error wrap, response decode.
- **Modify** `internal/apisrv/commands.go`, `internal/apisrv/containers.go`, `internal/apisrv/nodes.go` — add `EnsureNode` after `resolveSel` on the three write paths; add `node_id` to the three write responses.
- **Modify** `internal/apisrv/commands_test.go`, `internal/apisrv/containers_test.go`, `internal/apisrv/nodes_test.go` — assert EnsureNode-on-write and `node_id` echo.
- **Modify** `internal/portacli/root.go` — add the `--server` persistent flag and the `serverURL()` resolver.
- **Create** `internal/portacli/client.go` — small helper holding `serverFlag` + `serverURL()` (keeps `root.go` lean; spec §8 leaves the exact home open — we choose a dedicated file).
- **Create** `internal/portacli/client_test.go` — `serverURL()` precedence (flag > `$PORTA_SERVER` > default).
- **Modify** `internal/portacli/mutate.go` — re-point all 8 write commands; cores now take `*apiclient.Client` + raw selector; drop `store`/`control`/`bytes` imports and the `--crc` flag.
- **Modify** `internal/portacli/mutate_test.go` — rewrite core tests to drive the **real** `apisrv.Handler` over a temp store behind `httptest.NewServer`.
- **Create** `internal/portacli/e2e_test.go` — full cobra→HTTP→apisrv→store test through the `--server` flag.
- **Modify** `docs/specs/2026-06-02-control-plane-api-s1.md` — reconcile §4 to the additive `node_id` field and EnsureNode-on-write (the S1 spec is the canonical API doc).

---

## Task 1: `internal/apiclient` — Client, envelope decode, and `Command`

**Files:**
- Create: `internal/apiclient/client.go`
- Test: `internal/apiclient/client_test.go`

- [ ] **Step 1: Create the branch**

```bash
git checkout master
git checkout -b feat/porta-cli-api-client
```

- [ ] **Step 2: Write the failing test**

Create `internal/apiclient/client_test.go`:

```go
package apiclient

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubServer returns an httptest server whose handler records the last request
// (method, path, body) and replies with the given status + JSON envelope body.
func stubServer(t *testing.T, status int, respBody string, rec *recordedReq) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		rec.path = r.URL.Path
		rec.contentType = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		rec.body = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, respBody)
	}))
	t.Cleanup(srv.Close)
	return srv
}

type recordedReq struct {
	method, path, contentType, body string
}

func TestCommandPostsEnvelopeAndDecodes(t *testing.T) {
	var rec recordedReq
	srv := stubServer(t, http.StatusOK,
		`{"ok":true,"data":{"command_id":7,"node_id":"aabbccddeeff"},"error":""}`, &rec)
	c := New(srv.URL)

	cmdID, nodeID, err := c.Command("blinky", "set",
		map[string]any{"app": "sampler", "key": "interval", "value": 30})
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	if cmdID != 7 || nodeID != "aabbccddeeff" {
		t.Fatalf("cmdID=%d nodeID=%q", cmdID, nodeID)
	}
	if rec.method != "POST" || rec.path != "/api/nodes/blinky/commands" {
		t.Fatalf("request = %s %s", rec.method, rec.path)
	}
	if !strings.Contains(rec.contentType, "application/json") {
		t.Errorf("content-type=%q", rec.contentType)
	}
	// Body is a {verb,args} envelope.
	var got struct {
		Verb string                 `json:"verb"`
		Args map[string]interface{} `json:"args"`
	}
	if err := json.Unmarshal([]byte(rec.body), &got); err != nil {
		t.Fatalf("decode sent body: %v — %s", err, rec.body)
	}
	if got.Verb != "set" || got.Args["app"] != "sampler" || got.Args["key"] != "interval" {
		t.Errorf("sent body = %+v", got)
	}
}

func TestCommandServerErrorBecomesError(t *testing.T) {
	var rec recordedReq
	srv := stubServer(t, http.StatusBadRequest,
		`{"ok":false,"data":null,"error":"set-power-mode: invalid mode"}`, &rec)
	c := New(srv.URL)
	_, _, err := c.Command("n", "set-power-mode", map[string]any{"mode": "turbo"})
	if err == nil || !strings.Contains(err.Error(), "invalid mode") {
		t.Fatalf("want server error string, got %v", err)
	}
}

func TestCommandTransportErrorWrap(t *testing.T) {
	// Start a server, capture its URL, then close it so the connection is refused.
	var rec recordedReq
	srv := stubServer(t, http.StatusOK, `{"ok":true,"data":{},"error":""}`, &rec)
	url := srv.URL
	srv.Close()
	c := New(url)
	_, _, err := c.Command("n", "set-console", map[string]any{"state": "on"})
	if err == nil || !strings.Contains(err.Error(), "porta serve") {
		t.Fatalf("want friendly 'porta serve' wrap, got %v", err)
	}
}

func TestSelectorIsPathEscaped(t *testing.T) {
	var rec recordedReq
	srv := stubServer(t, http.StatusOK,
		`{"ok":true,"data":{"command_id":1,"node_id":"x"},"error":""}`, &rec)
	c := New(srv.URL)
	if _, _, err := c.Command("a b/c", "stop", map[string]any{"name": "app"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rec.path, "a%20b") {
		t.Errorf("selector not path-escaped: %q", rec.path)
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/apiclient/`
Expected: FAIL — `undefined: New` (package/file not created yet).

- [ ] **Step 4: Write the implementation**

Create `internal/apiclient/client.go`:

```go
// Package apiclient is the write-side HTTP client for the porta control-plane
// API (internal/apisrv). It is cobra-free and store-free: the CLI's mutating
// commands use it to POST/PATCH the server instead of opening the store, which
// keeps the server the single writer (one trustworthy audit trail).
package apiclient

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client targets one porta server's /api surface.
type Client struct {
	baseURL string
	http    *http.Client
}

// New builds a Client for baseURL (a trailing slash is trimmed). The 30s
// overall timeout is generous enough for a multipart image upload while still
// bounding a hung server.
func New(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// envelope mirrors apisrv's {ok,data,error} response shape.
type envelope struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data"`
	Error string          `json:"error"`
}

// do sends req and returns the envelope's data on success. A transport failure
// is wrapped with a friendly "is porta serve running?" hint; a non-2xx status
// or ok=false returns the server's error string verbatim (so CLI output reads
// the same as the old control errors).
func (c *Client) do(req *http.Request) (json.RawMessage, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach porta server at %s — is `porta serve` running? (%v)", c.baseURL, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var env envelope
	if jerr := json.Unmarshal(body, &env); jerr != nil {
		return nil, fmt.Errorf("invalid response from %s (status %d): %s",
			c.baseURL, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if resp.StatusCode/100 != 2 || !env.OK {
		if env.Error != "" {
			return nil, fmt.Errorf("%s", env.Error)
		}
		return nil, fmt.Errorf("server returned status %d", resp.StatusCode)
	}
	return env.Data, nil
}

// cmdResp decodes a command/stop write response.
type cmdResp struct {
	CommandID int64  `json:"command_id"`
	NodeID    string `json:"node_id"`
}

// Command marshals {verb,args}, POSTs it to /api/nodes/{sel}/commands, and
// returns the queued command id plus the server-resolved 12-hex node id.
func (c *Client) Command(sel, verb string, args any) (int64, string, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return 0, "", err
	}
	body, err := json.Marshal(struct {
		Verb string          `json:"verb"`
		Args json.RawMessage `json:"args"`
	}{Verb: verb, Args: raw})
	if err != nil {
		return 0, "", err
	}
	req, err := http.NewRequest("POST",
		c.baseURL+"/api/nodes/"+url.PathEscape(sel)+"/commands", bytes.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	data, err := c.do(req)
	if err != nil {
		return 0, "", err
	}
	var r cmdResp
	if err := json.Unmarshal(data, &r); err != nil {
		return 0, "", err
	}
	return r.CommandID, r.NodeID, nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/apiclient/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/apiclient/client.go internal/apiclient/client_test.go
git commit -m "feat(apiclient): Client + Command over the S1 commands endpoint"
```

---

## Task 2: `apiclient.Install` (multipart upload)

**Files:**
- Modify: `internal/apiclient/client.go`
- Test: `internal/apiclient/client_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/apiclient/client_test.go`:

```go
func TestInstallBuildsMultipart(t *testing.T) {
	var rec recordedReq
	srv := stubServer(t, http.StatusOK,
		`{"ok":true,"data":{"command_id":6,"node_id":"aabbccddeeff","size":16},"error":""}`, &rec)
	c := New(srv.URL)

	img := strings.NewReader("fake-image-bytes")
	cmdID, nodeID, size, err := c.Install("blinky", "blink", img, InstallOpts{
		Lifecycle: "run-loop", Runlevel: 3, IntervalS: 30, Triggers: []string{"boot", "gpio-high=21"},
	})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if cmdID != 6 || nodeID != "aabbccddeeff" || size != 16 {
		t.Fatalf("cmdID=%d nodeID=%q size=%d", cmdID, nodeID, size)
	}
	if rec.method != "POST" || rec.path != "/api/nodes/blinky/containers" {
		t.Fatalf("request = %s %s", rec.method, rec.path)
	}
	if !strings.HasPrefix(rec.contentType, "multipart/form-data") {
		t.Fatalf("content-type=%q", rec.contentType)
	}
	// The body must carry the image file part and the form fields.
	for _, want := range []string{
		`name="image"`, `filename="blink.bin"`, "fake-image-bytes",
		`name="name"`, "blink",
		`name="lifecycle"`, "run-loop",
		`name="runlevel"`, "3",
		`name="interval"`, "30",
		`name="trigger"`, "boot", "gpio-high=21",
	} {
		if !strings.Contains(rec.body, want) {
			t.Errorf("multipart body missing %q", want)
		}
	}
}

func TestInstallOmitsZeroInterval(t *testing.T) {
	var rec recordedReq
	srv := stubServer(t, http.StatusOK,
		`{"ok":true,"data":{"command_id":1,"node_id":"x","size":1},"error":""}`, &rec)
	c := New(srv.URL)
	if _, _, _, err := c.Install("n", "app", strings.NewReader("x"),
		InstallOpts{Lifecycle: "run-once", Runlevel: 3}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rec.body, `name="interval"`) {
		t.Error("interval field should be omitted when IntervalS == 0")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/apiclient/ -run TestInstall`
Expected: FAIL — `undefined: InstallOpts` / `c.Install undefined`.

- [ ] **Step 3: Write the implementation**

Add to the imports in `internal/apiclient/client.go`: `"mime/multipart"` and `"strconv"`. Then append:

```go
// InstallOpts carries the client-facing install knobs. CRC and size are
// server-computed (the server owns the CRC; size comes back in the response).
type InstallOpts struct {
	Lifecycle string
	Runlevel  int
	IntervalS int64
	Triggers  []string
}

// installResp decodes a container-install write response.
type installResp struct {
	CommandID int64  `json:"command_id"`
	NodeID    string `json:"node_id"`
	Size      int64  `json:"size"`
}

// Install builds a multipart body (an "image" file part named "<name>.bin" plus
// name/lifecycle/runlevel/interval and repeatable "trigger" fields) and POSTs
// it to /api/nodes/{sel}/containers. The server computes the CRC and registers
// the payload; it returns the queued run command id, the resolved node id, and
// the stored image size.
func (c *Client) Install(sel, name string, image io.Reader, opts InstallOpts) (int64, string, int64, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("image", name+".bin")
	if err != nil {
		return 0, "", 0, err
	}
	if _, err := io.Copy(fw, image); err != nil {
		return 0, "", 0, err
	}
	_ = mw.WriteField("name", name)
	if opts.Lifecycle != "" {
		_ = mw.WriteField("lifecycle", opts.Lifecycle)
	}
	_ = mw.WriteField("runlevel", strconv.Itoa(opts.Runlevel))
	if opts.IntervalS != 0 {
		// The server re-parses this with command.ParseDurationSeconds, which
		// accepts a bare integer as seconds.
		_ = mw.WriteField("interval", strconv.FormatInt(opts.IntervalS, 10))
	}
	for _, t := range opts.Triggers {
		_ = mw.WriteField("trigger", t)
	}
	if err := mw.Close(); err != nil {
		return 0, "", 0, err
	}
	req, err := http.NewRequest("POST",
		c.baseURL+"/api/nodes/"+url.PathEscape(sel)+"/containers", &buf)
	if err != nil {
		return 0, "", 0, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	data, err := c.do(req)
	if err != nil {
		return 0, "", 0, err
	}
	var r installResp
	if err := json.Unmarshal(data, &r); err != nil {
		return 0, "", 0, err
	}
	return r.CommandID, r.NodeID, r.Size, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/apiclient/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/apiclient/client.go internal/apiclient/client_test.go
git commit -m "feat(apiclient): Install via multipart to the containers endpoint"
```

---

## Task 3: `apiclient.PatchNode`

**Files:**
- Modify: `internal/apiclient/client.go`
- Test: `internal/apiclient/client_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/apiclient/client_test.go`:

```go
func TestPatchNodeSendsOnlyPresentFields(t *testing.T) {
	var rec recordedReq
	srv := stubServer(t, http.StatusOK,
		`{"ok":true,"data":{"node_id":"aabbccddeeff"},"error":""}`, &rec)
	c := New(srv.URL)

	name := "rename"
	nodeID, err := c.PatchNode("blinky", &name, nil)
	if err != nil {
		t.Fatalf("PatchNode: %v", err)
	}
	if nodeID != "aabbccddeeff" {
		t.Fatalf("nodeID=%q", nodeID)
	}
	if rec.method != "PATCH" || rec.path != "/api/nodes/blinky" {
		t.Fatalf("request = %s %s", rec.method, rec.path)
	}
	var body map[string]interface{}
	if err := json.Unmarshal([]byte(rec.body), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["name"] != "rename" {
		t.Errorf("name not sent: %v", body)
	}
	if _, ok := body["max_offline_s"]; ok {
		t.Errorf("max_offline_s must be omitted when nil: %v", body)
	}
}

func TestPatchNodeMaxOffline(t *testing.T) {
	var rec recordedReq
	srv := stubServer(t, http.StatusOK,
		`{"ok":true,"data":{"node_id":"x"},"error":""}`, &rec)
	c := New(srv.URL)
	secs := int64(600)
	if _, err := c.PatchNode("n", nil, &secs); err != nil {
		t.Fatal(err)
	}
	var body map[string]interface{}
	json.Unmarshal([]byte(rec.body), &body)
	if body["max_offline_s"].(float64) != 600 {
		t.Errorf("max_offline_s=%v", body["max_offline_s"])
	}
	if _, ok := body["name"]; ok {
		t.Errorf("name must be omitted when nil: %v", body)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/apiclient/ -run TestPatchNode`
Expected: FAIL — `c.PatchNode undefined`.

- [ ] **Step 3: Write the implementation**

Append to `internal/apiclient/client.go`:

```go
// patchResp decodes a PATCH /api/nodes/{sel} response.
type patchResp struct {
	NodeID string `json:"node_id"`
}

// PatchNode PATCHes only the present (non-nil) fields to /api/nodes/{sel} and
// returns the server-resolved node id. Used for rename and max-offline, which
// are gateway-side settings (not device commands).
func (c *Client) PatchNode(sel string, name *string, maxOfflineS *int64) (string, error) {
	body := map[string]any{}
	if name != nil {
		body["name"] = *name
	}
	if maxOfflineS != nil {
		body["max_offline_s"] = *maxOfflineS
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest("PATCH",
		c.baseURL+"/api/nodes/"+url.PathEscape(sel), bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	data, err := c.do(req)
	if err != nil {
		return "", err
	}
	var r patchResp
	if err := json.Unmarshal(data, &r); err != nil {
		return "", err
	}
	return r.NodeID, nil
}
```

- [ ] **Step 4: Run the full apiclient suite to verify it passes**

Run: `go test ./internal/apiclient/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/apiclient/client.go internal/apiclient/client_test.go
git commit -m "feat(apiclient): PatchNode for rename / max-offline"
```

---

## Task 4: Server-side `EnsureNode` on the write path

**Files:**
- Modify: `internal/apisrv/commands.go:20-39` (`handleCommand`)
- Modify: `internal/apisrv/containers.go:18-27` (`handleContainerInstall`, right after `resolveSel`)
- Modify: `internal/apisrv/nodes.go:109-118` (`handlePatchNode`, right after `resolveSel`)
- Test: `internal/apisrv/commands_test.go`

**Why:** The CLI used to call `st.EnsureNode(id)` before enqueuing, so a command could be pre-queued for a well-formed MAC that has never checked in (bench pre-provisioning). The S1 write handlers do not ensure the node. Add it on the write path; reads stay non-creating.

- [ ] **Step 1: Write the failing test**

Append to `internal/apisrv/commands_test.go`:

```go
// TestPostCommandEnsuresNode verifies EnsureNode-on-write: a command for a
// well-formed but never-seen MAC creates the node row and queues the command
// (preserves bench pre-provisioning).
func TestPostCommandEnsuresNode(t *testing.T) {
	h, st := newTestHandler(t)
	// "aabbccddeeff" is never touched — no node row exists.
	rec := postCmd(t, h, "aabbccddeeff", `{"verb":"set-console","args":{"state":"on"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	n, err := st.GetNode("aabbccddeeff")
	if err != nil {
		t.Fatal(err)
	}
	if n == nil {
		t.Fatal("EnsureNode-on-write should have created the node row")
	}
	cmd, _ := st.NextUndelivered("aabbccddeeff")
	if cmd == nil || cmd.Verb != "set-console" {
		t.Fatalf("queued=%+v", cmd)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/apisrv/ -run TestPostCommandEnsuresNode`
Expected: FAIL — `n == nil` (no node row was created without EnsureNode).

- [ ] **Step 3: Add EnsureNode to `handleCommand`**

In `internal/apisrv/commands.go`, edit `handleCommand` so the block right after `resolveSel` ensures the node:

```go
func (h *Handler) handleCommand(w http.ResponseWriter, r *http.Request) {
	id, ok := h.resolveSel(w, r.PathValue("sel"))
	if !ok {
		return
	}
	// EnsureNode-on-write: a well-formed MAC may be addressed before its first
	// poll (bench pre-provisioning). Reads stay non-creating.
	if err := h.st.EnsureNode(id, h.now()); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
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
```

- [ ] **Step 4: Add EnsureNode to `handleContainerInstall`**

In `internal/apisrv/containers.go`, insert the EnsureNode block right after `resolveSel`:

```go
	id, ok := h.resolveSel(w, r.PathValue("sel"))
	if !ok {
		return
	}
	if err := h.st.EnsureNode(id, h.now()); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
```

- [ ] **Step 5: Add EnsureNode to `handlePatchNode`**

In `internal/apisrv/nodes.go`, insert the EnsureNode block right after `resolveSel`:

```go
	id, ok := h.resolveSel(w, r.PathValue("sel"))
	if !ok {
		return
	}
	if err := h.st.EnsureNode(id, h.now()); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var p nodePatch
```

- [ ] **Step 6: Run the apisrv suite to verify it passes**

Run: `go test ./internal/apisrv/`
Expected: PASS (new test green; existing tests unaffected — they `TouchNode` first, and EnsureNode is a no-op on an existing row via `ON CONFLICT DO NOTHING`).

- [ ] **Step 7: Commit**

```bash
git add internal/apisrv/commands.go internal/apisrv/containers.go internal/apisrv/nodes.go internal/apisrv/commands_test.go
git commit -m "feat(apisrv): EnsureNode on the write path (preserve bench pre-provisioning)"
```

---

## Task 5: Server echoes `node_id` in write responses (+ S1 spec §4)

**Files:**
- Modify: `internal/apisrv/commands.go:38` (`writeOK` in `handleCommand`)
- Modify: `internal/apisrv/containers.go:68` (`writeOK` in `handleContainerInstall`)
- Modify: `internal/apisrv/nodes.go:131` (`writeOK` in `handlePatchNode`)
- Modify: `internal/apisrv/commands_test.go`, `internal/apisrv/containers_test.go`, `internal/apisrv/nodes_test.go`
- Modify: `docs/specs/2026-06-02-control-plane-api-s1.md` (§4 — additive `node_id`)

**Why:** CLI confirmation lines lead with the resolved MAC. With selectors resolved server-side, the server returns the resolved 12-hex id so the CLI can print it (and the operator gets a useful resolution check that catches a typo'd/ambiguous name).

- [ ] **Step 1: Write the failing tests**

Append to `internal/apisrv/commands_test.go`:

```go
// TestPostCommandEchoesNodeID asserts the write response carries the resolved
// node id (resolving by name proves it is the 12-hex id, not the selector).
func TestPostCommandEchoesNodeID(t *testing.T) {
	h, st := newTestHandler(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	st.SetNodeName("aabbccddeeff", "blinky")
	rec := postCmd(t, h, "blinky", `{"verb":"set-console","args":{"state":"on"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			NodeID string `json:"node_id"`
		} `json:"data"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Data.NodeID != "aabbccddeeff" {
		t.Errorf("node_id=%q, want aabbccddeeff", resp.Data.NodeID)
	}
}
```

Append to `internal/apisrv/nodes_test.go`:

```go
// TestPatchNodeEchoesNodeID asserts PATCH returns the resolved node id.
func TestPatchNodeEchoesNodeID(t *testing.T) {
	h, st := newTestHandler(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	st.SetNodeName("aabbccddeeff", "blinky")
	rec := patchNode(t, h, "blinky", `{"name":"renamed"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			NodeID string `json:"node_id"`
		} `json:"data"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Data.NodeID != "aabbccddeeff" {
		t.Errorf("node_id=%q", resp.Data.NodeID)
	}
}
```

Append to `internal/apisrv/containers_test.go`, reusing the file's existing
`postContainer(t, h, sel, img, fields)` helper (it builds the multipart request
and returns the recorder):

```go
// TestInstallEchoesNodeID asserts the install response carries the resolved id.
func TestInstallEchoesNodeID(t *testing.T) {
	h, st := newTestHandler(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	st.SetNodeName("aabbccddeeff", "blinky")

	rec := postContainer(t, h, "blinky", []byte("fake-image-bytes"), map[string]string{
		"name": "blink", "lifecycle": "run-once", "runlevel": "3",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			NodeID string `json:"node_id"`
			Size   int64  `json:"size"`
		} `json:"data"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Data.NodeID != "aabbccddeeff" {
		t.Errorf("node_id=%q", resp.Data.NodeID)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/apisrv/ -run NodeID`
Expected: FAIL — `node_id` is empty (`""`), not the resolved id.

- [ ] **Step 3: Add `node_id` to the three write responses**

`internal/apisrv/commands.go` — change the success line of `handleCommand`:

```go
	writeOK(w, map[string]any{"command_id": cmdID, "node_id": id})
```

`internal/apisrv/containers.go` — change the success line of `handleContainerInstall`:

```go
	writeOK(w, map[string]any{"command_id": cmdID, "node_id": id, "size": hdr.Size})
```

`internal/apisrv/nodes.go` — change the success line of `handlePatchNode`:

```go
	writeOK(w, map[string]any{"node_id": id})
```

- [ ] **Step 4: Run the apisrv suite to verify it passes**

Run: `go test ./internal/apisrv/`
Expected: PASS.

- [ ] **Step 5: Reconcile the S1 spec (§4)**

Read `docs/specs/2026-06-02-control-plane-api-s1.md` §4 and update the three write-response shapes to the as-built additive form, plus note EnsureNode-on-write. Add a short paragraph:

> **S2 backport (2026-06-02):** the three write responses now also carry
> `node_id` (the server-resolved 12-hex id), and the write handlers call
> `EnsureNode` after selector resolution so a well-formed MAC can be addressed
> before its first poll (bench pre-provisioning). Both are additive and
> backward-compatible. Response shapes:
> `POST /commands` → `{command_id, node_id}`;
> `POST /containers` → `{command_id, node_id, size}`;
> `PATCH /nodes/{sel}` → `{node_id}`.

- [ ] **Step 6: Commit**

```bash
git add internal/apisrv/ docs/specs/2026-06-02-control-plane-api-s1.md
git commit -m "feat(apisrv): echo node_id in write responses; reconcile S1 spec §4"
```

---

## Task 6: `--server` persistent flag + `serverURL()` resolver

**Files:**
- Create: `internal/portacli/client.go`
- Modify: `internal/portacli/root.go:24-41` (register the persistent flag)
- Test: `internal/portacli/client_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/portacli/client_test.go`:

```go
package portacli

import "testing"

func TestServerURLPrecedence(t *testing.T) {
	// Flag set → flag wins over env and default.
	serverFlag = "http://flag:1"
	t.Setenv("PORTA_SERVER", "http://env:2")
	if got := serverURL(); got != "http://flag:1" {
		t.Errorf("flag should win: %q", got)
	}

	// Flag empty, env set → env wins over default.
	serverFlag = ""
	if got := serverURL(); got != "http://env:2" {
		t.Errorf("env should win: %q", got)
	}

	// Flag empty, env empty → default.
	serverFlag = ""
	t.Setenv("PORTA_SERVER", "")
	if got := serverURL(); got != "http://localhost:6970" {
		t.Errorf("default should win: %q", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/portacli/ -run TestServerURLPrecedence`
Expected: FAIL — `undefined: serverFlag` / `undefined: serverURL`.

- [ ] **Step 3: Write the implementation**

Create `internal/portacli/client.go`:

```go
package portacli

import "os"

// serverFlag holds the value of the persistent --server flag (registered in
// root.go). Empty means "fall back to $PORTA_SERVER, then the default".
var serverFlag string

// serverURL resolves the porta server base URL: --server, then $PORTA_SERVER,
// then http://localhost:6970 (matches serve's default --http-port). Only the 8
// mutating commands consume it; reads stay db-backed.
func serverURL() string {
	if serverFlag != "" {
		return serverFlag
	}
	if env := os.Getenv("PORTA_SERVER"); env != "" {
		return env
	}
	return "http://localhost:6970"
}
```

- [ ] **Step 4: Register the persistent flag in `root.go`**

In `internal/portacli/root.go`, add the `--server` flag next to `--db` inside `NewRootCmd`:

```go
	root.PersistentFlags().StringVar(&dbPath, "db", "porta.db", "SQLite database path")
	root.PersistentFlags().StringVar(&serverFlag, "server", "",
		"porta server base URL for write commands (default $PORTA_SERVER or http://localhost:6970)")
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/portacli/ -run TestServerURLPrecedence`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/portacli/client.go internal/portacli/client_test.go internal/portacli/root.go
git commit -m "feat(portacli): --server persistent flag + serverURL() resolver"
```

---

## Task 7: Re-point all 8 write commands in `mutate.go` to the API client

**Files:**
- Modify: `internal/portacli/mutate.go` (whole-file re-point — all 8 commands)
- Modify: `internal/portacli/mutate_test.go` (rewrite core tests against the real `apisrv` over `httptest`)
- Modify: `internal/portacli/config_test.go` (delete the 5 write-core tests that call the old signatures; keep the `runDeviceGet` read-core tests + `mustCommands`)

**Why one task:** Go compiles a package as a unit, and `mutate.go`'s imports (`store`, `control`, `bytes`) are shared across all 8 commands. Re-pointing them together keeps the file compiling at the task boundary. The `inspect.go` reads stay db-backed and keep `openStore()`. **`config_test.go` calls `runDeviceSet`/`runDeviceSetConsole` with the old store-based signatures, so it MUST be migrated in this same task or the package won't compile.**

**Behavior change (accepted):** all 8 writes now flow through the API, so the command-log `issued_by` becomes `"api"` (the apisrv tag) instead of `"cli"`. The old `config_test.go` asserted `issued_by="cli"`; that assertion is dropped (the migrated tests don't re-assert it, and `porta log` will show `api`). This is the intended consequence of "one writer".

**Parity rules carried from the old code (do not regress):**
- `device set`, `set-console`, `set-power-mode`, `container install`, `container uninstall` **print** a confirmation line. They now lead with `node_id` from the response.
- `set-poll-interval`, `device name`, `set-max-offline` were **silent** (returned only an error). Keep them silent.
- `container install`'s line **drops the CRC** (`@<crc>`) — the server owns the CRC; it stays visible via `porta log`. The line uses `size` from the response.
- The `--crc` install flag is **removed** (server computes the CRC).
- The "no triggers given" warning stays client-side, printed before the call.
- `set-console` / `set-power-mode` value validation now happens **server-side** (single source of truth); the CLI passes the raw token through.
- `set-max-offline`'s duration is parsed **client-side** (it is an int64 PATCH field); `set-poll-interval`'s duration is passed **raw** (the server parses it).

- [ ] **Step 1: Rewrite the test file (failing)**

Replace the entire contents of `internal/portacli/mutate_test.go` with:

```go
package portacli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/davidg238/porta/internal/apiclient"
	"github.com/davidg238/porta/internal/apisrv"
	"github.com/davidg238/porta/internal/store"
)

// newClientServer stands up the REAL apisrv.Handler over a temp store behind an
// httptest server and returns a client pointed at it plus the store (so tests
// can assert what landed). This gives true CLI-core → HTTP → apisrv → store
// coverage.
func newClientServer(t *testing.T) (*apiclient.Client, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/m.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	mux := http.NewServeMux()
	apisrv.New(st).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return apiclient.New(srv.URL), st
}

func TestRunDeviceSetEnqueuesAndPrints(t *testing.T) {
	c, st := newClientServer(t)
	var out bytes.Buffer
	// Selector is a well-formed MAC never seen → EnsureNode-on-write creates it.
	if err := runDeviceSet(&out, c, "aabbccddeeff", "sampler", "interval", "30"); err != nil {
		t.Fatal(err)
	}
	cmd, _ := st.NextUndelivered("aabbccddeeff")
	if cmd == nil || cmd.Verb != "set" {
		t.Fatalf("queued=%+v", cmd)
	}
	// Output leads with the resolved MAC and an int-shaped value.
	if !strings.Contains(out.String(), "aabbccddeeff: enqueued set sampler.interval=30 (command #") {
		t.Errorf("output = %q", out.String())
	}
}

// TestRunDeviceSetTypeInference preserves the int/float/bool/string inference
// coverage migrated out of config_test.go: config.InferScalar runs client-side,
// the typed value rides in the JSON value field, and the server coerces it back
// to the right scalar in the queued command args.
func TestRunDeviceSetTypeInference(t *testing.T) {
	cases := []struct {
		name, value, wantArgs string
	}{
		{"int", "30", `{"app":"a","key":"k","value":30}`},
		{"float", "21.5", `{"app":"a","key":"k","value":21.5}`},
		{"bool", "true", `{"app":"a","key":"k","value":true}`},
		{"string", "eco", `{"app":"a","key":"k","value":"eco"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, st := newClientServer(t)
			var out bytes.Buffer
			if err := runDeviceSet(&out, c, "aabbccddeeff", "a", "k", tc.value); err != nil {
				t.Fatal(err)
			}
			next, _ := st.NextUndelivered("aabbccddeeff")
			if next == nil || next.Args != tc.wantArgs {
				t.Errorf("Args=%v, want %s", next, tc.wantArgs)
			}
		})
	}
}

func TestRunDeviceSetConsole(t *testing.T) {
	c, st := newClientServer(t)
	var out bytes.Buffer
	if err := runDeviceSetConsole(&out, c, "aabbccddeeff", "on"); err != nil {
		t.Fatal(err)
	}
	cmd, _ := st.NextUndelivered("aabbccddeeff")
	if cmd == nil || cmd.Verb != "set-console" {
		t.Fatalf("queued=%+v", cmd)
	}
	if !strings.Contains(out.String(), "aabbccddeeff: enqueued set-console on (command #") {
		t.Errorf("output = %q", out.String())
	}
}

func TestRunDeviceSetConsoleBadStateIsServerError(t *testing.T) {
	c, _ := newClientServer(t)
	var out bytes.Buffer
	err := runDeviceSetConsole(&out, c, "aabbccddeeff", "maybe")
	if err == nil || !strings.Contains(err.Error(), "on or off") {
		t.Fatalf("want server validation error, got %v", err)
	}
}

func TestRunDeviceSetPowerMode(t *testing.T) {
	c, st := newClientServer(t)
	var out bytes.Buffer
	if err := runDeviceSetPowerMode(&out, c, "aabbccddeeff", "always-on"); err != nil {
		t.Fatal(err)
	}
	cmd, _ := st.NextUndelivered("aabbccddeeff")
	if cmd == nil || cmd.Verb != "set-power-mode" {
		t.Fatalf("queued=%+v", cmd)
	}
	if !strings.Contains(out.String(), "aabbccddeeff: enqueued set-power-mode always-on (command #") {
		t.Errorf("output = %q", out.String())
	}
}

func TestRunSetPollIntervalEnqueuesSilently(t *testing.T) {
	c, st := newClientServer(t)
	if err := runSetPollInterval(c, "aabbccddeeff", "45s"); err != nil {
		t.Fatal(err)
	}
	cmd, _ := st.NextUndelivered("aabbccddeeff")
	if cmd == nil || cmd.Verb != "set-poll-interval" {
		t.Fatalf("queued=%+v", cmd)
	}
}

func TestRunUninstallEnqueuesStop(t *testing.T) {
	c, st := newClientServer(t)
	var out bytes.Buffer
	if err := runUninstall(&out, c, "aabbccddeeff", "blink"); err != nil {
		t.Fatal(err)
	}
	cmd, _ := st.NextUndelivered("aabbccddeeff")
	if cmd == nil || cmd.Verb != "stop" || cmd.Args != `{"name":"blink"}` {
		t.Fatalf("queued=%+v", cmd)
	}
	if !strings.Contains(out.String(), "aabbccddeeff: enqueued stop blink (command #") {
		t.Errorf("output = %q", out.String())
	}
}

func TestRunInstallRegistersAndPrintsWithoutCRC(t *testing.T) {
	c, st := newClientServer(t)
	dir := t.TempDir()
	bin := filepath.Join(dir, "blink.bin")
	img := []byte("fake-image-bytes")
	if err := os.WriteFile(bin, img, 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runInstall(&out, c, "aabbccddeeff", "blink", bin, apiclient.InstallOpts{
		Lifecycle: "run-loop", Runlevel: 3, Triggers: []string{"boot"},
	}); err != nil {
		t.Fatal(err)
	}
	cmd, _ := st.NextUndelivered("aabbccddeeff")
	if cmd == nil || cmd.Verb != "run" {
		t.Fatalf("queued=%+v", cmd)
	}
	var args map[string]interface{}
	json.Unmarshal([]byte(cmd.Args), &args)
	if args["size"].(float64) != float64(len(img)) {
		t.Errorf("size arg = %v, want %d", args["size"], len(img))
	}
	s := out.String()
	if !strings.Contains(s, "aabbccddeeff: registered blink (16 B); enqueued run (command #") {
		t.Errorf("output = %q", s)
	}
	if strings.Contains(s, "@") {
		t.Errorf("CRC should be dropped from the install line: %q", s)
	}
}

func TestRunInstallRejectsNonBin(t *testing.T) {
	c, _ := newClientServer(t)
	dir := t.TempDir()
	pod := filepath.Join(dir, "x.pod")
	os.WriteFile(pod, []byte("x"), 0o644)
	var out bytes.Buffer
	if err := runInstall(&out, c, "aabbccddeeff", "x", pod, apiclient.InstallOpts{Lifecycle: "run-once"}); err == nil {
		t.Error(".pod must be rejected (only .bin)")
	}
}

func TestRunDeviceName(t *testing.T) {
	c, st := newClientServer(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	if err := runDeviceName(c, "aabbccddeeff", "newname"); err != nil {
		t.Fatal(err)
	}
	n, _ := st.GetNode("aabbccddeeff")
	if n.Name != "newname" {
		t.Errorf("name=%q", n.Name)
	}
}

func TestRunSetMaxOffline(t *testing.T) {
	c, st := newClientServer(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	if err := runSetMaxOffline(c, "aabbccddeeff", 600); err != nil {
		t.Fatal(err)
	}
	n, _ := st.GetNode("aabbccddeeff")
	if n.MaxOfflineS != 600 {
		t.Errorf("max_offline_s=%d", n.MaxOfflineS)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails (compile failure)**

Run: `go test ./internal/portacli/ -run TestRun`
Expected: FAIL — the old core signatures (`runDeviceSet(out, *store.Store, id, …)`, `runInstall(*store.Store, …)`, etc.) don't match; `runDeviceName`/`runSetMaxOffline` don't exist.

- [ ] **Step 3: Rewrite `mutate.go`**

Replace the entire contents of `internal/portacli/mutate.go` with:

```go
package portacli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/davidg238/porta/internal/apiclient"
	"github.com/davidg238/porta/internal/command"
	"github.com/davidg238/porta/internal/config"
	"github.com/spf13/cobra"
)

// --- testable cores: each takes an *apiclient.Client and the RAW -d selector
// (the server resolves it); confirmation lines lead with the resolved node_id. ---

// runDeviceSet infers the scalar type from the operator's string and enqueues a
// set command via the API.
func runDeviceSet(out io.Writer, c *apiclient.Client, sel, app, key, valueStr string) error {
	value := config.InferScalar(valueStr)
	cmdID, nodeID, err := c.Command(sel, "set", map[string]any{"app": app, "key": key, "value": value})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: enqueued set %s.%s=%v (command #%d)\n", nodeID, app, key, value, cmdID)
	return nil
}

// runDeviceSetConsole enqueues a set-console command. The on/off token is
// validated server-side.
func runDeviceSetConsole(out io.Writer, c *apiclient.Client, sel, state string) error {
	cmdID, nodeID, err := c.Command(sel, "set-console", map[string]any{"state": state})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: enqueued set-console %s (command #%d)\n", nodeID, state, cmdID)
	return nil
}

// runDeviceSetPowerMode enqueues a set-power-mode command. The mode is validated
// server-side (command.SetPowerMode).
func runDeviceSetPowerMode(out io.Writer, c *apiclient.Client, sel, mode string) error {
	cmdID, nodeID, err := c.Command(sel, "set-power-mode", map[string]any{"mode": mode})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: enqueued set-power-mode %s (command #%d)\n", nodeID, mode, cmdID)
	return nil
}

// runSetPollInterval enqueues a set-poll-interval command. The duration string
// is parsed server-side. Silent on success (parity with the pre-S2 CLI).
func runSetPollInterval(c *apiclient.Client, sel, dur string) error {
	_, _, err := c.Command(sel, "set-poll-interval", map[string]any{"interval": dur})
	return err
}

// runUninstall enqueues a stop command.
func runUninstall(out io.Writer, c *apiclient.Client, sel, name string) error {
	cmdID, nodeID, err := c.Command(sel, "stop", map[string]any{"name": name})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: enqueued stop %s (command #%d)\n", nodeID, name, cmdID)
	return nil
}

// runInstall uploads a prebuilt .bin via multipart; the server computes the CRC
// and registers the payload. The confirmation drops the CRC (visible via
// `porta log`) and uses the server-reported size.
func runInstall(out io.Writer, c *apiclient.Client, sel, name, path string, opts apiclient.InstallOpts) error {
	if !strings.HasSuffix(path, ".bin") {
		return fmt.Errorf("unsupported file %q (B1 accepts only prebuilt .bin)", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if len(opts.Triggers) == 0 && opts.IntervalS == 0 {
		fmt.Fprintf(out, "note: no triggers given — %q installed but not started\n", name)
	}
	cmdID, nodeID, size, err := c.Install(sel, name, f, opts)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: registered %s (%d B); enqueued run (command #%d)\n", nodeID, name, size, cmdID)
	return nil
}

// runDeviceName renames a node (gateway-side). Silent on success (parity).
func runDeviceName(c *apiclient.Client, sel, newName string) error {
	_, err := c.PatchNode(sel, &newName, nil)
	return err
}

// runSetMaxOffline sets the offline threshold (gateway-side). Silent on success.
func runSetMaxOffline(c *apiclient.Client, sel string, secs int64) error {
	_, err := c.PatchNode(sel, nil, &secs)
	return err
}

// --- cobra wiring (attached to the parents from inspect.go) ---

func newContainerInstallCmd() *cobra.Command {
	var device string
	var opts apiclient.InstallOpts
	var interval string
	cmd := &cobra.Command{
		Use:   "install <name> <file.bin>",
		Short: "Register a prebuilt image and enqueue run",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if interval != "" {
				var err error
				if opts.IntervalS, err = command.ParseDurationSeconds(interval); err != nil {
					return err
				}
			}
			if opts.Lifecycle == "" {
				opts.Lifecycle = "run-once"
			}
			c := apiclient.New(serverURL())
			return runInstall(cmd.OutOrStdout(), c, device, args[0], args[1], opts)
		},
	}
	deviceFlag(cmd, &device)
	cmd.Flags().StringVar(&interval, "interval", "", "interval trigger (e.g. 30s)")
	cmd.Flags().StringArrayVar(&opts.Triggers, "trigger", nil, "trigger spec (boot, gpio-high=21, …); repeatable")
	cmd.Flags().IntVar(&opts.Runlevel, "runlevel", 3, "runlevel")
	cmd.Flags().StringVar(&opts.Lifecycle, "lifecycle", "run-once", "run-once or run-loop")
	return cmd
}

func newContainerUninstallCmd() *cobra.Command {
	var device string
	cmd := &cobra.Command{
		Use:   "uninstall <name>",
		Short: "Enqueue stop for an app",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := apiclient.New(serverURL())
			return runUninstall(cmd.OutOrStdout(), c, device, args[0])
		},
	}
	deviceFlag(cmd, &device)
	return cmd
}

func newDeviceSetPollIntervalCmd() *cobra.Command {
	var device string
	cmd := &cobra.Command{
		Use:   "set-poll-interval <dur>",
		Short: "Enqueue a poll-interval change",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := apiclient.New(serverURL())
			return runSetPollInterval(c, device, args[0])
		},
	}
	deviceFlag(cmd, &device)
	return cmd
}

func newDeviceSetMaxOfflineCmd() *cobra.Command {
	var device string
	cmd := &cobra.Command{
		Use:   "set-max-offline <dur>",
		Short: "Set the offline threshold (gateway-side only)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			secs, err := command.ParseDurationSeconds(args[0])
			if err != nil {
				return err
			}
			c := apiclient.New(serverURL())
			return runSetMaxOffline(c, device, secs)
		},
	}
	deviceFlag(cmd, &device)
	return cmd
}

func newDeviceNameCmd() *cobra.Command {
	var device string
	cmd := &cobra.Command{
		Use:   "name <new-name>",
		Short: "Override the auto-assigned friendly name",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := apiclient.New(serverURL())
			return runDeviceName(c, device, args[0])
		},
	}
	deviceFlag(cmd, &device)
	return cmd
}

func newDeviceSetCmd() *cobra.Command {
	var device string
	cmd := &cobra.Command{
		Use:   "set <app> <key> <value>",
		Short: "Enqueue a per-app config write (set verb)",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := apiclient.New(serverURL())
			return runDeviceSet(cmd.OutOrStdout(), c, device, args[0], args[1], args[2])
		},
	}
	deviceFlag(cmd, &device)
	return cmd
}

func newDeviceSetPowerModeCmd() *cobra.Command {
	var device string
	cmd := &cobra.Command{
		Use:   "set-power-mode <deep-sleep|always-on>",
		Short: "Set a node's power mode (always-on keeps run-loop daemons alive)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := apiclient.New(serverURL())
			return runDeviceSetPowerMode(cmd.OutOrStdout(), c, device, args[0])
		},
	}
	deviceFlag(cmd, &device)
	return cmd
}

func newDeviceSetConsoleCmd() *cobra.Command {
	var device string
	cmd := &cobra.Command{
		Use:   "set-console <on|off>",
		Short: "Toggle a node's console/telemetry forwarding",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := apiclient.New(serverURL())
			return runDeviceSetConsole(cmd.OutOrStdout(), c, device, args[0])
		},
	}
	deviceFlag(cmd, &device)
	return cmd
}
```

- [ ] **Step 4: Migrate `config_test.go`**

`config_test.go` mixes write-core tests (old signatures — now broken) with
read-core `runDeviceGet` tests (still valid). **Delete exactly these five
write-core test functions** (their behavior is now covered by `mutate_test.go`,
including the migrated type-inference test):

- `TestRunDeviceSetEnqueuesCli`
- `TestRunDeviceSetTypeInference`
- `TestRunDeviceSetConsoleOn`
- `TestRunDeviceSetConsoleOff`
- `TestRunDeviceSetConsoleRejectsBadState`

**Keep** everything else in the file: `TestRunDeviceGetSingleKeyConverged`,
`TestRunDeviceGetSingleKeyDrift`, `TestRunDeviceGetSingleKeyPending`,
`TestRunDeviceGetMultiKeyTable`, `TestRunDeviceGetWarningAtTwoOrMore`, and the
`mustCommands` helper. These call `runDeviceGet` (a read core in `inspect.go`,
unchanged) and still use `store` + `strings` + `bytes`, so the imports stay.

Do not touch `inspect_test.go`, `monitor_test.go`, `resolve_test.go`,
`serve_test.go`, or `run_test.go` — they exercise db-backed reads/run, unchanged.

- [ ] **Step 5: Run the portacli suite to verify it passes**

Run: `go test ./internal/portacli/`
Expected: PASS.

- [ ] **Step 6: Build the whole module to confirm nothing else referenced the old cores**

Run: `go build ./... && go vet ./internal/portacli/ ./internal/apiclient/`
Expected: clean.

- [ ] **Step 7: Commit**

```bash
git add internal/portacli/mutate.go internal/portacli/mutate_test.go internal/portacli/config_test.go
git commit -m "feat(portacli): re-point all 8 write commands to the HTTP API client"
```

---

## Task 8: End-to-end cobra → HTTP → apisrv → store test (via `--server`)

**Files:**
- Create: `internal/portacli/e2e_test.go`

**Why:** Tasks 1–7 cover the client, the server additions, and the cores. This task proves the cobra plumbing: the `--server` flag, selector passthrough, and a full mutate landing in the store — exercised by executing the assembled root command exactly as an operator would.

- [ ] **Step 1: Write the failing test**

Create `internal/portacli/e2e_test.go`:

```go
package portacli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/davidg238/porta/internal/apisrv"
	"github.com/davidg238/porta/internal/store"
)

// TestDeviceSetEndToEnd runs the real cobra command with --server pointed at a
// real apisrv over a temp store, proving flag plumbing + selector passthrough +
// the write landing in the store.
func TestDeviceSetEndToEnd(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/e2e.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)
	st.SetNodeName("aabbccddeeff", "blinky")

	mux := http.NewServeMux()
	apisrv.New(st).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	// Resolve by NAME to prove the server resolves the selector and echoes the MAC.
	root.SetArgs([]string{"device", "set", "sampler", "interval", "30",
		"-d", "blinky", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v — out=%s", err, out.String())
	}

	cmd, _ := st.NextUndelivered("aabbccddeeff")
	if cmd == nil || cmd.Verb != "set" {
		t.Fatalf("queued=%+v", cmd)
	}
	// Confirmation leads with the resolved MAC, not the name.
	if !strings.Contains(out.String(), "aabbccddeeff: enqueued set sampler.interval=30") {
		t.Errorf("output = %q", out.String())
	}
}

// TestServerDownIsFriendly proves the transport-error wrap surfaces through the
// command when no server is listening.
func TestServerDownIsFriendly(t *testing.T) {
	// Stand up then immediately close a server to get a refused port.
	srv := httptest.NewServer(http.NewServeMux())
	url := srv.URL
	srv.Close()

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"device", "set-console", "on", "-d", "aabbccddeeff", "--server", url})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "porta serve") {
		t.Fatalf("want friendly 'porta serve' error, got %v", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails (then passes)**

Run: `go test ./internal/portacli/ -run 'TestDeviceSetEndToEnd|TestServerDownIsFriendly'`
Expected: With Tasks 1–7 in place this should **PASS** immediately (it is integration coverage, not new behavior). If it fails, the failure pinpoints a plumbing gap — fix it before continuing. If you want a strict red→green, temporarily point `--server` at a wrong path and confirm the assertion fires, then restore.

- [ ] **Step 3: Run the full module test suite**

Run: `go test ./...`
Expected: PASS across all packages (the parked `cmd/st-devserver` + `internal/st` and `examples/toit-gateway` are separate; this command covers the Go mainline module — confirm no regression).

- [ ] **Step 4: Commit**

```bash
git add internal/portacli/e2e_test.go
git commit -m "test(portacli): end-to-end cobra→HTTP→apisrv→store via --server"
```

---

## Task 9: Manual smoke + docs touch-up

**Files:**
- Modify: `CLAUDE.md` is **not** touched (it's auto-context). Optionally update any operator README under `docs/` that documents the write commands needing `--server`.

- [ ] **Step 1: Build the binary**

Run: `go build -o /tmp/porta ./cmd/porta`
Expected: clean build.

- [ ] **Step 2: Smoke the write path against a live server**

In one shell (the user runs long-lived/interactive commands via `!`):

```bash
/tmp/porta serve --db /tmp/smoke.db
```

In another:

```bash
/tmp/porta device set sampler interval 30 -d aabbccddeeff
/tmp/porta log -d aabbccddeeff --db /tmp/smoke.db
```

Expected: the `set` prints `aabbccddeeff: enqueued set sampler.interval=30 (command #1)` (note int-shaped `30`, leading MAC), and `log` shows the queued `set` row. Then stop the server with Ctrl-C.

- [ ] **Step 3: Smoke the friendly error**

Run (with no server up): `/tmp/porta device set-console on -d aabbccddeeff`
Expected: an error mentioning *"is `porta serve` running?"*.

- [ ] **Step 4: Commit any doc touch-ups (if made)**

```bash
git add docs/
git commit -m "docs(porta): note --server for write commands (S2)"
```

(If no doc change was needed, skip this commit.)

---

## Done criteria

- `go test ./...` green.
- All 8 write commands POST/PATCH the S1 API; none open the store.
- `internal/portacli/mutate.go` no longer imports `store` or `control`.
- Reads (`scan`, `ping`, `device show`/`get`, `container list`, `log`), `run`, `monitor`, and `serve` are unchanged (still db-backed).
- The S1 spec §4 reflects the additive `node_id` field and EnsureNode-on-write.
- Confirmation lines lead with the resolved MAC; `container install` drops the CRC and shows server-reported size.

**Then:** invoke `superpowers:finishing-a-development-branch`. Per the project memory, the **user gates push-to-master and pushes via `!`** — do not push; present the merge/PR options and hand off.

---

## Self-Review (completed during planning)

**Spec coverage:**
- §2 scope (8 writes only) → Task 7 re-points exactly those 8; reads/run/monitor/serve untouched (Task 7 note + Done criteria). ✓
- §2 transport (3 endpoints, envelope) → Tasks 1–3. ✓
- §2 `--server` + `$PORTA_SERVER` default → Task 6. ✓
- §2 selector passthrough (server resolves) → cores take raw `sel`; Task 8 proves name→MAC. ✓
- §2 preserve `EnsureNode` server-side → Task 4. ✓
- §2 `node_id` in write responses → Task 5. ✓
- §2 "requires serve running" friendly error → `do()` wrap (Task 1) + Task 8 `TestServerDownIsFriendly`. ✓
- §3.1 apiclient (Client/New/InstallOpts/3 methods/envelope decode/path-escape) → Tasks 1–3. ✓
- §3.2 `serverURL()` → Task 6 (chosen home: `client.go`, resolving §8 open question). ✓
- §3.3 re-pointed command mapping (the table) → Task 7 cores + wiring match each row. ✓
- §3.4 server changes → Tasks 4–5. ✓
- §4 output parity (lead with MAC, drop CRC, size from response, no-triggers warning) → Task 7 parity rules + tests. ✓
- §5 error handling → Task 1 `do()` + Task 7 server-side validation for console/power-mode + client-side dur parse for set-max-offline. ✓
- §6 testing (apiclient unit, e2e real apisrv, EnsureNode-on-write, node_id echo, replace mutate_test cores) → Tasks 1–8. ✓
- §8 open details (timeouts, serverURL home, confirmation formats) → pinned in Tasks 1/6/7. ✓

**Placeholder scan:** none — every code step carries complete, runnable code; no "TBD"/"handle errors"/empty-test stubs. The `config_test.go` migration (Task 7 Step 4) names the exact five functions to delete and the exact set to keep, rather than gesturing at "fix references".

**Type consistency:** `Command(sel,verb,args) (int64,string,error)`, `Install(...) (int64,string,int64,error)`, `PatchNode(...) (string,error)`, `InstallOpts{Lifecycle,Runlevel,IntervalS,Triggers}` used identically in apiclient (Tasks 1–3), the mutate cores, and all tests (Tasks 7–8). Server `node_id`/`size`/`command_id` field names match the client's `cmdResp`/`installResp`/`patchResp` json tags.
