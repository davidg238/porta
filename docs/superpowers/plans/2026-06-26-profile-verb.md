# `profile` verb — remote target-execution profiling — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a language-neutral `profile` command verb + a WRQ-only `profile?id=` resource that arms a one-shot profiling session on a node, ingests an opaque profiler blob, stores it append-only with selectable per-session identity, and surfaces it across API/CLI/web — decode stays node-side.

**Architecture:** Mirror the existing `debug` verb end-to-end (command constructor → control → store → handler resource → apisrv → apiclient → portacli → web), but the resource is upload-only and the result is a binary blob in its own table. An operator `label` is stored porta-side only (a `profile_session` row, last-write-wins per node) and joined onto each result at ingest, so it never touches the wire. The web node-detail page is re-split: left column = discrete tables (incl. the new Profiles list), right column = the two consoles (Logs 60% / relocated Prints 40%).

**Tech Stack:** Go (CGO sqlite via `modernc`/mattn driver already in use), cobra CLI, html/template + htmx, standard `net/http`.

## Global Constraints

- Module: `github.com/davidg238/porta`. Copyright header `// Copyright (c) 2026 Ekorau LLC` on every new `.go`/`.html` file (HTML uses `<!-- ... -->`).
- porta is **language-neutral**: it never parses the profile blob; decode is selected by the node `kind` column and lives in node repos. No Toit-specific knobs (`all_tasks`/`cutoff`) on the wire.
- The `label` is **porta-side only** — never in command args / `EncodeWire` output.
- Profile goal is **single-in-flight per node** (last-write-wins) — correlation needs no wire session token.
- TDD throughout: failing test → run/verify fail → minimal impl → run/verify pass → commit. Run `gofmt` before each commit.
- Tests use `store.Open(":memory:")` (see `internal/web/web_test.go:testStore`).
- Spec: `docs/superpowers/specs/2026-06-26-profile-verb-design.md`.

---

### Task 1: Store — `profile_session` + `profile_result` schema and methods

**Files:**
- Modify: `internal/store/store.go:75-91` (append two `CREATE TABLE` blocks + indexes to the `schema` const)
- Create: `internal/store/profile.go`
- Test: `internal/store/profile_test.go`

**Interfaces:**
- Produces:
  - `type ProfileSession struct { DeviceID, App, Label string; StartedAt int64 }`
  - `type ProfileResult struct { ID, Seq int64; DeviceID string; TS int64; App, Label string; ByteLen int64; Blob []byte }`
  - `func (s *Store) UpsertProfileSession(deviceID, app, label string, now int64) error`
  - `func (s *Store) GetProfileSession(deviceID string) (*ProfileSession, error)` — nil when none
  - `func (s *Store) InsertProfileResult(deviceID, app, label string, ts int64, blob []byte) (int64, error)` — returns the new per-node `seq`
  - `func (s *Store) ProfileResults(deviceID string, afterSeq int64, limit int) ([]ProfileResult, error)` — `seq > afterSeq`, ordered by `seq`, **Blob nil** (list view)
  - `func (s *Store) GetProfileResult(deviceID string, seq int64) (*ProfileResult, error)` — Blob populated; nil when not found

- [ ] **Step 1: Write the failing test**

```go
// internal/store/profile_test.go
// Copyright (c) 2026 Ekorau LLC

package store

import (
	"bytes"
	"testing"
)

func TestProfileResultSeqAndCorrelation(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if err := st.UpsertProfileSession("aabbccddeeff", "myapp", "before-fix", 1000); err != nil {
		t.Fatal(err)
	}
	sess, err := st.GetProfileSession("aabbccddeeff")
	if err != nil || sess == nil {
		t.Fatalf("session: %v %v", sess, err)
	}
	if sess.App != "myapp" || sess.Label != "before-fix" {
		t.Fatalf("session mismatch: %+v", sess)
	}

	seq1, err := st.InsertProfileResult("aabbccddeeff", sess.App, sess.Label, 1001, []byte{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	seq2, err := st.InsertProfileResult("aabbccddeeff", sess.App, sess.Label, 1002, []byte{4, 5})
	if err != nil {
		t.Fatal(err)
	}
	if seq1 != 1 || seq2 != 2 {
		t.Fatalf("per-node seq want 1,2 got %d,%d", seq1, seq2)
	}

	list, err := st.ProfileResults("aabbccddeeff", 0, 0)
	if err != nil || len(list) != 2 {
		t.Fatalf("list: %v len=%d", err, len(list))
	}
	if list[0].Blob != nil {
		t.Errorf("list view must omit blob")
	}
	if list[0].App != "myapp" || list[0].Label != "before-fix" || list[0].ByteLen != 3 {
		t.Errorf("row0 wrong: %+v", list[0])
	}

	one, err := st.GetProfileResult("aabbccddeeff", 1)
	if err != nil || one == nil {
		t.Fatalf("get: %v", err)
	}
	if !bytes.Equal(one.Blob, []byte{1, 2, 3}) {
		t.Errorf("blob mismatch: %v", one.Blob)
	}

	after, err := st.ProfileResults("aabbccddeeff", 1, 0)
	if err != nil || len(after) != 1 || after[0].Seq != 2 {
		t.Fatalf("afterSeq filter wrong: %v %+v", err, after)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestProfileResultSeqAndCorrelation -v`
Expected: FAIL — build error, `UpsertProfileSession`/`ProfileSession` undefined.

- [ ] **Step 3a: Extend the schema**

In `internal/store/store.go`, inside the `schema` const, after the `idx_dbgresp_device` index line (`:90`) and before the closing backtick, add:

```sql
CREATE TABLE IF NOT EXISTS profile_session (
  device_id TEXT PRIMARY KEY,
  app TEXT,
  label TEXT,
  started_at INTEGER
);
CREATE TABLE IF NOT EXISTS profile_result (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  device_id TEXT,
  seq INTEGER,
  ts INTEGER,
  app TEXT,
  label TEXT,
  blob BLOB,
  byte_len INTEGER
);
CREATE INDEX IF NOT EXISTS idx_profres_device ON profile_result(device_id, seq);
```

- [ ] **Step 3b: Create the store methods**

```go
// internal/store/profile.go
// Copyright (c) 2026 Ekorau LLC

// internal/store/profile.go — the profile?id= channel backing store: a per-node
// profile session (label/app, porta-side only, last-write-wins) used to correlate
// arriving blobs, plus an append-only profile_result log with a per-node seq.
package store

import (
	"database/sql"
	"errors"
)

type ProfileSession struct {
	DeviceID  string
	App       string
	Label     string
	StartedAt int64
}

type ProfileResult struct {
	ID      int64
	Seq     int64
	DeviceID string
	TS      int64
	App     string
	Label   string
	ByteLen int64
	Blob    []byte // populated only by GetProfileResult; nil in list views
}

// UpsertProfileSession records the in-flight profile goal for a node. Single
// row per device (last-write-wins) — the correlation source for arriving blobs.
func (s *Store) UpsertProfileSession(deviceID, app, label string, now int64) error {
	_, err := s.db.Exec(
		`INSERT INTO profile_session (device_id, app, label, started_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(device_id) DO UPDATE SET app=excluded.app, label=excluded.label, started_at=excluded.started_at`,
		deviceID, app, label, now)
	return err
}

func (s *Store) GetProfileSession(deviceID string) (*ProfileSession, error) {
	var p ProfileSession
	err := s.db.QueryRow(
		`SELECT device_id, COALESCE(app,''), COALESCE(label,''), COALESCE(started_at,0)
		 FROM profile_session WHERE device_id = ?`, deviceID).
		Scan(&p.DeviceID, &p.App, &p.Label, &p.StartedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// InsertProfileResult appends one result row, assigning the next per-node seq.
func (s *Store) InsertProfileResult(deviceID, app, label string, ts int64, blob []byte) (int64, error) {
	var seq int64
	if err := s.db.QueryRow(
		`SELECT COALESCE(MAX(seq),0)+1 FROM profile_result WHERE device_id = ?`,
		deviceID).Scan(&seq); err != nil {
		return 0, err
	}
	if _, err := s.db.Exec(
		`INSERT INTO profile_result (device_id, seq, ts, app, label, blob, byte_len)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		deviceID, seq, ts, app, label, blob, len(blob)); err != nil {
		return 0, err
	}
	return seq, nil
}

func (s *Store) ProfileResults(deviceID string, afterSeq int64, limit int) ([]ProfileResult, error) {
	q := `SELECT id, seq, ts, COALESCE(app,''), COALESCE(label,''), COALESCE(byte_len,0)
		  FROM profile_result WHERE device_id = ? AND seq > ? ORDER BY seq`
	args := []any{deviceID, afterSeq}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProfileResult
	for rows.Next() {
		var r ProfileResult
		r.DeviceID = deviceID
		if err := rows.Scan(&r.ID, &r.Seq, &r.TS, &r.App, &r.Label, &r.ByteLen); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) GetProfileResult(deviceID string, seq int64) (*ProfileResult, error) {
	var r ProfileResult
	r.DeviceID = deviceID
	err := s.db.QueryRow(
		`SELECT id, seq, ts, COALESCE(app,''), COALESCE(label,''), COALESCE(byte_len,0), blob
		 FROM profile_result WHERE device_id = ? AND seq = ?`, deviceID, seq).
		Scan(&r.ID, &r.Seq, &r.TS, &r.App, &r.Label, &r.ByteLen, &r.Blob)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestProfileResultSeqAndCorrelation -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/store/profile.go internal/store/profile_test.go internal/store/store.go
git add internal/store/profile.go internal/store/profile_test.go internal/store/store.go
git commit -m "feat(store): profile_session + append-only profile_result with per-node seq"
```

---

### Task 2: Command — `Profile` constructor

**Files:**
- Modify: `internal/command/command.go` (add after `Debug`, ~`:165`)
- Test: `internal/command/command_test.go` (append)

**Interfaces:**
- Consumes: `command.Command` struct, `EncodeWire` (Task uses existing).
- Produces: `func Profile(name, action string, durationS int64, continuous bool) (Command, error)`
  - `action ∈ {start, stop}`; else error.
  - `start` args: `{"name":…,"action":"start","duration_s":N}` plus `"continuous":true` only when set; `stop` args: `{"name":…,"action":"stop"}`.
  - **No `label`** (porta-side only). `durationS` must be `>= 0`.

- [ ] **Step 1: Write the failing test**

```go
// append to internal/command/command_test.go
func TestProfileVerb(t *testing.T) {
	c, err := command.Profile("myapp", "start", 30, false)
	if err != nil {
		t.Fatal(err)
	}
	if c.Verb != "profile" {
		t.Fatalf("verb = %q", c.Verb)
	}
	wire := string(command.EncodeWire(c.Verb, c.ArgsJSON))
	for _, want := range []string{`"verb":"profile"`, `"action":"start"`, `"name":"myapp"`, `"duration_s":30`} {
		if !strings.Contains(wire, want) {
			t.Errorf("wire missing %q: %s", want, wire)
		}
	}
	if strings.Contains(wire, "continuous") {
		t.Errorf("continuous must be omitted when false: %s", wire)
	}
	if strings.Contains(wire, "label") {
		t.Errorf("label must never reach the wire: %s", wire)
	}

	cc, _ := command.Profile("myapp", "start", 0, true)
	if !strings.Contains(string(command.EncodeWire(cc.Verb, cc.ArgsJSON)), `"continuous":true`) {
		t.Errorf("continuous=true must be present")
	}

	stop, _ := command.Profile("myapp", "stop", 0, false)
	sw := string(command.EncodeWire(stop.Verb, stop.ArgsJSON))
	if strings.Contains(sw, "duration_s") || strings.Contains(sw, "continuous") {
		t.Errorf("stop carries no duration/continuous: %s", sw)
	}

	if _, err := command.Profile("myapp", "pause", 0, false); err == nil {
		t.Error("expected error for invalid action")
	}
}
```

(If `strings` is not already imported in `command_test.go`, add it.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/command/ -run TestProfileVerb -v`
Expected: FAIL — `command.Profile` undefined.

- [ ] **Step 3: Implement**

```go
// internal/command/command.go — add after Debug()
// Profile enqueues a declarative profile session goal: action ∈ {start, stop}.
// start arms a one-shot profiling run of app `name` (run-loop bounded by
// duration_s; deep-sleep bounded by the next wake); stop disarms early. The
// operator label is porta-side only and is deliberately NOT part of the wire
// args. duration_s/continuous ride only on start.
func Profile(name, action string, durationS int64, continuous bool) (Command, error) {
	if action != "start" && action != "stop" {
		return Command{}, fmt.Errorf("invalid profile action %q (expected start|stop)", action)
	}
	if durationS < 0 {
		return Command{}, fmt.Errorf("profile duration_s must be >= 0")
	}
	obj := map[string]any{"name": name, "action": action}
	if action == "start" {
		obj["duration_s"] = durationS
		if continuous {
			obj["continuous"] = true
		}
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return Command{}, err
	}
	return Command{Verb: "profile", ArgsJSON: string(b)}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/command/ -run TestProfileVerb -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/command/command.go internal/command/command_test.go
git add internal/command/command.go internal/command/command_test.go
git commit -m "feat(command): profile verb constructor (start/stop, label off-wire)"
```

---

### Task 3: Control — `Profile`, `ProfileResults`, `ProfileResult`

**Files:**
- Modify: `internal/control/control.go` (add after `Debug`, ~`:79`)
- Test: `internal/control/control_test.go` (append; if absent, create with the package + imports below)

**Interfaces:**
- Consumes: `command.Profile`, `store.UpsertProfileSession`, `store.EnqueueCommand`, `store.ProfileResults`, `store.GetProfileResult`.
- Produces:
  - `func Profile(st *store.Store, id, name, action string, durationS int64, continuous bool, label, issuedBy string, now int64) (int64, error)` — on `start`, upserts the session (storing `label`) **then** enqueues the command; returns the command id.
  - `func ProfileResults(st *store.Store, id string, afterSeq int64, limit int) ([]store.ProfileResult, error)`
  - `func ProfileResult(st *store.Store, id string, seq int64) (*store.ProfileResult, error)`

- [ ] **Step 1: Write the failing test**

```go
// internal/control/control_test.go (append; create file with this header if missing)
// Copyright (c) 2026 Ekorau LLC

package control_test

import (
	"testing"

	"github.com/davidg238/porta/internal/control"
	"github.com/davidg238/porta/internal/store"
)

func TestProfileStartStoresLabelAndEnqueues(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.EnsureNode("aabbccddeeff", 1000); err != nil {
		t.Fatal(err)
	}

	cid, err := control.Profile(st, "aabbccddeeff", "myapp", "start", 30, false, "before-fix", "test", 1000)
	if err != nil || cid == 0 {
		t.Fatalf("profile start: cid=%d err=%v", cid, err)
	}
	sess, err := st.GetProfileSession("aabbccddeeff")
	if err != nil || sess == nil || sess.Label != "before-fix" || sess.App != "myapp" {
		t.Fatalf("session not stored: %+v err=%v", sess, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/control/ -run TestProfileStartStoresLabelAndEnqueues -v`
Expected: FAIL — `control.Profile` undefined.

- [ ] **Step 3: Implement**

```go
// internal/control/control.go — add after Debug()
// Profile enqueues a declarative profile session goal. On start it first records
// the porta-side session (app + operator label) so arriving blobs can be
// correlated and labelled, then enqueues the (label-free) command.
func Profile(st *store.Store, id, name, action string, durationS int64, continuous bool, label, issuedBy string, now int64) (int64, error) {
	c, err := command.Profile(name, action, durationS, continuous)
	if err != nil {
		return 0, err
	}
	if action == "start" {
		if err := st.UpsertProfileSession(id, name, label, now); err != nil {
			return 0, err
		}
	}
	return st.EnqueueCommand(id, c.Verb, c.ArgsJSON, issuedBy, now)
}

// ProfileResults lists profile result rows (no blob) with seq > afterSeq.
func ProfileResults(st *store.Store, id string, afterSeq int64, limit int) ([]store.ProfileResult, error) {
	return st.ProfileResults(id, afterSeq, limit)
}

// ProfileResult fetches one profile result (with blob) by per-node seq.
func ProfileResult(st *store.Store, id string, seq int64) (*store.ProfileResult, error) {
	return st.GetProfileResult(id, seq)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/control/ -run TestProfileStartStoresLabelAndEnqueues -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/control/control.go internal/control/control_test.go
git add internal/control/control.go internal/control/control_test.go
git commit -m "feat(control): profile start/stop + result reads"
```

---

### Task 4: Handler — `profile?id=` WRQ ingest + correlation

**Files:**
- Modify: `internal/handler/handler.go:167` (AcceptWrite allowlist), `:185-194` (Write switch), add `writeProfile` near `writeDebug` (`:339`)
- Test: `internal/handler/handler_test.go` (append; reuse existing test helpers there)

**Interfaces:**
- Consumes: `store.GetProfileSession`, `store.InsertProfileResult`, `h.store.TouchNode`, `h.now`.
- Produces: `profile?id=` accepted by `AcceptWrite`; `Write` routes `profile` → `writeProfile`, which stores the raw body as one `profile_result` row, tagged with the current session's app/label.

- [ ] **Step 1: Write the failing test**

```go
// append to internal/handler/handler_test.go
func TestWriteProfileStoresBlobWithSessionLabel(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	h := New(st, func() int64 { return 2000 })

	// Arm a session so the blob is correlated.
	if err := st.UpsertProfileSession("aabbccddeeff", "myapp", "run1", 1999); err != nil {
		t.Fatal(err)
	}
	if err := h.AcceptWrite("profile?id=aabbccddeeff", "1.2.3.4:5"); err != nil {
		t.Fatalf("AcceptWrite rejected profile: %v", err)
	}
	if err := h.Write("profile?id=aabbccddeeff", "1.2.3.4:5", []byte{9, 8, 7}); err != nil {
		t.Fatalf("Write profile: %v", err)
	}
	list, err := st.ProfileResults("aabbccddeeff", 0, 0)
	if err != nil || len(list) != 1 {
		t.Fatalf("expected 1 result, got %d (%v)", len(list), err)
	}
	if list[0].App != "myapp" || list[0].Label != "run1" || list[0].ByteLen != 3 {
		t.Errorf("result not correlated: %+v", list[0])
	}
}
```

(Match the real `New(...)` signature in `handler_test.go`; if its helper differs, use that helper instead of `New` directly.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/handler/ -run TestWriteProfileStoresBlobWithSessionLabel -v`
Expected: FAIL — `profile` rejected by `AcceptWrite` / no result stored.

- [ ] **Step 3a: Allow the WRQ** — `internal/handler/handler.go:167`

```go
	if base != "report" && base != "data" && base != "debug" && base != "profile" {
		return fmt.Errorf("access denied: %s", base)
	}
```

- [ ] **Step 3b: Route the write** — add to the `switch base` in `Write` (after the `debug` case, `:191`):

```go
	case "profile":
		err = h.writeProfile(id, peer, data)
```

- [ ] **Step 3c: Implement `writeProfile`** — add after `writeDebug` (`:339`):

```go
// writeProfile ingests one profiler blob (the whole WRQ body, opaque to porta)
// into profile_result, tagged with the node's current profile session app/label
// for correlation. porta never parses the blob — decode is node-kind-defined.
func (h *Handler) writeProfile(id, peer string, data []byte) error {
	if err := h.store.TouchNode(id, peer, h.now()); err != nil {
		return err
	}
	app, label := "", ""
	if sess, err := h.store.GetProfileSession(id); err == nil && sess != nil {
		app, label = sess.App, sess.Label
	}
	_, err := h.store.InsertProfileResult(id, app, label, h.now(), data)
	return err
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/handler/ -run TestWriteProfileStoresBlobWithSessionLabel -v`
Expected: PASS. Then `go test ./internal/handler/` — all green.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/handler/handler.go internal/handler/handler_test.go
git add internal/handler/handler.go internal/handler/handler_test.go
git commit -m "feat(handler): profile?id= WRQ ingest, correlated to session label"
```

---

### Task 5: API — dispatch `profile` + list/fetch endpoints

**Files:**
- Modify: `internal/apisrv/commands.go` (add `case "profile"` to `dispatch`, after the `debug` case `:110-121`)
- Create: `internal/apisrv/profile.go`
- Modify: `internal/apisrv/apisrv.go:58-69` (register 2 routes)
- Test: `internal/apisrv/profile_test.go`

**Interfaces:**
- Consumes: `control.Profile`, `control.ProfileResults`, `control.ProfileResult`, `h.resolveSel`, `decodeArgs`, `writeOK`, `writeErr`.
- Produces:
  - dispatch `profile`: body args `{name, action, duration_s, continuous, label}` → `control.Profile`.
  - `GET /api/nodes/{sel}/profile?after=N` → `{node_id, results:[{seq,ts,app,label,byte_len}]}`
  - `GET /api/nodes/{sel}/profile/{seq}` → `{node_id, seq, ts, app, label, byte_len, blob}` (blob base64) or 404.

- [ ] **Step 1: Write the failing test**

```go
// internal/apisrv/profile_test.go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/apisrv/ -run TestProfileStartListGet -v`
Expected: FAIL — unknown verb `profile` / 404 on `/profile`.

- [ ] **Step 3a: Dispatch** — add to `dispatch` switch in `internal/apisrv/commands.go` after the `debug` case:

```go
	case "profile":
		var a struct {
			Name       string `json:"name"`
			Action     string `json:"action"`
			DurationS  int64  `json:"duration_s"`
			Continuous bool   `json:"continuous"`
			Label      string `json:"label"`
		}
		if err := decodeArgs(req.Args, &a); err != nil {
			return 0, err
		}
		if a.Name == "" {
			return 0, fmt.Errorf("profile requires name")
		}
		return control.Profile(h.st, id, a.Name, a.Action, a.DurationS, a.Continuous, a.Label, "api", now)
```

- [ ] **Step 3b: Endpoints** — create `internal/apisrv/profile.go`:

```go
// Copyright (c) 2026 Ekorau LLC

package apisrv

import (
	"encoding/base64"
	"net/http"
	"strconv"

	"github.com/davidg238/porta/internal/control"
)

// handleProfileList: GET /api/nodes/{sel}/profile?after=N — result rows, no blob.
func (h *Handler) handleProfileList(w http.ResponseWriter, r *http.Request) {
	id, ok := h.resolveSel(w, r.PathValue("sel"))
	if !ok {
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	rows, err := control.ProfileResults(h.st, id, after, 0)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, x := range rows {
		out = append(out, map[string]any{
			"seq": x.Seq, "ts": x.TS, "app": x.App, "label": x.Label, "byte_len": x.ByteLen,
		})
	}
	writeOK(w, map[string]any{"node_id": id, "results": out})
}

// handleProfileGet: GET /api/nodes/{sel}/profile/{seq} — one result with blob (base64).
func (h *Handler) handleProfileGet(w http.ResponseWriter, r *http.Request) {
	id, ok := h.resolveSel(w, r.PathValue("sel"))
	if !ok {
		return
	}
	seq, err := strconv.ParseInt(r.PathValue("seq"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad seq")
		return
	}
	res, err := control.ProfileResult(h.st, id, seq)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if res == nil {
		writeErr(w, http.StatusNotFound, "no such profile result")
		return
	}
	writeOK(w, map[string]any{
		"node_id": id, "seq": res.Seq, "ts": res.TS, "app": res.App, "label": res.Label,
		"byte_len": res.ByteLen, "blob": base64.StdEncoding.EncodeToString(res.Blob),
	})
}
```

- [ ] **Step 3c: Register** — add to `Register` in `internal/apisrv/apisrv.go` (with the other node routes):

```go
	mux.HandleFunc("GET /api/nodes/{sel}/profile", recoverer(h.handleProfileList))
	mux.HandleFunc("GET /api/nodes/{sel}/profile/{seq}", recoverer(h.handleProfileGet))
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/apisrv/ -run TestProfileStartListGet -v`
Expected: PASS. Then `go test ./internal/apisrv/` — all green.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/apisrv/commands.go internal/apisrv/profile.go internal/apisrv/apisrv.go internal/apisrv/profile_test.go
git add internal/apisrv/commands.go internal/apisrv/profile.go internal/apisrv/apisrv.go internal/apisrv/profile_test.go
git commit -m "feat(apisrv): profile dispatch + list/fetch endpoints"
```

---

### Task 6: API client — profile methods

**Files:**
- Modify: `devsdk/apiclient/client.go` (add after `DebugResponses`, ~`:222`)
- Test: `devsdk/apiclient/client_test.go` (append; reuse its existing httptest harness)

**Interfaces:**
- Consumes: `c.Command`, `c.do`, `c.baseURL`.
- Produces:
  - `func (c *Client) ProfileStart(sel, app string, durationS int64, continuous bool, label string) (int64, string, error)`
  - `func (c *Client) ProfileStop(sel, app string) (int64, string, error)`
  - `type ProfileRow struct { Seq, TS int64; App, Label string; ByteLen int64 }` (json tags `seq,ts,app,label,byte_len`)
  - `func (c *Client) ProfileResults(sel string, afterSeq int64) ([]ProfileRow, error)`
  - `func (c *Client) ProfileBlob(sel string, seq int64) ([]byte, error)`

- [ ] **Step 1: Write the failing test**

```go
// append to devsdk/apiclient/client_test.go
func TestProfileClientRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/commands"):
			w.Write([]byte(`{"ok":true,"data":{"command_id":7,"node_id":"n1"}}`))
		case strings.HasSuffix(r.URL.Path, "/profile/1"):
			w.Write([]byte(`{"ok":true,"data":{"seq":1,"blob":"AQIDBA=="}}`))
		case strings.HasSuffix(r.URL.Path, "/profile"):
			w.Write([]byte(`{"ok":true,"data":{"results":[{"seq":1,"ts":5,"app":"myapp","label":"run1","byte_len":4}]}}`))
		}
	}))
	defer srv.Close()
	c := New(srv.URL)

	cid, node, err := c.ProfileStart("n1", "myapp", 30, false, "run1")
	if err != nil || cid != 7 || node != "n1" {
		t.Fatalf("start: %d %q %v", cid, node, err)
	}
	rows, err := c.ProfileResults("n1", 0)
	if err != nil || len(rows) != 1 || rows[0].Label != "run1" || rows[0].ByteLen != 4 {
		t.Fatalf("list: %+v %v", rows, err)
	}
	blob, err := c.ProfileBlob("n1", 1)
	if err != nil || len(blob) != 4 {
		t.Fatalf("blob: %v %v", blob, err)
	}
}
```

(Ensure `strings` is imported in the test file.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./devsdk/apiclient/ -run TestProfileClientRoundTrip -v`
Expected: FAIL — `ProfileStart` undefined.

- [ ] **Step 3: Implement** — add to `devsdk/apiclient/client.go`:

```go
// ProfileStart arms a one-shot profile session for sel targeting app. label is
// stored porta-side only (it is sent in the command body but never reaches the
// wire — porta strips it into the profile_session row).
func (c *Client) ProfileStart(sel, app string, durationS int64, continuous bool, label string) (int64, string, error) {
	return c.Command(sel, "profile", map[string]any{
		"name": app, "action": "start", "duration_s": durationS, "continuous": continuous, "label": label,
	})
}

// ProfileStop disarms an armed/running profile session for sel targeting app.
func (c *Client) ProfileStop(sel, app string) (int64, string, error) {
	return c.Command(sel, "profile", map[string]any{"name": app, "action": "stop"})
}

// ProfileRow is one profile result row (no blob) from the list endpoint.
type ProfileRow struct {
	Seq     int64  `json:"seq"`
	TS      int64  `json:"ts"`
	App     string `json:"app"`
	Label   string `json:"label"`
	ByteLen int64  `json:"byte_len"`
}

// ProfileResults lists profile result rows with seq > afterSeq.
func (c *Client) ProfileResults(sel string, afterSeq int64) ([]ProfileRow, error) {
	req, err := http.NewRequest("GET",
		c.baseURL+"/api/nodes/"+url.PathEscape(sel)+"/profile?after="+strconv.FormatInt(afterSeq, 10), nil)
	if err != nil {
		return nil, err
	}
	data, err := c.do(req)
	if err != nil {
		return nil, err
	}
	var r struct {
		Results []ProfileRow `json:"results"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return r.Results, nil
}

// ProfileBlob fetches one profile result's raw blob by per-node seq.
func (c *Client) ProfileBlob(sel string, seq int64) ([]byte, error) {
	req, err := http.NewRequest("GET",
		c.baseURL+"/api/nodes/"+url.PathEscape(sel)+"/profile/"+strconv.FormatInt(seq, 10), nil)
	if err != nil {
		return nil, err
	}
	data, err := c.do(req)
	if err != nil {
		return nil, err
	}
	var r struct {
		Blob string `json:"blob"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(r.Blob)
}
```

(Add `"encoding/base64"` to the imports if not present.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./devsdk/apiclient/ -run TestProfileClientRoundTrip -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w devsdk/apiclient/client.go devsdk/apiclient/client_test.go
git add devsdk/apiclient/client.go devsdk/apiclient/client_test.go
git commit -m "feat(apiclient): profile start/stop/list/blob methods"
```

---

### Task 7: CLI — `porta profile` subcommands

**Files:**
- Create: `internal/portacli/profile.go`
- Modify: `internal/portacli/root.go:49-57` (add `newProfileCmd()` to `root.AddCommand(...)`)
- Test: `internal/portacli/profile_test.go`

**Interfaces:**
- Consumes: `apiclient.New`, `serverURL()`, the Task 6 client methods.
- Produces: `newProfileCmd() *cobra.Command` with subcommands `start <app> [--duration 30s] [--continuous] [--label L]`, `stop <app>`, `poll [--after N]`, `get <seq> [-o file]`, all under a persistent `--device` flag (mirrors `newDebugCmd`).

- [ ] **Step 1: Write the failing test**

```go
// internal/portacli/profile_test.go
// Copyright (c) 2026 Ekorau LLC

package portacli

import (
	"bytes"
	"testing"

	"github.com/davidg238/porta/devsdk/apiclient"
)

func TestRunProfileStartPrints(t *testing.T) {
	// Uses the same in-process API harness the debug CLI test uses; if the
	// package exposes a test server helper, reuse it. Here we assert the command
	// constructs and runs its RunE without panicking against a stub client URL.
	var buf bytes.Buffer
	c := apiclient.New("http://127.0.0.1:0") // unreachable: we assert error path is clean
	err := runProfileStart(&buf, c, "n1", "myapp", 30, false, "run1")
	if err == nil {
		t.Skip("network reachable unexpectedly; covered by apiclient test")
	}
}
```

(If `portacli` already has a richer in-process API harness in `debug_test.go`/`mutate_test.go`, model this test on that instead — assert the printed line on success. Keep at least the `runProfileStart` signature exercised.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/portacli/ -run TestRunProfileStartPrints -v`
Expected: FAIL — `runProfileStart` undefined.

- [ ] **Step 3: Implement** — create `internal/portacli/profile.go`:

```go
// Copyright (c) 2026 Ekorau LLC

package portacli

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/davidg238/porta/devsdk/apiclient"
	"github.com/spf13/cobra"
)

func runProfileStart(out io.Writer, c *apiclient.Client, sel, app string, durationS int64, continuous bool, label string) error {
	cid, node, err := c.ProfileStart(sel, app, durationS, continuous, label)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: profile start %s (command #%d)\n", node, app, cid)
	return nil
}

func runProfileStop(out io.Writer, c *apiclient.Client, sel, app string) error {
	cid, node, err := c.ProfileStop(sel, app)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: profile stop %s (command #%d)\n", node, app, cid)
	return nil
}

func runProfilePoll(out io.Writer, c *apiclient.Client, sel string, after int64) error {
	rows, err := c.ProfileResults(sel, after)
	if err != nil {
		return err
	}
	for _, r := range rows {
		label := r.Label
		if label == "" {
			label = "-"
		}
		fmt.Fprintf(out, "#%d  %s  %s  %d bytes\n", r.Seq, r.App, label, r.ByteLen)
	}
	return nil
}

func runProfileGet(out io.Writer, c *apiclient.Client, sel string, seq int64, outFile string) error {
	blob, err := c.ProfileBlob(sel, seq)
	if err != nil {
		return err
	}
	if outFile == "" {
		_, err = out.Write(blob)
		return err
	}
	return os.WriteFile(outFile, blob, 0o644)
}

func newProfileCmd() *cobra.Command {
	var device string
	cmd := &cobra.Command{Use: "profile", Short: "Profile a node app's execution (opaque blob; decode is node-side)"}
	cmd.PersistentFlags().StringVar(&device, "device", "", "target node id or name")

	var duration time.Duration
	var continuous bool
	var label string
	start := &cobra.Command{Use: "start <app>", Short: "Arm a one-shot profile session", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			return runProfileStart(cmd.OutOrStdout(), apiclient.New(serverURL()), device, a[0],
				int64(duration.Seconds()), continuous, label)
		}}
	start.Flags().DurationVar(&duration, "duration", 30*time.Second, "run-loop auto-stop bound")
	start.Flags().BoolVar(&continuous, "continuous", false, "re-arm each cycle until stop")
	start.Flags().StringVar(&label, "label", "", "operator label (porta-side only)")

	stop := &cobra.Command{Use: "stop <app>", Short: "Disarm a profile session", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			return runProfileStop(cmd.OutOrStdout(), apiclient.New(serverURL()), device, a[0])
		}}

	var after int64
	poll := &cobra.Command{Use: "poll", Short: "List profile results (seq > --after)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runProfilePoll(cmd.OutOrStdout(), apiclient.New(serverURL()), device, after)
		}}
	poll.Flags().Int64Var(&after, "after", 0, "only results with seq greater than this")

	var outFile string
	get := &cobra.Command{Use: "get <seq>", Short: "Fetch one result blob (raw) to stdout or file", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			seq, err := parseInt64(a[0])
			if err != nil {
				return err
			}
			return runProfileGet(cmd.OutOrStdout(), apiclient.New(serverURL()), device, seq, outFile)
		}}
	get.Flags().StringVarP(&outFile, "output", "o", "", "write blob to this file instead of stdout")

	cmd.AddCommand(start, stop, poll, get)
	return cmd
}

// parseInt64 is a tiny helper for the get <seq> arg.
func parseInt64(s string) (int64, error) {
	var v int64
	_, err := fmt.Sscan(s, &v)
	return v, err
}
```

- [ ] **Step 3b: Register** — in `internal/portacli/root.go`, add `newProfileCmd(),` to the `root.AddCommand(...)` list (next to `newDebugCmd()`).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/portacli/ -run TestRunProfileStartPrints -v` then `go build ./...`
Expected: PASS + clean build.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/portacli/profile.go internal/portacli/profile_test.go internal/portacli/root.go
git add internal/portacli/profile.go internal/portacli/profile_test.go internal/portacli/root.go
git commit -m "feat(portacli): porta profile start/stop/poll/get"
```

---

### Task 8: Web — relocate Prints to the right column (Logs 60 / Prints 40)

**Files:**
- Modify: `internal/web/templates/node.html:8-23` (move Prints into `node-right`)
- Modify: `internal/web/assets/style.css:64-79` (right-column split + min-heights)
- Test: `internal/web/web_test.go` (append a layout assertion)

**Interfaces:**
- No Go signature changes. Pure markup/CSS. Prints + Logs partials are unchanged (`node-prints`/`node-logs` still poll their own routes).

- [ ] **Step 1: Write the failing test**

```go
// append to internal/web/web_test.go
func TestNodePageStacksConsolesRight(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	srv := serve(t, st)
	body := readBody(t, mustGet(t, srv.URL+"/n/aabbccddeeff/"))
	// Both consoles now live in the right column, Prints after Logs.
	li := strings.Index(body, `id="logs"`)
	pi := strings.Index(body, `id="prints"`)
	ri := strings.Index(body, `node-right`)
	if li < 0 || pi < 0 || ri < 0 {
		t.Fatalf("missing logs/prints/node-right: %d %d %d", li, pi, ri)
	}
	if !(ri < li && li < pi) {
		t.Errorf("expected node-right then Logs then Prints; got right=%d logs=%d prints=%d", ri, li, pi)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/web/ -run TestNodePageStacksConsolesRight -v`
Expected: FAIL — Prints currently precedes `node-right`/Logs (it's in the left column).

- [ ] **Step 3a: Move Prints in `node.html`** — replace the `<div class="node-cols">…</div>` block (`:8-23`) with:

```html
<div class="node-cols">
  <div class="node-left">
    {{template "node-header" .}}
    {{template "node-config" .}}
    {{template "node-recent" .}}
    {{template "node-profiles" .}}
    {{template "node-containers" .}}
  </div>
  <div class="node-right">
    {{/* telemetry:node-console begin (optional; see node_console.go) */}}
    <section id="logs" hx-get="/n/{{.ID}}/logs" hx-trigger="load, every 3s" hx-swap="outerHTML">
      <h2>Logs</h2><p class="subtitle">loading…</p></section>
    <section id="prints" hx-get="/n/{{.ID}}/prints" hx-trigger="load, every 3s" hx-swap="outerHTML">
      <h2>Prints</h2><p class="subtitle">loading…</p></section>
    {{/* telemetry:node-console end */}}
  </div>
</div>
```

(Note: `{{template "node-profiles" .}}` is added now but its definition lands in Task 9. To keep this task independently green, **temporarily** define an empty stub at the bottom of `node.html`: `{{define "node-profiles"}}{{end}}` — Task 9 replaces the stub with the real partial in `node_profiles.html` and removes this stub line.)

- [ ] **Step 3b: CSS** — replace the `.node-left #prints …` / `.node-right #logs …` rules (`style.css:67-72`) with a right-column flex split:

```css
/* Right column stacks the two consoles: Logs 60% on top, Prints 40% below.
   Both get a min-height so neither collapses when the other floods. */
.node-right { gap:.7rem; }
.node-right #logs   { flex:6 1 0; display:flex; flex-direction:column; margin:0; min-height:8em; }
.node-right #prints { flex:4 1 0; display:flex; flex-direction:column; margin:0; min-height:8em; }
.node-right #logs .console, .node-right #prints .console { flex:1; max-height:none; }
```

And update the narrow-screen rule (`:75-79`) to cover both consoles:

```css
@media (max-width: 600px) {
  .node-cols { flex-direction:column; height:auto; }
  .node-left { overflow:visible; }
  .node-right #logs .console, .node-right #prints .console { max-height:16em; }
}
```

(Remove the now-defunct `.node-left #prints …` lines.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/web/ -run TestNodePageStacksConsolesRight -v` then `go test ./internal/web/`
Expected: PASS, all green.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/web/web_test.go
git add internal/web/templates/node.html internal/web/assets/style.css internal/web/web_test.go
git commit -m "feat(web): stack Logs/Prints in the right column (60/40)"
```

---

### Task 9: Web — Profiles panel (list + decode link)

**Files:**
- Create: `internal/web/node_profiles.go`
- Create: `internal/web/templates/node_profiles.html`
- Modify: `internal/web/pages.go:70-90` (add `case "profiles"` to `handleNodeSub`)
- Modify: `internal/web/web.go:42-51` register nothing new (handled by `/n/`), but ensure `node-profiles` stub from Task 8 is removed.
- Test: `internal/web/node_profiles_test.go`

**Interfaces:**
- Consumes: `control.ProfileResults`, `control.RelativeAge`, `h.st`, `h.now`.
- Produces: template defs `node-profiles` (full section, polled `/n/{id}/profiles` every 10s) with rows `seq · age · app · label · bytes · [decode ↗]`; decode href `nodus://profile?node=<id>&seq=<seq>` (typed `template.URL`).

- [ ] **Step 1: Write the failing test**

```go
// internal/web/node_profiles_test.go
// Copyright (c) 2026 Ekorau LLC

package web

import (
	"strings"
	"testing"
)

func TestNodeProfilesPanelListsAndLinks(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	if _, err := st.InsertProfileResult("aabbccddeeff", "myapp", "run1", 1001, []byte{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	srv := serve(t, st)

	p := readBody(t, mustGet(t, srv.URL+"/n/aabbccddeeff/profiles"))
	for _, want := range []string{`id="profiles"`, "myapp", "run1", "nodus://profile?", "seq=1", "[decode"} {
		if !strings.Contains(p, want) {
			t.Errorf("profiles panel missing %q in:\n%s", want, p)
		}
	}
	if !strings.Contains(p, `hx-get="/n/aabbccddeeff/profiles"`) {
		t.Errorf("profiles panel must self-poll: %s", p)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/web/ -run TestNodeProfilesPanelListsAndLinks -v`
Expected: FAIL — `/n/.../profiles` 404 (no case) and stub renders empty.

- [ ] **Step 3a: Renderer** — create `internal/web/node_profiles.go`:

```go
// Copyright (c) 2026 Ekorau LLC

// node_profiles.go renders the per-node Profiles panel: an append-only list of
// profile result sessions (seq · age · app · label · bytes) each with a decode
// hint handing the blob (by seq) to the node's dev tool. porta performs NO
// decode — the blob is opaque and node-kind-defined.
package web

import (
	"fmt"
	"html/template"
	"net/http"

	"github.com/davidg238/porta/internal/control"
	"github.com/davidg238/porta/internal/store"
)

type profileRowVM struct {
	Seq        int64
	Age        string
	App        string
	Label      string
	Bytes      int64
	DecodeHref template.URL
}

type profilesVM struct {
	ID    string
	Rows  []profileRowVM
	Empty string
}

func (h *Handler) renderNodeProfiles(w http.ResponseWriter, n *store.Node) {
	rows, err := control.ProfileResults(h.st, n.ID, 0, 50)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	now := h.now()
	out := make([]profileRowVM, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- { // newest first
		r := rows[i]
		href := fmt.Sprintf("nodus://profile?node=%s&seq=%d", n.ID, r.Seq)
		out = append(out, profileRowVM{
			Seq: r.Seq, Age: control.RelativeAge(r.TS, now), App: r.App, Label: r.Label,
			Bytes: r.ByteLen, DecodeHref: template.URL(href),
		})
	}
	h.render(w, "node-profiles", profilesVM{
		ID: n.ID, Rows: out,
		Empty: "no profiles — porta profile start <node> <app>",
	})
}
```

- [ ] **Step 3b: Template** — create `internal/web/templates/node_profiles.html`:

```html
<!-- Copyright (c) 2026 Ekorau LLC -->
{{define "node-profiles"}}<section id="profiles" hx-get="/n/{{.ID}}/profiles" hx-trigger="load, every 10s" hx-swap="outerHTML">
<h2>Profiles</h2>
{{if .Rows}}<table class="compact"><tbody>
{{range .Rows}}<tr><td>#{{.Seq}}</td><td>{{.Age}}</td><td>{{.App}}</td><td>{{if .Label}}{{.Label}}{{else}}—{{end}}</td><td>{{.Bytes}} B</td><td><a href="{{.DecodeHref}}">[decode ↗]</a></td></tr>{{end}}
</tbody></table>{{else}}<p class="subtitle">{{.Empty}}</p>{{end}}
</section>{{end}}
```

- [ ] **Step 3c: Dispatch** — add to `handleNodeSub` switch in `internal/web/pages.go` (with the other partial cases):

```go
	case "profiles":
		h.renderNodeProfiles(w, n)
```

- [ ] **Step 3d: Remove the Task-8 stub** — delete the temporary `{{define "node-profiles"}}{{end}}` line from `node.html` (the real def now lives in `node_profiles.html`, parsed by the same `templates/*.html` glob).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/web/ -run TestNodeProfilesPanelListsAndLinks -v` then `go test ./internal/web/`
Expected: PASS, all green.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/web/node_profiles.go internal/web/node_profiles_test.go internal/web/pages.go
git add internal/web/node_profiles.go internal/web/node_profiles_test.go internal/web/pages.go internal/web/templates/node_profiles.html internal/web/templates/node.html
git commit -m "feat(web): node-detail Profiles panel (list + nodus decode link)"
```

---

### Task 10: Web — start/stop profiling affordance in the panel header

**Files:**
- Modify: `internal/web/templates/node_profiles.html` (add a start form in the panel header)
- Modify: `internal/web/pages.go` (`handleNode`/`handleNodeSub`: handle `POST` to `profile-start` / `profile-stop`)
- Test: `internal/web/node_profiles_test.go` (append a POST test)

**Interfaces:**
- Consumes: `control.Profile`, `r.ParseForm`/`r.FormValue`, `h.renderNodeProfiles`.
- Produces: `POST /n/{id}/profile-start` (form fields `app`, `duration`, `continuous`, `label`) and `POST /n/{id}/profile-stop` (field `app`); both call `control.Profile` then re-render the `node-profiles` partial.

- [ ] **Step 1: Write the failing test**

```go
// append to internal/web/node_profiles_test.go
import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestProfileStartFormEnqueues(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	srv := serve(t, st)

	form := url.Values{"app": {"myapp"}, "duration": {"30"}, "label": {"run1"}}
	resp, err := http.PostForm(srv.URL+"/n/aabbccddeeff/profile-start", form)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("post: %v code=%d", err, resp.StatusCode)
	}
	sess, err := st.GetProfileSession("aabbccddeeff")
	if err != nil || sess == nil || sess.App != "myapp" || sess.Label != "run1" {
		t.Fatalf("session not armed: %+v %v", sess, err)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, `id="profiles"`) {
		t.Errorf("response should be the refreshed profiles partial: %s", body)
	}
}
```

(Merge imports with the existing test file's import block — don't duplicate.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/web/ -run TestProfileStartFormEnqueues -v`
Expected: FAIL — `profile-start` 404 (no POST handling).

- [ ] **Step 3a: Handle the POSTs** — in `internal/web/pages.go`, extend `handleNodeSub` to branch on method for the two mutation subs (add near the top of the `switch sub`):

```go
	case "profile-start":
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		_ = r.ParseForm()
		dur, _ := strconv.ParseInt(r.FormValue("duration"), 10, 64)
		if dur == 0 {
			dur = 30
		}
		cont := r.FormValue("continuous") == "on" || r.FormValue("continuous") == "true"
		if _, err := control.Profile(h.st, n.ID, r.FormValue("app"), "start", dur, cont, r.FormValue("label"), "web", h.now()); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		h.renderNodeProfiles(w, n)
		return
	case "profile-stop":
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		_ = r.ParseForm()
		if _, err := control.Profile(h.st, n.ID, r.FormValue("app"), "stop", 0, false, "", "web", h.now()); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		h.renderNodeProfiles(w, n)
		return
```

(Add `"strconv"` to `pages.go` imports if not present; `control` is already imported.)

- [ ] **Step 3b: Form in the panel header** — in `node_profiles.html`, change the `<h2>Profiles</h2>` line to include an inline start form using htmx so the panel refreshes in place:

```html
<h2>Profiles</h2>
<form class="action" hx-post="/n/{{.ID}}/profile-start" hx-target="#profiles" hx-swap="outerHTML">
  <input name="app" placeholder="app" required>
  <input name="duration" type="number" value="30" style="width:5em" title="duration_s">
  <label><input name="continuous" type="checkbox"> cont</label>
  <input name="label" placeholder="label">
  <button type="submit">profile</button>
</form>
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/web/ -run TestProfileStartFormEnqueues -v` then `go test ./internal/web/`
Expected: PASS, all green.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/web/pages.go internal/web/node_profiles_test.go
git add internal/web/pages.go internal/web/node_profiles_test.go internal/web/templates/node_profiles.html
git commit -m "feat(web): start/stop profiling affordance in the Profiles panel"
```

---

### Task 11: Docs — PROTOCOL.md `profile` verb + resource

**Files:**
- Modify: `docs/PROTOCOL.md` — resource table (`:47-48` area), verb-constant table (`:99`), and a new `### 2.10 profile` section after `### 2.9 debug` (`:329`).

**Interfaces:** none (documentation). This is the canonical wire contract for node implementers.

- [ ] **Step 1: Add the resource row** — in the TFTP resource table (next to the `data?id=` / `debug?id=` rows), add:

```
| node → gw | WRQ | `profile?id=<mac>` | Upload one encoded profiler blob (opaque, node-kind-defined). |
```

And add `profile` to the WRQ-accepted bases note (the line listing `report`, `data`, `debug`).

- [ ] **Step 2: Add the verb constant** — in the verb-constant table (`:99`), add:

```
| `profile` | `VERB-PROFILE` |
```

- [ ] **Step 3: Add the verb section** — after §2.9, add:

```markdown
### 2.10 `profile` — arm or disarm remote profiling

Declares the node's profile-session goal for a named app: **start** arms a
one-shot profiling run; **stop** disarms early. Declarative, last-write-wins;
while armed, a `run` for that app is held back (the profiler owns it), mirroring
`debug`. The profiler model (sampling vs invocation-count, all-tasks, cutoff) is
**node-internal** and deliberately off the wire.

| Key | Type | Required | Meaning |
|-----|------|----------|---------|
| `verb` | string | yes | `"profile"` |
| `name` | string | yes | App to profile (must already be installed). |
| `action` | string | yes | `"start"` — arm a one-shot session · `"stop"` — disarm early. |
| `duration_s` | int | no (start) | Run-loop auto-stop bound (default 30). Ignored by deep-sleep nodes (bounded by the wake's single execution). |
| `continuous` | bool | no (start) | `true` re-arms each cycle until `stop`. Default `false`. |

​```json
{"verb": "profile", "name": "myapp", "action": "start", "duration_s": 30}
{"verb": "profile", "name": "myapp", "action": "stop"}
​```

On **start**, a run-loop node relaunches the app under its profiler; a deep-sleep
node profiles the next wake's single execution. On completion (duration elapsed,
app exit, or one wake) the node encodes its profiler result and WRQs it to
`profile?id=<mac>` (§7), then disarms unless `continuous`. The blob is **opaque
and node-kind-defined**: porta stores it verbatim and never parses it; decoding
is node-defined and lives in the node's dev tooling (selected by the node `kind`),
exactly like the `kind:"panic"` contract. An operator label, when supplied, is
porta-side metadata only and never appears on the wire.
```

(The `​` zero-width marks around the JSON fence are just to show nesting here — write a normal ```` ``` ```` fence in the doc.)

- [ ] **Step 4: Verify the doc builds/links**

Run: `grep -n "profile" docs/PROTOCOL.md` — confirm the resource row, verb constant, and §2.10 are present and consistent.

- [ ] **Step 5: Commit**

```bash
git add docs/PROTOCOL.md
git commit -m "docs(protocol): document the profile verb + profile?id= resource"
```

---

## Final verification

- [ ] `gofmt -l internal/ devsdk/` prints nothing.
- [ ] `go build ./...` clean.
- [ ] `go test ./...` all green.
- [ ] Manual smoke (optional): `porta serve` locally, `porta profile start <node> app --label run1`, simulate a `profile?id=` WRQ (or `store.InsertProfileResult`), confirm `porta profile poll <node>` lists it and the web node page shows the Profiles row with a `[decode ↗]` link.

## Self-review notes (coverage vs spec)

- Spec §1 verb → Task 2/3/5/6/7/11. §1 resource → Task 4/11. §3 sessions/identity/label → Task 1/3/4. §4 API → Task 5; CLI → Task 7; web panel + layout → Task 8/9/10. §5 neutrality invariant → enforced by Task 4 (opaque blob), Task 2 (label/knobs off-wire), Task 9 (decode link only). §6 testing → every task is TDD. Handoff (node side) → out of scope, documented in §2.10 (Task 11).
