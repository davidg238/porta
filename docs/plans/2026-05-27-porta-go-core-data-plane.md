# porta Go core data plane (B1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bring the Go server to the canonical porta wire protocol (`docs/PROTOCOL.md`) so a live nodus node installs an image, runs it, and reports observed apps end-to-end against `cmd/porta` — while the existing Smalltalk/berry server is parked intact at `cmd/st-devserver`.

**Architecture:** Two binaries in one Go module (`github.com/davidg238/porta`). The Smalltalk machinery (today's `cmd/porta` + its packages) is *moved* under `cmd/st-devserver` + `internal/st/…` and must keep building and passing its current tests — it is not developed further. A fresh porta control plane is built from new packages: `internal/store` (porta sqlite schema + deterministic naming), `internal/command` (verb codec, CRC32-IEEE, duration parsing), `internal/handler` (TFTP resource dispatch implementing a new shared `tftp.Dispatcher` interface), and `internal/portacli` (cobra subcommand tree). The generic TFTP packet codec + transfer state machine (`internal/tftp`) stays shared; it gains a `Dispatcher` interface (read returns `([]byte, error)`, plus an RRQ transfer-complete callback) that the porta handler implements, while the legacy `RegisterGet`/`RegisterPut` path is left untouched for the parked ST binary.

**Tech Stack:** Go 1.26, `github.com/mattn/go-sqlite3` (CGO; the glibc-2.36/gw deployment question is deferred to a later slice), `github.com/spf13/cobra` (subcommand tree — replaces the ST binary's stdlib `flag` parsing), Go stdlib `hash/crc32` (IEEE), `encoding/json` with `Decoder.UseNumber()` and `json.RawMessage` for scalar-type fidelity.

---

## Key design decisions locked here (spec deferred these to plan-level)

1. **Shared transport gains a `Dispatcher` interface.** `internal/tftp` keeps its packet codec (`packet.go`) and transfer state machine, and adds:
   ```go
   type Dispatcher interface {
       Read(resource, peer string) ([]byte, error)        // RRQ: err → TFTP ERROR; (nil,nil) → empty body
       AcceptWrite(resource, peer string) error            // WRQ request gate; err → TFTP ERROR, no transfer
       Write(resource, peer string, data []byte) error     // WRQ completion ingest; err → TFTP ERROR
       Complete(op uint16, resource, peer string, ok bool) // transfer-complete (RRQ mark-delivered hook)
   }
   ```
   A `Server` with a dispatcher set routes the **full resource string** (incl. `?id=…&crc=…`) to it and keys transfer state by that string (so concurrent device polls don't collide — no path rewriting). A `Server` with no dispatcher uses the legacy `RegisterGet`/`RegisterPut` path unchanged, so the parked ST binary keeps working.
2. **Driver = `mattn/go-sqlite3` (CGO), unchanged.** Already a dep; host tests run on the dev box. gw deployment / CGO-glibc is explicitly out of B1 (spec §7/§8).
3. **CLI = `cobra`.** Idiomatic Go subcommand tree matching the Toit gateway's verbs.
4. **Scalar type fidelity** is preserved two ways: command **args are stored as exact JSON bytes** and the wire command is built by splicing `"verb"` into the stored args object via `map[string]json.RawMessage` (numbers are never round-tripped through `float64`); the `Decode` path (used by display/tests) uses `json.Decoder.UseNumber()`.
5. **`peer` (UDP source addr) threads through the transport** via a new `Server.HandlePacketFrom(pkt, peer)`; the handler uses it to record `source_addr` on touch. Legacy `HandlePacket(pkt)` (peer="") stays for ST.

---

## File structure

**Parked ST (Task 1 moves these; not developed further):**
- `cmd/st-devserver/main.go` ← from `cmd/porta/main.go`
- `internal/st/store/` ← from `internal/store/`
- `internal/st/cli/`, `internal/st/debug/`, `internal/st/debugui/`, `internal/st/mcpserver/`, `internal/st/gateway/`, `internal/st/helpers/` ← from the same names under `internal/`
- `internal/st/gateway/command.go` ← new home for `Command` + `CommandToJSON` (moved out of `internal/tftp`)

**Shared transport:**
- `internal/tftp/packet.go` — unchanged (generic codec)
- `internal/tftp/server.go` — modified: drop dead `Command`/`CommandToJSON`/`QueueCommand`/`PopCommand`/`commands`; add `Dispatcher`, `SetDispatcher`, `HandlePacketFrom`, dispatcher-mode RRQ/WRQ/ACK/DATA paths

**New porta control plane:**
- `internal/store/store.go` — porta sqlite schema + node/payload/command/report CRUD
- `internal/store/names.go` — deterministic `NodeNameFor` (byte-faithful port of `names.toit`)
- `internal/command/command.go` — verb codec, `Run`/`Stop`/`SetPollInterval` constructors, trigger map
- `internal/command/crc32.go` — `CRC32` (IEEE) thin wrapper + check vector
- `internal/command/duration.go` — `ParseDurationSeconds` (jag-style `30s`/`5m`/`2h`/`1d`/bare int)
- `internal/handler/handler.go` — porta `tftp.Dispatcher` impl
- `internal/portacli/root.go`, `serve.go`, `inspect.go`, `mutate.go`, `resolve.go` — cobra tree
- `cmd/porta/main.go` — new porta entrypoint (delegates to `portacli.Execute`)

---

## Task 1: Park the Smalltalk server (restructure, keep green)

Pure refactor — no behavior change. Success = the existing build and test suite stay green after the move.

**Files:**
- Move: `cmd/porta/` → `cmd/st-devserver/`
- Move: `internal/store/` → `internal/st/store/`; `internal/cli/` → `internal/st/cli/`; `internal/debug/` → `internal/st/debug/`; `internal/debugui/` → `internal/st/debugui/`; `internal/mcpserver/` → `internal/st/mcpserver/`; `internal/gateway/` → `internal/st/gateway/`; `internal/helpers/` → `internal/st/helpers/`
- Create: `internal/st/gateway/command.go`
- Modify: `internal/tftp/server.go`, `internal/tftp/server_test.go`

- [ ] **Step 1: Move the ST packages with git, preserving history**

```bash
cd /home/david/workspaceToit/porta
git mv cmd/porta cmd/st-devserver
mkdir -p internal/st
for p in store cli debug debugui mcpserver gateway helpers; do git mv "internal/$p" "internal/st/$p"; done
```

- [ ] **Step 2: Rewrite import paths `internal/<pkg>` → `internal/st/<pkg>`**

Rewrite every Go import of the moved packages. Run from repo root:

```bash
grep -rl 'davidg238/porta/internal/\(store\|cli\|debug\|debugui\|mcpserver\|gateway\|helpers\)' --include='*.go' . \
  | xargs sed -i -E 's#(davidg238/porta/internal/)(store|cli|debug|debugui|mcpserver|gateway|helpers)#\1st/\2#g'
```

Note: `internal/tftp` is NOT in that list — it stays put. The `debug`/`debugui`/`mcpserver` substrings only ever appear as full package path segments here, so the regex is safe.

- [ ] **Step 3: Move `Command` + `CommandToJSON` out of shared `tftp` into the ST gateway**

Delete from `internal/tftp/server.go` the `Command` struct (lines ~16-20), the `CommandToJSON` func (lines ~252-261), the `commands` field on `Server` (line ~46), its init in `NewServer` (line ~56), and the now-dead `QueueCommand`/`PopCommand` methods (lines ~76-98). Also remove the now-unused `encoding/hex` and `encoding/json` imports.

Create `internal/st/gateway/command.go`:

```go
package gateway

import (
	"encoding/hex"
	"encoding/json"
)

// Command represents a queued command for a device (Smalltalk wire format).
type Command struct {
	Verb    string
	Payload []byte
}

// CommandToJSON encodes a Command as JSON matching the firmware format:
// {"verb":"...", "payload":"hex..."}
func CommandToJSON(cmd *Command) []byte {
	m := map[string]string{
		"verb":    cmd.Verb,
		"payload": hex.EncodeToString(cmd.Payload),
	}
	b, _ := json.Marshal(m)
	return b
}
```

In `internal/st/gateway/gateway.go`, change the `commandsHandler` closure (the `tftp.Command{...}` / `tftp.CommandToJSON` block, lines ~147-152) to use the local types:

```go
		// Convert store.Command to the local Command for JSON encoding.
		tCmd := &Command{
			Verb:    cmd.Verb,
			Payload: cmd.Payload,
		}
		return CommandToJSON(tCmd)
```

- [ ] **Step 4: Remove the dead-code tests for the moved/deleted methods**

In `internal/tftp/server_test.go`, delete any test referencing `Command`, `CommandToJSON`, `QueueCommand`, or `PopCommand` (these exercised the in-memory queue that production never used). Keep the RRQ/WRQ/ACK/DATA transfer-machine tests.

```bash
grep -n 'Command\|QueueCommand\|PopCommand\|CommandToJSON' internal/tftp/server_test.go
```

- [ ] **Step 5: Build and test — must be green**

Run: `go build ./... && go test ./...`
Expected: PASS. All packages build under their new paths; `cmd/st-devserver` builds; every previously-green test still passes.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "refactor(porta): park Smalltalk server → cmd/st-devserver + internal/st/*

Move the ST/berry server and its packages aside, keeping them buildable
and test-green, to free internal/store + cmd/porta for the porta core.
Drop the dead in-memory command queue from the shared tftp.Server; move
Command/CommandToJSON to the ST gateway side.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: porta store — schema + node/payload/command/report CRUD

**Files:**
- Create: `internal/store/store.go`
- Test: `internal/store/store_test.go`

- [ ] **Step 1: Write the failing test**

`internal/store/store_test.go`:

```go
package store

import (
	"testing"
)

func openTmp(t *testing.T) *Store {
	t.Helper()
	st, err := Open(t.TempDir() + "/porta.db")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestTouchNodeCreatesThenUpdates(t *testing.T) {
	st := openTmp(t)
	if err := st.TouchNode("aabbccddeeff", "192.0.2.1:5000", 1000); err != nil {
		t.Fatal(err)
	}
	n, err := st.GetNode("aabbccddeeff")
	if err != nil || n == nil {
		t.Fatalf("GetNode: %v %v", n, err)
	}
	if n.Name == "" {
		t.Error("expected auto-assigned name")
	}
	if !n.LastSeen.Valid || n.LastSeen.Int64 != 1000 {
		t.Errorf("last_seen = %v, want 1000", n.LastSeen)
	}
	if n.Kind != "toit" {
		t.Errorf("kind = %q, want toit", n.Kind)
	}
	if n.PollIntervalS != 30 || n.MaxOfflineS != 300 {
		t.Errorf("defaults wrong: poll=%d max=%d", n.PollIntervalS, n.MaxOfflineS)
	}
	// Second contact with empty addr keeps the old addr (COALESCE), bumps last_seen.
	if err := st.TouchNode("aabbccddeeff", "", 2000); err != nil {
		t.Fatal(err)
	}
	n, _ = st.GetNode("aabbccddeeff")
	if n.LastSeen.Int64 != 2000 {
		t.Errorf("last_seen = %d, want 2000", n.LastSeen.Int64)
	}
	if n.SourceAddr != "192.0.2.1:5000" {
		t.Errorf("source_addr = %q, want preserved", n.SourceAddr)
	}
}

func TestEnsureNodeNoLastSeen(t *testing.T) {
	st := openTmp(t)
	if err := st.EnsureNode("001122334455", 500); err != nil {
		t.Fatal(err)
	}
	n, _ := st.GetNode("001122334455")
	if n == nil {
		t.Fatal("node not created")
	}
	if n.LastSeen.Valid {
		t.Error("ensure must not set last_seen")
	}
	// EnsureNode on an existing (touched) node must not clobber last_seen.
	st.TouchNode("001122334455", "x", 600)
	st.EnsureNode("001122334455", 700)
	n, _ = st.GetNode("001122334455")
	if !n.LastSeen.Valid || n.LastSeen.Int64 != 600 {
		t.Errorf("ensure clobbered last_seen: %v", n.LastSeen)
	}
}

func TestPayloadRegisterFetch(t *testing.T) {
	st := openTmp(t)
	img := []byte{1, 2, 3, 4, 5}
	if err := st.RegisterPayload(12345, "blink", img); err != nil {
		t.Fatal(err)
	}
	ok, _ := st.PayloadExists(12345)
	if !ok {
		t.Fatal("payload should exist")
	}
	got, err := st.Payload(12345)
	if err != nil || string(got) != string(img) {
		t.Errorf("Payload = %v %v", got, err)
	}
	missing, _ := st.Payload(99999)
	if missing != nil {
		t.Error("missing crc should return nil")
	}
	// INSERT OR REPLACE keyed by crc.
	st.RegisterPayload(12345, "blink2", []byte{9})
	got, _ = st.Payload(12345)
	if string(got) != string([]byte{9}) {
		t.Error("re-register should replace")
	}
}

func TestCommandQueueFIFOAndDeliver(t *testing.T) {
	st := openTmp(t)
	id1, err := st.EnqueueCommand("dev", "run", `{"name":"a"}`, "cli", 100)
	if err != nil {
		t.Fatal(err)
	}
	st.EnqueueCommand("dev", "stop", `{"name":"a"}`, "cli", 101)

	next, _ := st.NextUndelivered("dev")
	if next == nil || next.ID != id1 || next.Verb != "run" {
		t.Fatalf("FIFO wrong: %+v", next)
	}
	if err := st.MarkDelivered(next.ID, 200); err != nil {
		t.Fatal(err)
	}
	next, _ = st.NextUndelivered("dev")
	if next == nil || next.Verb != "stop" {
		t.Fatalf("after deliver, next should be stop: %+v", next)
	}
	un, _ := st.UndeliveredCommands("dev")
	if len(un) != 1 {
		t.Errorf("undelivered = %d, want 1", len(un))
	}
	log, _ := st.CommandLog("dev")
	if len(log) != 2 {
		t.Errorf("log = %d, want 2", len(log))
	}
	if !log[0].DeliveredAt.Valid || log[1].DeliveredAt.Valid {
		t.Error("delivery flags wrong in log")
	}
}

func TestInsertReportCachesObservedState(t *testing.T) {
	st := openTmp(t)
	st.TouchNode("dev", "x", 10)
	obs := `{"apps":{"blink":{"crc":7}},"config":{}}`
	if err := st.InsertReport("dev", obs, `{"uptime":42}`, 300); err != nil {
		t.Fatal(err)
	}
	n, _ := st.GetNode("dev")
	if n.ObservedState != obs {
		t.Errorf("observed_state = %q, want cached", n.ObservedState)
	}
	if !n.LastReportAt.Valid || n.LastReportAt.Int64 != 300 {
		t.Errorf("last_report_at = %v, want 300", n.LastReportAt)
	}
}

func TestNodeOnline(t *testing.T) {
	st := openTmp(t)
	st.TouchNode("dev", "x", 1000)
	n, _ := st.GetNode("dev")
	if !n.Online(1000 + 299) {
		t.Error("within max_offline should be online")
	}
	if n.Online(1000 + 301) {
		t.Error("past max_offline should be offline")
	}
	en := &Node{} // never-seen
	if en.Online(123456) {
		t.Error("never-seen must be offline")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run Test -v`
Expected: FAIL — `undefined: Open` / package has no non-test files.

- [ ] **Step 3: Write the implementation**

`internal/store/store.go`:

```go
// Package store is the porta gateway's sqlite data layer: node inventory,
// payload blobs, the command queue, and the append-only report log.
package store

import (
	"database/sql"

	_ "github.com/mattn/go-sqlite3"
)

const (
	DefaultPollIntervalS = 30
	DefaultMaxOfflineS   = 300
)

const schema = `
CREATE TABLE IF NOT EXISTS nodes (
  id TEXT PRIMARY KEY,
  name TEXT,
  source_addr TEXT,
  kind TEXT NOT NULL DEFAULT 'toit',
  first_seen INTEGER,
  last_seen INTEGER,
  poll_interval_s INTEGER DEFAULT 30,
  max_offline_s INTEGER DEFAULT 300,
  last_report_at INTEGER,
  observed_state TEXT
);
CREATE TABLE IF NOT EXISTS payloads (
  crc INTEGER PRIMARY KEY,
  name TEXT,
  size INTEGER,
  image BLOB
);
CREATE TABLE IF NOT EXISTS command_queue (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  device_id TEXT,
  verb TEXT,
  args TEXT,
  issued_at INTEGER,
  issued_by TEXT,
  delivered_at INTEGER
);
CREATE TABLE IF NOT EXISTS reports (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  device_id TEXT,
  ts INTEGER,
  observed_state TEXT,
  health TEXT
);
CREATE TABLE IF NOT EXISTS data_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  device_id TEXT,
  ts INTEGER,
  seq INTEGER,
  kind TEXT,
  name TEXT,
  value NUMERIC,
  text TEXT,
  value_type TEXT
);
CREATE INDEX IF NOT EXISTS idx_data_device_ts ON data_log(device_id, ts);
`

// Store wraps the sqlite database.
type Store struct {
	db *sql.DB
}

// Node is a row from the nodes table.
type Node struct {
	ID            string
	Name          string
	SourceAddr    string
	Kind          string
	FirstSeen     sql.NullInt64
	LastSeen      sql.NullInt64
	PollIntervalS int64
	MaxOfflineS   int64
	LastReportAt  sql.NullInt64
	ObservedState string
}

// Online reports whether the node has been seen within its max_offline window.
func (n *Node) Online(now int64) bool {
	return n.LastSeen.Valid && (now-n.LastSeen.Int64) <= n.MaxOfflineS
}

// Command is a row from the command_queue table.
type Command struct {
	ID          int64
	Verb        string
	Args        string // JSON object of flattened args
	IssuedAt    int64
	IssuedBy    string
	DeliveredAt sql.NullInt64
}

// Open opens (creating if needed) the sqlite database and applies the schema.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// nullStr returns a driver value that is NULL when v is empty.
func nullStr(v string) interface{} {
	if v == "" {
		return nil
	}
	return v
}

// TouchNode records contact: creates the node on first sight (with an
// auto-assigned name), otherwise bumps last_seen and refreshes source_addr.
// An empty source_addr is COALESCEd so it never clobbers a known address.
func (s *Store) TouchNode(id, sourceAddr string, now int64) error {
	_, err := s.db.Exec(`
		INSERT INTO nodes (id, name, source_addr, first_seen, last_seen, poll_interval_s, max_offline_s)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
		  last_seen = excluded.last_seen,
		  source_addr = COALESCE(excluded.source_addr, nodes.source_addr)`,
		id, NodeNameFor(id), nullStr(sourceAddr), now, now,
		DefaultPollIntervalS, DefaultMaxOfflineS)
	return err
}

// EnsureNode guarantees a row exists without recording contact (no last_seen).
// Used to address a node by MAC before its first poll.
func (s *Store) EnsureNode(id string, now int64) error {
	_, err := s.db.Exec(`
		INSERT INTO nodes (id, name, first_seen, poll_interval_s, max_offline_s)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`,
		id, NodeNameFor(id), now, DefaultPollIntervalS, DefaultMaxOfflineS)
	return err
}

const nodeCols = `id, name, COALESCE(source_addr,''), kind, first_seen, last_seen,
	COALESCE(poll_interval_s,30), COALESCE(max_offline_s,300), last_report_at,
	COALESCE(observed_state,'')`

func scanNode(row interface{ Scan(...interface{}) error }) (*Node, error) {
	var n Node
	err := row.Scan(&n.ID, &n.Name, &n.SourceAddr, &n.Kind, &n.FirstSeen,
		&n.LastSeen, &n.PollIntervalS, &n.MaxOfflineS, &n.LastReportAt, &n.ObservedState)
	if err != nil {
		return nil, err
	}
	return &n, nil
}

// GetNode returns the node row or (nil, nil) if absent.
func (s *Store) GetNode(id string) (*Node, error) {
	n, err := scanNode(s.db.QueryRow(`SELECT `+nodeCols+` FROM nodes WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return n, err
}

// NodeByName returns the node with the given friendly name, or (nil, nil).
func (s *Store) NodeByName(name string) (*Node, error) {
	n, err := scanNode(s.db.QueryRow(`SELECT `+nodeCols+` FROM nodes WHERE name = ?`, name))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return n, err
}

// ListNodes returns all nodes ordered by id.
func (s *Store) ListNodes() ([]Node, error) {
	rows, err := s.db.Query(`SELECT ` + nodeCols + ` FROM nodes ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	return out, rows.Err()
}

func (s *Store) SetNodeName(id, name string) error {
	_, err := s.db.Exec(`UPDATE nodes SET name = ? WHERE id = ?`, name, id)
	return err
}

func (s *Store) SetMaxOffline(id string, secs int64) error {
	_, err := s.db.Exec(`UPDATE nodes SET max_offline_s = ? WHERE id = ?`, secs, id)
	return err
}

func (s *Store) SetPollInterval(id string, secs int64) error {
	_, err := s.db.Exec(`UPDATE nodes SET poll_interval_s = ? WHERE id = ?`, secs, id)
	return err
}

func (s *Store) RegisterPayload(crc int64, name string, image []byte) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO payloads (crc, name, size, image) VALUES (?, ?, ?, ?)`,
		crc, name, len(image), image)
	return err
}

func (s *Store) PayloadExists(crc int64) (bool, error) {
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM payloads WHERE crc = ?`, crc).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

// Payload returns the raw image bytes for crc, or (nil, nil) if absent.
func (s *Store) Payload(crc int64) ([]byte, error) {
	var img []byte
	err := s.db.QueryRow(`SELECT image FROM payloads WHERE crc = ?`, crc).Scan(&img)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return img, err
}

// EnqueueCommand appends a command and returns its new id.
func (s *Store) EnqueueCommand(deviceID, verb, argsJSON, issuedBy string, now int64) (int64, error) {
	res, err := s.db.Exec(`
		INSERT INTO command_queue (device_id, verb, args, issued_at, issued_by, delivered_at)
		VALUES (?, ?, ?, ?, ?, NULL)`,
		deviceID, verb, argsJSON, now, issuedBy)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func scanCommand(row interface{ Scan(...interface{}) error }) (*Command, error) {
	var c Command
	err := row.Scan(&c.ID, &c.Verb, &c.Args, &c.IssuedAt, &c.IssuedBy, &c.DeliveredAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

const cmdCols = `id, verb, COALESCE(args,''), issued_at, COALESCE(issued_by,''), delivered_at`

// NextUndelivered returns the oldest undelivered command, or (nil, nil).
func (s *Store) NextUndelivered(deviceID string) (*Command, error) {
	c, err := scanCommand(s.db.QueryRow(`SELECT `+cmdCols+`
		FROM command_queue WHERE device_id = ? AND delivered_at IS NULL
		ORDER BY id LIMIT 1`, deviceID))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return c, err
}

func (s *Store) queryCommands(where, deviceID string) ([]Command, error) {
	rows, err := s.db.Query(`SELECT `+cmdCols+` FROM command_queue WHERE device_id = ? `+where+` ORDER BY id`, deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Command
	for rows.Next() {
		c, err := scanCommand(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func (s *Store) UndeliveredCommands(deviceID string) ([]Command, error) {
	return s.queryCommands("AND delivered_at IS NULL", deviceID)
}

func (s *Store) CommandLog(deviceID string) ([]Command, error) {
	return s.queryCommands("", deviceID)
}

func (s *Store) MarkDelivered(id, now int64) error {
	_, err := s.db.Exec(`UPDATE command_queue SET delivered_at = ? WHERE id = ?`, now, id)
	return err
}

// InsertReport appends to the report log and refreshes the node's cached
// observed_state + last_report_at.
func (s *Store) InsertReport(deviceID, observedState, health string, now int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO reports (device_id, ts, observed_state, health) VALUES (?, ?, ?, ?)`,
		deviceID, now, observedState, health); err != nil {
		tx.Rollback()
		return err
	}
	if _, err := tx.Exec(`UPDATE nodes SET observed_state = ?, last_report_at = ? WHERE id = ?`,
		observedState, now, deviceID); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -v`
Expected: PASS (all six tests). `NodeNameFor` is still undefined → this step will fail to **compile** until Task 3. To unblock, add a temporary stub at the bottom of `store.go`, then replace it in Task 3:

```go
// TEMP stub — replaced by names.go in Task 3.
func NodeNameFor(mac string) string { return "node-" + mac }
```

After adding the stub, run the command above. Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/store.go internal/store/store_test.go
git commit -m "feat(porta): store — porta sqlite schema + node/payload/command/report CRUD

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: Deterministic node naming (`NodeNameFor`)

Byte-faithful port of `examples/toit-gateway/names.toit`, so a node gets the *same* friendly name from the Go gateway as from the Toit gateway.

**Files:**
- Create: `internal/store/names.go`
- Test: `internal/store/names_test.go`
- Modify: `internal/store/store.go` (remove the Task 2 temp stub)

- [ ] **Step 1: Write the failing test**

`internal/store/names_test.go`:

```go
package store

import (
	"strings"
	"testing"
)

func TestNodeNameForDeterministic(t *testing.T) {
	a := NodeNameFor("aabbccddeeff")
	b := NodeNameFor("aabbccddeeff")
	if a != b {
		t.Errorf("not deterministic: %q vs %q", a, b)
	}
	if !strings.Contains(a, "-") {
		t.Errorf("expected adjective-noun shape, got %q", a)
	}
}

func TestNodeNameForDiffersAcrossMacs(t *testing.T) {
	if NodeNameFor("aabbccddeeff") == NodeNameFor("001122334455") {
		t.Error("different MACs should (very likely) differ")
	}
}

// Cross-check vector: jolly-pine's real MAC (30aea41a6208) was named
// "jolly-pine" by the Toit gateway. The Go port must agree byte-for-byte.
func TestNodeNameForMatchesToitGateway(t *testing.T) {
	if got := NodeNameFor("30aea41a6208"); got != "jolly-pine" {
		t.Errorf("NodeNameFor(jolly-pine MAC) = %q, want jolly-pine", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run NodeNameFor -v`
Expected: FAIL — the temp stub returns `node-30aea41a6208`, not `jolly-pine`.

- [ ] **Step 3: Implement and remove the stub**

Delete the temp `NodeNameFor` stub from `store.go`. Create `internal/store/names.go`:

```go
package store

// Word lists copied verbatim from examples/toit-gateway/names.toit so the Go
// gateway assigns the same friendly name as the Toit gateway for any MAC.
var adjectives = []string{
	"amber", "brave", "calm", "clever", "eager", "fancy", "gentle", "happy",
	"jolly", "keen", "lively", "merry", "noble", "proud", "quiet", "rapid",
	"shiny", "swift", "tidy", "witty",
}

var nouns = []string{
	"antler", "badger", "cedar", "comet", "dune", "ember", "falcon", "grove",
	"harbor", "ibex", "jaguar", "kestrel", "lynx", "maple", "nimbus", "otter",
	"pine", "quartz", "raven", "summit",
}

// NodeNameFor maps a 12-hex-lowercase MAC to a deterministic adjective-noun
// name. Horner hash with multiplier 31, masked to 31 bits, then indexed into
// the two 20-word lists. Collisions are accepted (operator can override).
func NodeNameFor(mac string) string {
	h := 0
	for _, c := range mac {
		h = (h*31 + int(c)) & 0x7fffffff
	}
	adjective := adjectives[h%len(adjectives)]
	noun := nouns[(h/len(adjectives))%len(nouns)]
	return adjective + "-" + noun
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/store/ -v`
Expected: PASS, including `TestNodeNameForMatchesToitGateway`. (If the cross-check fails, the Horner loop must iterate Unicode code points — Go's `range` over a string already yields runes, matching Toit's `--runes`; for the ASCII hex MAC, runes == bytes.)

- [ ] **Step 5: Commit**

```bash
git add internal/store/names.go internal/store/names_test.go internal/store/store.go
git commit -m "feat(porta): store — deterministic adjective-noun node naming (port of names.toit)

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: CRC32-IEEE helper

**Files:**
- Create: `internal/command/crc32.go`
- Test: `internal/command/crc32_test.go`

- [ ] **Step 1: Write the failing test**

`internal/command/crc32_test.go`:

```go
package command

import "testing"

func TestCRC32CanonicalVector(t *testing.T) {
	// IEEE check value: "123456789" → 0xCBF43926.
	if got := CRC32([]byte("123456789")); got != 0xCBF43926 {
		t.Errorf("CRC32(123456789) = %#x, want 0xCBF43926", got)
	}
}

func TestCRC32Empty(t *testing.T) {
	if got := CRC32([]byte{}); got != 0 {
		t.Errorf("CRC32(empty) = %#x, want 0", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/command/ -run CRC32 -v`
Expected: FAIL — `undefined: CRC32`.

- [ ] **Step 3: Implement**

`internal/command/crc32.go`:

```go
// Package command defines the porta command vocabulary, its wire codec, and
// payload helpers shared by the gateway control plane.
package command

import "hash/crc32"

// CRC32 computes the CRC32-IEEE of data, byte-identical to the protocol's
// image checksum (and to jag's X-Jaguar-CRC32). Go's stdlib IEEE table uses
// the reversed polynomial 0xEDB88320 with the standard init/xor, matching the
// Toit gateway's crc32.toit.
func CRC32(data []byte) uint32 {
	return crc32.ChecksumIEEE(data)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/command/ -run CRC32 -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/command/crc32.go internal/command/crc32_test.go
git commit -m "feat(porta): command — CRC32-IEEE payload checksum helper

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: Command codec — verbs, args, triggers, scalar fidelity

**Files:**
- Create: `internal/command/command.go`
- Test: `internal/command/command_test.go`

- [ ] **Step 1: Write the failing test**

`internal/command/command_test.go`:

```go
package command

import (
	"encoding/json"
	"testing"
)

func TestRunDefaults(t *testing.T) {
	c, err := Run(RunSpec{Name: "blink", CRC: 7, Size: 4096, Triggers: map[string]int64{"interval": 30}})
	if err != nil {
		t.Fatal(err)
	}
	if c.Verb != "run" {
		t.Fatalf("verb = %q", c.Verb)
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(c.ArgsJSON), &m); err != nil {
		t.Fatal(err)
	}
	if m["runlevel"].(float64) != 3 {
		t.Errorf("default runlevel = %v, want 3", m["runlevel"])
	}
	if m["lifecycle"].(string) != "run-once" {
		t.Errorf("default lifecycle = %v, want run-once", m["lifecycle"])
	}
	if args, ok := m["arguments"].([]interface{}); !ok || len(args) != 0 {
		t.Errorf("default arguments = %v, want []", m["arguments"])
	}
}

func TestRunRejectsBadLifecycle(t *testing.T) {
	_, err := Run(RunSpec{Name: "x", CRC: 1, Size: 1, Triggers: map[string]int64{"boot": 1}, Lifecycle: "always"})
	if err == nil {
		t.Error("expected error for invalid lifecycle")
	}
}

func TestEncodeWireShapeFlat(t *testing.T) {
	// Wire shape is a FLAT object: {"verb":..., <args flattened>} — no nested "args".
	wire := EncodeWire("run", `{"name":"blink","crc":7}`)
	var m map[string]interface{}
	if err := json.Unmarshal(wire, &m); err != nil {
		t.Fatal(err)
	}
	if m["verb"] != "run" || m["name"] != "blink" {
		t.Errorf("flat wire wrong: %v", m)
	}
	if _, nested := m["args"]; nested {
		t.Error("args must be flattened, not nested")
	}
}

func TestEncodeWireScalarFidelity(t *testing.T) {
	// A float must NOT collapse to an int when spliced onto the wire.
	wire := EncodeWire("set", `{"temp":21.5,"count":7}`)
	s := string(wire)
	if !contains(s, "21.5") {
		t.Errorf("float 21.5 lost in %q", s)
	}
	if contains(s, "7.0") || !contains(s, `"count":7`) {
		t.Errorf("int 7 became float in %q", s)
	}
}

func TestSetPollIntervalAndStop(t *testing.T) {
	c := SetPollInterval(45)
	if c.Verb != "set-poll-interval" {
		t.Fatalf("verb = %q", c.Verb)
	}
	if c.ArgsJSON != `{"interval":45}` {
		t.Errorf("args = %q", c.ArgsJSON)
	}
	st := Stop("blink")
	if st.Verb != "stop" || st.ArgsJSON != `{"name":"blink"}` {
		t.Errorf("stop = %+v", st)
	}
}

func TestTriggersFromFlags(t *testing.T) {
	m, err := TriggersFromFlags([]string{"boot", "gpio-high=21", "install=1"}, 60)
	if err != nil {
		t.Fatal(err)
	}
	if m["boot"] != 1 || m["interval"] != 60 || m["install"] != 1 || m["gpio-high:21"] != 21 {
		t.Errorf("triggers = %v", m)
	}
}

func TestTriggersRejectsUnknown(t *testing.T) {
	if _, err := TriggersFromFlags([]string{"laser=1"}, 0); err == nil {
		t.Error("unknown trigger should be rejected")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/command/ -run 'Run|Encode|Set|Stop|Triggers' -v`
Expected: FAIL — undefined symbols.

- [ ] **Step 3: Implement**

`internal/command/command.go`:

```go
package command

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Command is a control-plane command: a verb plus its args as a JSON object
// string (stored verbatim to preserve scalar types end to end).
type Command struct {
	Verb     string
	ArgsJSON string
}

// RunSpec describes a run command before encoding.
type RunSpec struct {
	Name      string
	CRC       int64
	Size      int64
	Triggers  map[string]int64
	Runlevel  int    // 0 means "use default 3"
	Lifecycle string // "" means "use default run-once"
	Arguments []string
}

func validLifecycle(lc string) bool { return lc == "run-once" || lc == "run-loop" }

// Run builds a run command, applying defaults (runlevel 3, lifecycle run-once,
// empty arguments) and validating the lifecycle.
func Run(spec RunSpec) (Command, error) {
	runlevel := spec.Runlevel
	if runlevel == 0 {
		runlevel = 3
	}
	lifecycle := spec.Lifecycle
	if lifecycle == "" {
		lifecycle = "run-once"
	}
	if !validLifecycle(lifecycle) {
		return Command{}, fmt.Errorf("invalid lifecycle %q (expected run-once or run-loop)", lifecycle)
	}
	args := spec.Arguments
	if args == nil {
		args = []string{}
	}
	triggers := spec.Triggers
	if triggers == nil {
		triggers = map[string]int64{}
	}
	// Build the args object with typed values so json.Marshal keeps int/float
	// distinctions. map[string]any marshals deterministically by key.
	obj := map[string]interface{}{
		"name":      spec.Name,
		"crc":       spec.CRC,
		"size":      spec.Size,
		"triggers":  triggers,
		"runlevel":  runlevel,
		"lifecycle": lifecycle,
		"arguments": args,
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return Command{}, err
	}
	return Command{Verb: "run", ArgsJSON: string(b)}, nil
}

// Stop builds a stop command.
func Stop(name string) Command {
	b, _ := json.Marshal(map[string]string{"name": name})
	return Command{Verb: "stop", ArgsJSON: string(b)}
}

// SetPollInterval builds a set-poll-interval command.
func SetPollInterval(intervalS int64) Command {
	return Command{Verb: "set-poll-interval", ArgsJSON: fmt.Sprintf(`{"interval":%d}`, intervalS)}
}

// EncodeWire produces the flat wire JSON {"verb":<verb>, <args flattened>}.
// The args object is spliced in via json.RawMessage so number tokens are
// copied byte-for-byte — int stays int, float stays float.
func EncodeWire(verb, argsJSON string) []byte {
	fields := map[string]json.RawMessage{}
	if argsJSON != "" {
		_ = json.Unmarshal([]byte(argsJSON), &fields)
	}
	vb, _ := json.Marshal(verb)
	fields["verb"] = vb
	// Deterministic key order keeps output stable for tests and logs.
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		sb.Write(kb)
		sb.WriteByte(':')
		sb.Write(fields[k])
	}
	sb.WriteByte('}')
	return []byte(sb.String())
}

// TriggersFromFlags parses --trigger specs plus an optional --interval shorthand
// into the trigger map. Valid keys: boot, interval=<s>, install=<n>,
// gpio-high=<pin>, gpio-low=<pin>, gpio-touch=<pin>. intervalS<=0 is ignored.
func TriggersFromFlags(flags []string, intervalS int64) (map[string]int64, error) {
	m := map[string]int64{}
	if intervalS > 0 {
		m["interval"] = intervalS
	}
	for _, spec := range flags {
		eq := strings.Index(spec, "=")
		if eq < 0 {
			if spec == "boot" {
				m["boot"] = 1
				continue
			}
			return nil, fmt.Errorf("unknown trigger: %s", spec)
		}
		typ := spec[:eq]
		val, err := strconv.ParseInt(spec[eq+1:], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid trigger value: %s", spec)
		}
		switch typ {
		case "interval", "install":
			m[typ] = val
		case "gpio-high", "gpio-low", "gpio-touch":
			m[fmt.Sprintf("%s:%d", typ, val)] = val
		default:
			return nil, fmt.Errorf("unknown trigger: %s", typ)
		}
	}
	return m, nil
}

// Decode parses a flat wire command back into verb + a typed args map, using
// json.Number so scalar types survive the round-trip (used by display/tests).
func Decode(wire []byte) (verb string, args map[string]interface{}, err error) {
	dec := json.NewDecoder(strings.NewReader(string(wire)))
	dec.UseNumber()
	raw := map[string]interface{}{}
	if err = dec.Decode(&raw); err != nil {
		return "", nil, err
	}
	if v, ok := raw["verb"].(string); ok {
		verb = v
	}
	args = map[string]interface{}{}
	for k, v := range raw {
		if k != "verb" {
			args[k] = v
		}
	}
	return verb, args, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/command/ -v`
Expected: PASS (all command + crc tests).

- [ ] **Step 5: Commit**

```bash
git add internal/command/command.go internal/command/command_test.go
git commit -m "feat(porta): command — verb codec, run/stop/set-poll, trigger map, scalar fidelity

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 6: Duration parsing (jag-style)

**Files:**
- Create: `internal/command/duration.go`
- Test: `internal/command/duration_test.go`

- [ ] **Step 1: Write the failing test**

`internal/command/duration_test.go`:

```go
package command

import "testing"

func TestParseDurationSeconds(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"30s", 30}, {"5m", 300}, {"2h", 7200}, {"1d", 86400}, {"45", 45},
	}
	for _, c := range cases {
		got, err := ParseDurationSeconds(c.in)
		if err != nil || got != c.want {
			t.Errorf("ParseDurationSeconds(%q) = %d, %v; want %d", c.in, got, err, c.want)
		}
	}
}

func TestParseDurationSecondsRejectsGarbage(t *testing.T) {
	for _, in := range []string{"", "abc", "10x", "3.5m"} {
		if _, err := ParseDurationSeconds(in); err == nil {
			t.Errorf("ParseDurationSeconds(%q) should error", in)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/command/ -run Duration -v`
Expected: FAIL — `undefined: ParseDurationSeconds`.

- [ ] **Step 3: Implement**

`internal/command/duration.go`:

```go
package command

import (
	"fmt"
	"strconv"
)

// ParseDurationSeconds parses a jag-style duration into whole seconds. Accepts
// a bare integer (seconds) or an integer with a unit suffix s/m/h/d.
func ParseDurationSeconds(s string) (int64, error) {
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	mult := int64(1)
	body := s
	switch s[len(s)-1] {
	case 's':
		mult, body = 1, s[:len(s)-1]
	case 'm':
		mult, body = 60, s[:len(s)-1]
	case 'h':
		mult, body = 3600, s[:len(s)-1]
	case 'd':
		mult, body = 86400, s[:len(s)-1]
	}
	n, err := strconv.ParseInt(body, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	return n * mult, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/command/ -run Duration -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/command/duration.go internal/command/duration_test.go
git commit -m "feat(porta): command — jag-style duration parsing

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 7: Shared TFTP transport — add the `Dispatcher` interface

Add a dispatcher path to the shared `tftp.Server` so the porta handler can return `([]byte, error)` (drain chokepoint) and be told when an RRQ transfer completes (mark-delivered). The legacy `RegisterGet`/`RegisterPut` path is untouched — parked ST keeps working.

**Files:**
- Modify: `internal/tftp/server.go`
- Test: `internal/tftp/dispatcher_test.go` (new)

- [ ] **Step 1: Write the failing test**

`internal/tftp/dispatcher_test.go`:

```go
package tftp

import (
	"errors"
	"testing"
)

// fakeDispatcher records calls and serves canned responses.
type fakeDispatcher struct {
	readData  []byte
	readErr   error
	writeErr  error
	acceptErr error
	completed []string // "op:resource:ok"
	wrote     []byte
}

func (f *fakeDispatcher) Read(resource, peer string) ([]byte, error) { return f.readData, f.readErr }
func (f *fakeDispatcher) AcceptWrite(resource, peer string) error    { return f.acceptErr }
func (f *fakeDispatcher) Write(resource, peer string, data []byte) error {
	f.wrote = data
	return f.writeErr
}
func (f *fakeDispatcher) Complete(op uint16, resource, peer string, ok bool) {
	tag := "rrq"
	if op == OpWRQ {
		tag = "wrq"
	}
	res := tag + ":" + resource + ":"
	if ok {
		res += "ok"
	} else {
		res += "fail"
	}
	f.completed = append(f.completed, res)
}

// drive feeds an RRQ (no blksize) then ACKs each DATA block to completion,
// returning the concatenated served bytes.
func driveRRQ(s *Server, resource, peer string) (data []byte, sawError bool) {
	replies := s.HandlePacketFrom(BuildRRQ(resource, 0), peer)
	for len(replies) > 0 {
		pkt := replies[0]
		op, _ := ParseOpcode(pkt)
		switch op {
		case OpERROR:
			return data, true
		case OpDATA:
			block, chunk, _ := ParseData(pkt)
			data = append(data, chunk...)
			replies = s.HandlePacketFrom(BuildACK(block), peer)
		default:
			return data, false
		}
	}
	return data, false
}

func TestDispatcherReadServesCommand(t *testing.T) {
	d := &fakeDispatcher{readData: []byte(`{"verb":"run"}`)}
	s := NewServer()
	s.SetDispatcher(d)
	data, sawErr := driveRRQ(s, "commands?id=abc", "1.2.3.4:5")
	if sawErr {
		t.Fatal("unexpected ERROR")
	}
	if string(data) != `{"verb":"run"}` {
		t.Errorf("served %q", data)
	}
	if len(d.completed) != 1 || d.completed[0] != "rrq:commands?id=abc:ok" {
		t.Errorf("completed = %v", d.completed)
	}
}

func TestDispatcherDrainIsEmptySuccessNotError(t *testing.T) {
	d := &fakeDispatcher{readData: nil, readErr: nil} // empty queue sentinel
	s := NewServer()
	s.SetDispatcher(d)
	data, sawErr := driveRRQ(s, "commands?id=abc", "p")
	if sawErr {
		t.Fatal("drain must be empty SUCCESS, not ERROR")
	}
	if len(data) != 0 {
		t.Errorf("drain body = %q, want empty", data)
	}
}

func TestDispatcherReadErrorIsTFTPError(t *testing.T) {
	d := &fakeDispatcher{readErr: errors.New("boom")}
	s := NewServer()
	s.SetDispatcher(d)
	_, sawErr := driveRRQ(s, "commands?id=abc", "p")
	if !sawErr {
		t.Fatal("read error must produce a TFTP ERROR packet")
	}
}

func TestDispatcherWriteIngestsAndCompletes(t *testing.T) {
	d := &fakeDispatcher{}
	s := NewServer()
	s.SetDispatcher(d)
	// WRQ (no blksize) → ACK0, then one short DATA block → ACK1 + Write + Complete.
	replies := s.HandlePacketFrom(BuildWRQ("report?id=abc", 0), "p")
	if op, _ := ParseOpcode(replies[0]); op != OpACK {
		t.Fatalf("WRQ reply op = %d, want ACK", op)
	}
	replies = s.HandlePacketFrom(BuildData(1, []byte(`{"apps":{}}`)), "p")
	if op, _ := ParseOpcode(replies[0]); op != OpACK {
		t.Fatalf("DATA reply op = %d, want ACK", op)
	}
	if string(d.wrote) != `{"apps":{}}` {
		t.Errorf("wrote %q", d.wrote)
	}
	if len(d.completed) != 1 || d.completed[0] != "wrq:report?id=abc:ok" {
		t.Errorf("completed = %v", d.completed)
	}
}

func TestDispatcherAcceptWriteRejection(t *testing.T) {
	d := &fakeDispatcher{acceptErr: errors.New("access denied")}
	s := NewServer()
	s.SetDispatcher(d)
	replies := s.HandlePacketFrom(BuildWRQ("data?id=abc", 0), "p")
	if op, _ := ParseOpcode(replies[0]); op != OpERROR {
		t.Fatalf("rejected WRQ op = %d, want ERROR", op)
	}
}
```

This test assumes `BuildRRQ`/`BuildWRQ` accept `blksize=0` to mean "no option". Verify the existing signatures in `packet.go`; the current `BuildRRQ(path, blksize)` / `BuildWRQ(path, blksize)` already exist. If `0` does not already mean "omit the blksize option," adjust the test to pass the value that does (check `packet_test.go` for the convention) — do not change `packet.go`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tftp/ -run Dispatcher -v`
Expected: FAIL — `undefined: SetDispatcher` / `HandlePacketFrom` / `Dispatcher` / `OpERROR`.

(If `OpERROR` is named differently in `packet.go`, e.g. `opError`, use the exported constant that exists; add one if none is exported.)

- [ ] **Step 3: Implement the dispatcher path in `server.go`**

Add to `internal/tftp/server.go`:

```go
// Dispatcher routes parsed TFTP resources (full "base?k=v" strings) to the
// application. It is the porta-side alternative to RegisterGet/RegisterPut.
type Dispatcher interface {
	// Read serves bytes for an RRQ. A non-nil error → TFTP ERROR packet;
	// (nil, nil) → a valid empty body (the drain sentinel).
	Read(resource, peer string) ([]byte, error)
	// AcceptWrite gates a WRQ at request time. Non-nil → TFTP ERROR, no transfer.
	AcceptWrite(resource, peer string) error
	// Write ingests a completed WRQ body. Non-nil → TFTP ERROR.
	Write(resource, peer string, data []byte) error
	// Complete is called when a transfer finishes (ok=false on failure).
	Complete(op uint16, resource, peer string, ok bool)
}
```

Add a `dispatcher Dispatcher` field to `Server`, and to the dispatcher-mode transfers a `resource` and `peer` field so completion callbacks have context. Extend `getTransfer`/`putTransfer`:

```go
type getTransfer struct {
	chunks      [][]byte
	blockIndex  int
	blksize     int
	oackPending bool
	resource    string // dispatcher mode
	peer        string
}

type putTransfer struct {
	path     string
	handler  PutHandler // legacy mode
	buf      []byte
	blksize  int
	resource string // dispatcher mode
	peer     string
}
```

Add the setter and the peer-aware entrypoint:

```go
// SetDispatcher switches the server to dispatcher mode.
func (s *Server) SetDispatcher(d Dispatcher) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dispatcher = d
}

// HandlePacketFrom processes a packet, threading the peer address to the
// dispatcher. HandlePacket(pkt) is equivalent to HandlePacketFrom(pkt, "").
func (s *Server) HandlePacketFrom(pkt []byte, peer string) [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	op, err := ParseOpcode(pkt)
	if err != nil {
		return nil
	}
	switch op {
	case OpRRQ:
		if s.dispatcher != nil {
			return s.dispatchRRQ(pkt, peer)
		}
		return s.handleRRQ(pkt)
	case OpWRQ:
		if s.dispatcher != nil {
			return s.dispatchWRQ(pkt, peer)
		}
		return s.handleWRQ(pkt)
	case OpACK:
		return s.handleACK(pkt)
	case OpDATA:
		return s.handleDATA(pkt)
	default:
		return nil
	}
}
```

Make the existing `HandlePacket` delegate (so ST callers are unchanged):

```go
func (s *Server) HandlePacket(pkt []byte) [][]byte {
	return s.HandlePacketFrom(pkt, "")
}
```

Note: `HandlePacket` currently takes the lock itself. After this change the lock is taken in `HandlePacketFrom`; remove the `s.mu.Lock()`/`defer s.mu.Unlock()` from the old `HandlePacket` body (now a one-line delegate) to avoid a double-lock deadlock.

Add the dispatcher RRQ/WRQ handlers (they mirror the legacy ones but key transfers by `resource` and carry peer):

```go
func (s *Server) dispatchRRQ(pkt []byte, peer string) [][]byte {
	resource, opts, err := ParseRequest(pkt)
	if err != nil {
		return [][]byte{BuildError(0, "malformed request")}
	}
	data, derr := s.dispatcher.Read(resource, peer)
	if derr != nil {
		return [][]byte{BuildError(1, derr.Error())}
	}
	blksize := DefaultBlockSize
	hasBlksize := false
	if bs, found := opts["blksize"]; found {
		if v, err := strconv.Atoi(bs); err == nil && v > 0 {
			blksize, hasBlksize = v, true
		}
	}
	chunks := ChunkData(data, blksize)
	xfer := &getTransfer{chunks: chunks, blksize: blksize, oackPending: hasBlksize, resource: resource, peer: peer}
	s.gets[resource] = xfer
	if hasBlksize {
		return [][]byte{BuildOACK(map[string]string{"blksize": strconv.Itoa(blksize)})}
	}
	xfer.blockIndex = 1
	return [][]byte{BuildData(1, chunks[0])}
}

func (s *Server) dispatchWRQ(pkt []byte, peer string) [][]byte {
	resource, opts, err := ParseRequest(pkt)
	if err != nil {
		return [][]byte{BuildError(0, "malformed request")}
	}
	if aerr := s.dispatcher.AcceptWrite(resource, peer); aerr != nil {
		return [][]byte{BuildError(2, aerr.Error())} // 2 = access violation
	}
	blksize := DefaultBlockSize
	hasBlksize := false
	if bs, found := opts["blksize"]; found {
		if v, err := strconv.Atoi(bs); err == nil && v > 0 {
			blksize, hasBlksize = v, true
		}
	}
	s.puts[resource] = &putTransfer{resource: resource, peer: peer, blksize: blksize}
	if hasBlksize {
		return [][]byte{BuildOACK(map[string]string{"blksize": strconv.Itoa(blksize)})}
	}
	return [][]byte{BuildACK(0)}
}
```

In `handleACK`, after the block-complete branch deletes the transfer, fire the dispatcher completion if this was a dispatcher-mode get. Replace the two `delete(s.gets, path); return nil` completion points with a helper:

```go
func (s *Server) finishGet(path string, xfer *getTransfer) [][]byte {
	delete(s.gets, path)
	if s.dispatcher != nil && xfer.resource != "" {
		s.dispatcher.Complete(OpRRQ, xfer.resource, xfer.peer, true)
	}
	return nil
}
```

i.e. in `handleACK`, change `if xfer.blockIndex >= len(xfer.chunks) { delete(s.gets, path); return nil }` to `... { return s.finishGet(path, xfer) }`, and the trailing `delete(s.gets, path); return nil` likewise to `return s.finishGet(path, xfer)`.

In `handleDATA`, on the final block, route dispatcher-mode puts to `Write`/`Complete` and emit ERROR on a write failure:

```go
func (s *Server) handleDATA(pkt []byte) [][]byte {
	block, data, err := ParseData(pkt)
	if err != nil {
		return nil
	}
	for path, xfer := range s.puts {
		xfer.buf = append(xfer.buf, data...)
		if len(data) < xfer.blksize {
			delete(s.puts, path)
			if s.dispatcher != nil && xfer.resource != "" {
				if werr := s.dispatcher.Write(xfer.resource, xfer.peer, xfer.buf); werr != nil {
					s.dispatcher.Complete(OpWRQ, xfer.resource, xfer.peer, false)
					return [][]byte{BuildError(2, werr.Error())}
				}
				s.dispatcher.Complete(OpWRQ, xfer.resource, xfer.peer, true)
				return [][]byte{BuildACK(block)}
			}
			xfer.handler(xfer.path, xfer.buf) // legacy mode
			return [][]byte{BuildACK(block)}
		}
		return [][]byte{BuildACK(block)}
	}
	return nil
}
```

If `OpERROR` is not an exported constant, add `OpERROR = 5` alongside the other opcodes in `packet.go` (it is the standard TFTP error opcode; do not change `BuildError`).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/tftp/ -v`
Expected: PASS — new dispatcher tests AND the pre-existing legacy transfer tests (ST path must stay green).

- [ ] **Step 5: Commit**

```bash
git add internal/tftp/server.go internal/tftp/dispatcher_test.go internal/tftp/packet.go
git commit -m "feat(porta): tftp — add Dispatcher interface (([]byte,error) read + transfer-complete)

Legacy RegisterGet/RegisterPut path untouched so the parked ST server stays green.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 8: porta TFTP handler — resource dispatch + drain chokepoint

Implements `tftp.Dispatcher` against the store. This is where the drain-sentinel footgun lives — §7's required regression test is here.

**Files:**
- Create: `internal/handler/handler.go`
- Test: `internal/handler/handler_test.go`

- [ ] **Step 1: Write the failing test**

`internal/handler/handler_test.go`:

```go
package handler

import (
	"errors"
	"testing"

	"github.com/davidg238/porta/internal/store"
	"github.com/davidg238/porta/internal/tftp"
)

func newH(t *testing.T) (*Handler, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/h.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	clock := int64(1000)
	return New(st, func() int64 { return clock }), st
}

func TestParseResource(t *testing.T) {
	base, params := parseResource("payload?id=aabb&crc=12345")
	if base != "payload" || params["id"] != "aabb" || params["crc"] != "12345" {
		t.Errorf("got %q %v", base, params)
	}
	base, params = parseResource("commands?id=")
	if base != "commands" || params["id"] != "" {
		t.Errorf("bare value: %q %v", base, params)
	}
	base, params = parseResource("report")
	if base != "report" || len(params) != 0 {
		t.Errorf("no query: %q %v", base, params)
	}
}

func TestReadCommandsDrainIsEmptyNotError(t *testing.T) {
	h, st := newH(t)
	st.TouchNode("dev", "p", 1000)
	data, err := h.Read("commands?id=dev", "p:1")
	if err != nil {
		t.Fatalf("empty queue must be (nil,nil), got err %v", err)
	}
	if len(data) != 0 {
		t.Errorf("empty queue body = %q, want empty", data)
	}
}

func TestReadCommandsServesFlatCommand(t *testing.T) {
	h, st := newH(t)
	st.TouchNode("dev", "p", 1000)
	st.EnqueueCommand("dev", "run", `{"name":"blink","crc":7}`, "cli", 1000)
	data, err := h.Read("commands?id=dev", "p:1")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"crc":7,"name":"blink","verb":"run"}` {
		t.Errorf("served %q", data)
	}
}

// REQUIRED regression test (spec §7): a store error on a commands RRQ must be a
// TFTP ERROR (non-nil error), while a genuinely empty queue is empty success —
// the two must never collapse into the same response.
func TestDrainVsErrorNeverCollapse(t *testing.T) {
	h, st := newH(t)
	st.TouchNode("dev", "p", 1000)

	// Empty queue → (nil, nil): empty success.
	data, err := h.Read("commands?id=dev", "p")
	if err != nil || len(data) != 0 {
		t.Fatalf("empty queue: got (%q, %v), want (empty, nil)", data, err)
	}

	// Forced store error → non-nil error (so the transport emits a TFTP ERROR).
	st.Close() // subsequent queries fail
	_, err = h.Read("commands?id=dev", "p")
	if err == nil {
		t.Fatal("store error must surface as a non-nil error, not an empty body")
	}
	_ = errors.Is // keep import meaningful if refactored
}

func TestReadPayloadRawBytesAndNotFound(t *testing.T) {
	h, st := newH(t)
	st.RegisterPayload(12345, "blink", []byte{1, 2, 3})
	data, err := h.Read("payload?id=dev&crc=12345", "p")
	if err != nil || string(data) != string([]byte{1, 2, 3}) {
		t.Errorf("payload = %q, %v", data, err)
	}
	if _, err := h.Read("payload?id=dev&crc=99999", "p"); err == nil {
		t.Error("missing crc must error (→ file not found)")
	}
	if _, err := h.Read("payload?id=dev&crc=notanint", "p"); err == nil {
		t.Error("bad crc must error")
	}
}

func TestReadUnknownResourceErrors(t *testing.T) {
	h, _ := newH(t)
	if _, err := h.Read("nonsense?id=dev", "p"); err == nil {
		t.Error("unknown RRQ resource must error")
	}
}

func TestReadTouchesNode(t *testing.T) {
	h, st := newH(t)
	if _, err := h.Read("commands?id=newdev", "1.2.3.4:9"); err != nil {
		t.Fatal(err)
	}
	n, _ := st.GetNode("newdev")
	if n == nil || !n.LastSeen.Valid {
		t.Error("RRQ with ?id= must touch the node")
	}
	if n.SourceAddr != "1.2.3.4:9" {
		t.Errorf("source_addr = %q, want recorded from peer", n.SourceAddr)
	}
}

func TestWriteReportIngest(t *testing.T) {
	h, st := newH(t)
	if err := h.AcceptWrite("report?id=dev", "p"); err != nil {
		t.Fatalf("report WRQ should be accepted: %v", err)
	}
	body := `{"apps":{"blink":{"crc":7}},"config":{"blink":{"k":1}},"health":{"uptime":42}}`
	if err := h.Write("report?id=dev", "p", []byte(body)); err != nil {
		t.Fatal(err)
	}
	n, _ := st.GetNode("dev")
	if n == nil || n.ObservedState != `{"apps":{"blink":{"crc":7}},"config":{"blink":{"k":1}}}` {
		t.Errorf("observed_state = %q", n.ObservedState)
	}
}

func TestWriteRejectsNonReportAndMissingID(t *testing.T) {
	h, _ := newH(t)
	if err := h.AcceptWrite("data?id=dev", "p"); err == nil {
		t.Error("data WRQ deferred to B3 → must be rejected in B1")
	}
	if err := h.AcceptWrite("report", "p"); err == nil {
		t.Error("report without ?id= must be rejected")
	}
}

func TestCompleteMarksDelivered(t *testing.T) {
	h, st := newH(t)
	st.TouchNode("dev", "p", 1000)
	id, _ := st.EnqueueCommand("dev", "run", `{"name":"x"}`, "cli", 1000)
	// Serving alone must NOT mark delivered.
	h.Read("commands?id=dev", "p")
	c, _ := st.NextUndelivered("dev")
	if c == nil || c.ID != id {
		t.Fatal("Read must not mark delivered")
	}
	// Transfer-complete marks it.
	h.Complete(tftp.OpRRQ, "commands?id=dev", "p", true)
	if c, _ := st.NextUndelivered("dev"); c != nil {
		t.Error("Complete(RRQ, commands) must mark delivered")
	}
}

func TestCompletePayloadDoesNotMark(t *testing.T) {
	h, st := newH(t)
	st.TouchNode("dev", "p", 1000)
	st.EnqueueCommand("dev", "run", `{"name":"x"}`, "cli", 1000)
	h.Complete(tftp.OpRRQ, "payload?id=dev&crc=1", "p", true)
	if c, _ := st.NextUndelivered("dev"); c == nil {
		t.Error("completing a payload transfer must NOT mark a command delivered")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/handler/ -v`
Expected: FAIL — `undefined: New` / `parseResource`.

- [ ] **Step 3: Implement**

`internal/handler/handler.go`:

```go
// Package handler implements the porta TFTP resource surface as a
// tftp.Dispatcher backed by the store.
package handler

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/davidg238/porta/internal/command"
	"github.com/davidg238/porta/internal/store"
	"github.com/davidg238/porta/internal/tftp"
)

// Handler dispatches TFTP resources against the store.
type Handler struct {
	store *store.Store
	now   func() int64
}

// New creates a Handler. now supplies the current epoch seconds (injectable
// for tests).
func New(st *store.Store, now func() int64) *Handler {
	return &Handler{store: st, now: now}
}

// parseResource splits "base?k=v&k2=v2" into base + params. A bare key maps to "".
func parseResource(raw string) (string, map[string]string) {
	params := map[string]string{}
	q := strings.Index(raw, "?")
	if q < 0 {
		return raw, params
	}
	base := raw[:q]
	for _, kv := range strings.Split(raw[q+1:], "&") {
		if kv == "" {
			continue
		}
		if eq := strings.Index(kv, "="); eq < 0 {
			params[kv] = ""
		} else {
			params[kv[:eq]] = kv[eq+1:]
		}
	}
	return base, params
}

// Read serves an RRQ. Touches the node on ?id=. The "commands" branch is the
// single drain chokepoint: err != nil → TFTP ERROR; (nil,nil) → empty body
// (queue drained); len>0 → the command.
func (h *Handler) Read(resource, peer string) ([]byte, error) {
	base, params := parseResource(resource)
	if id, ok := params["id"]; ok && id != "" {
		if err := h.store.TouchNode(id, peer, h.now()); err != nil {
			return nil, err
		}
	}
	switch base {
	case "commands":
		return h.readCommands(params["id"])
	case "payload":
		return h.readPayload(params)
	default:
		return nil, fmt.Errorf("file not found: %s", base)
	}
}

// readCommands is the chokepoint. Every return is one of: (nil, err) for a real
// error → TFTP ERROR; (nil, nil) for an empty queue → drain sentinel; (bytes,
// nil) for a command. No error path can fall through to an empty body.
func (h *Handler) readCommands(id string) ([]byte, error) {
	if id == "" {
		return nil, fmt.Errorf("commands: missing id")
	}
	cmd, err := h.store.NextUndelivered(id)
	if err != nil {
		return nil, err
	}
	if cmd == nil {
		return nil, nil // drain sentinel
	}
	return command.EncodeWire(cmd.Verb, cmd.Args), nil
}

func (h *Handler) readPayload(params map[string]string) ([]byte, error) {
	crc, err := strconv.ParseInt(params["crc"], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("payload: invalid crc %q", params["crc"])
	}
	img, err := h.store.Payload(crc)
	if err != nil {
		return nil, err
	}
	if img == nil {
		return nil, fmt.Errorf("payload not found: crc=%d", crc)
	}
	return img, nil
}

// AcceptWrite gates WRQs: only report?id= is accepted in B1. Everything else
// (data → B3, missing id) is rejected → TFTP ERROR.
func (h *Handler) AcceptWrite(resource, peer string) error {
	base, params := parseResource(resource)
	if base != "report" {
		return fmt.Errorf("access denied: %s", base)
	}
	if params["id"] == "" {
		return fmt.Errorf("access denied: report missing id")
	}
	return nil
}

// Write ingests a completed report body: cache {apps,config} as observed_state
// (config stored, not reconciled — that's B2) and append to the report log.
func (h *Handler) Write(resource, peer string, data []byte) error {
	base, params := parseResource(resource)
	id := params["id"]
	if base != "report" || id == "" {
		return fmt.Errorf("access denied")
	}
	if id != "" {
		if err := h.store.TouchNode(id, peer, h.now()); err != nil {
			return err
		}
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return fmt.Errorf("report: bad json: %w", err)
	}
	field := func(k string) json.RawMessage {
		if v, ok := obj[k]; ok {
			return v
		}
		return json.RawMessage("{}")
	}
	observed := fmt.Sprintf(`{"apps":%s,"config":%s}`, field("apps"), field("config"))
	health := string(field("health"))
	return h.store.InsertReport(id, observed, health, h.now())
}

// Complete marks a command delivered after a successful commands RRQ transfer —
// never on pop, never for payload transfers, never on failure.
func (h *Handler) Complete(op uint16, resource, peer string, ok bool) {
	if !ok || op != tftp.OpRRQ {
		return
	}
	base, params := parseResource(resource)
	if base != "commands" {
		return
	}
	id := params["id"]
	if id == "" {
		return
	}
	cmd, err := h.store.NextUndelivered(id)
	if err != nil || cmd == nil {
		return // nothing to mark (drain-sentinel transfer or transient error)
	}
	_ = h.store.MarkDelivered(cmd.ID, h.now())
}
```

Note on `TestReadCommandsServesFlatCommand`: `EncodeWire` emits keys in sorted order, so `{"crc":7,"name":"blink","verb":"run"}` is the expected output. Keep the test's expected string in that order.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/handler/ -v`
Expected: PASS — including `TestDrainVsErrorNeverCollapse`.

- [ ] **Step 5: Commit**

```bash
git add internal/handler/handler.go internal/handler/handler_test.go
git commit -m "feat(porta): handler — TFTP resource dispatch + drain chokepoint + mark-on-complete

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 9: CLI scaffold + `serve` (cobra root, UDP loop, new `cmd/porta`)

**Files:**
- Create: `internal/portacli/root.go`, `internal/portacli/resolve.go`, `internal/portacli/serve.go`
- Create: `cmd/porta/main.go`
- Test: `internal/portacli/resolve_test.go`
- Modify: `go.mod` / `go.sum` (add cobra)

- [ ] **Step 1: Add cobra**

```bash
cd /home/david/workspaceToit/porta
go get github.com/spf13/cobra@latest
```

- [ ] **Step 2: Write the failing test (node resolution)**

`internal/portacli/resolve_test.go`:

```go
package portacli

import (
	"testing"

	"github.com/davidg238/porta/internal/store"
)

func TestResolveNodeID(t *testing.T) {
	st, _ := store.Open(t.TempDir() + "/r.db")
	defer st.Close()
	st.TouchNode("aabbccddeeff", "p", 1000) // auto-named

	// A 12-hex string is treated as a MAC directly.
	if id, err := resolveNodeID(st, "aabbccddeeff"); err != nil || id != "aabbccddeeff" {
		t.Errorf("by mac: %q %v", id, err)
	}
	// A friendly name resolves via the store.
	n, _ := st.GetNode("aabbccddeeff")
	if id, err := resolveNodeID(st, n.Name); err != nil || id != "aabbccddeeff" {
		t.Errorf("by name: %q %v", id, err)
	}
	// Unknown name errors.
	if _, err := resolveNodeID(st, "no-such-node"); err == nil {
		t.Error("unknown node should error")
	}
}

func TestIsMAC(t *testing.T) {
	if !isMAC("30aea41a6208") {
		t.Error("12 lowercase hex should be a MAC")
	}
	if isMAC("jolly-pine") || isMAC("AABBCCDDEEFF") || isMAC("30aea41a620") {
		t.Error("non-12-lowercase-hex should not be a MAC")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/portacli/ -run 'Resolve|IsMAC' -v`
Expected: FAIL — undefined `resolveNodeID` / `isMAC`.

- [ ] **Step 4: Implement resolve + root + serve + main**

`internal/portacli/resolve.go`:

```go
package portacli

import (
	"fmt"

	"github.com/davidg238/porta/internal/store"
)

// isMAC reports whether s is exactly 12 lowercase hex digits.
func isMAC(s string) bool {
	if len(s) != 12 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// resolveNodeID turns a CLI <node> (MAC or friendly name) into a node id.
func resolveNodeID(st *store.Store, nodeArg string) (string, error) {
	if isMAC(nodeArg) {
		return nodeArg, nil
	}
	n, err := st.NodeByName(nodeArg)
	if err != nil {
		return "", err
	}
	if n == nil {
		return "", fmt.Errorf("no node named %q", nodeArg)
	}
	return n.ID, nil
}
```

`internal/portacli/root.go`:

```go
// Package portacli is the porta gateway's cobra command tree.
package portacli

import (
	"time"

	"github.com/davidg238/porta/internal/store"
	"github.com/spf13/cobra"
)

var (
	dbPath string
)

func nowSec() int64 { return time.Now().Unix() }

func openStore() (*store.Store, error) { return store.Open(dbPath) }

// NewRootCmd builds the porta command tree.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "porta",
		Short: "porta — northbound gateway for nodus-style nodes",
	}
	root.PersistentFlags().StringVar(&dbPath, "db", "porta.db", "SQLite database path")
	root.AddCommand(
		newServeCmd(),
		newScanCmd(),
		newPingCmd(),
		newDeviceCmd(),
		newContainerCmd(),
		newLogCmd(),
	)
	return root
}

// Execute runs the porta CLI.
func Execute() error { return NewRootCmd().Execute() }
```

`internal/portacli/serve.go`:

```go
package portacli

import (
	"fmt"
	"log"
	"net"

	"github.com/davidg238/porta/internal/handler"
	"github.com/davidg238/porta/internal/tftp"
	"github.com/spf13/cobra"
)

func newServeCmd() *cobra.Command {
	var port int
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the TFTP daemon serving the command queue + payloads",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			srv := tftp.NewServer()
			srv.SetDispatcher(handler.New(st, nowSec))

			addr := fmt.Sprintf(":%d", port)
			conn, err := net.ListenPacket("udp", addr)
			if err != nil {
				return err
			}
			defer conn.Close()
			log.Printf("porta: serving TFTP on udp %s (db=%s)", addr, dbPath)
			return serveUDP(conn, srv)
		},
	}
	cmd.Flags().IntVar(&port, "port", 6969, "UDP port")
	return cmd
}

// serveUDP reads TFTP packets and writes the server's replies back to the peer.
func serveUDP(conn net.PacketConn, srv *tftp.Server) error {
	buf := make([]byte, 2048)
	for {
		n, peer, err := conn.ReadFrom(buf)
		if err != nil {
			return err
		}
		pkt := make([]byte, n)
		copy(pkt, buf[:n])
		for _, reply := range srv.HandlePacketFrom(pkt, peer.String()) {
			if _, err := conn.WriteTo(reply, peer); err != nil {
				log.Printf("porta: WriteTo(%s): %v", peer, err)
			}
		}
	}
}
```

`cmd/porta/main.go`:

```go
// Command porta is the northbound gateway server + operator CLI.
package main

import (
	"fmt"
	"os"

	"github.com/davidg238/porta/internal/portacli"
)

func main() {
	if err := portacli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "porta:", err)
		os.Exit(1)
	}
}
```

The CLI references `newScanCmd`, `newPingCmd`, `newDeviceCmd`, `newContainerCmd`, `newLogCmd` — these are added in Tasks 10–11. To compile this task in isolation, add minimal stubs now in `root.go` and replace them in the next tasks, OR implement Tasks 9–11 before the first `go build ./...`. Recommended: add temporary stubs returning `&cobra.Command{Use: "...", RunE: func(*cobra.Command,[]string) error { return nil }}` so the tree compiles; later tasks overwrite the files that define them.

Add temporary stub file `internal/portacli/stubs.go` (deleted in Task 11):

```go
package portacli

import "github.com/spf13/cobra"

func newScanCmd() *cobra.Command      { return &cobra.Command{Use: "scan", RunE: noop} }
func newPingCmd() *cobra.Command      { return &cobra.Command{Use: "ping", RunE: noop} }
func newDeviceCmd() *cobra.Command    { return &cobra.Command{Use: "device", RunE: noop} }
func newContainerCmd() *cobra.Command { return &cobra.Command{Use: "container", RunE: noop} }
func newLogCmd() *cobra.Command       { return &cobra.Command{Use: "log", RunE: noop} }
func noop(*cobra.Command, []string) error { return nil }
```

- [ ] **Step 5: Run test + build to verify**

Run: `go test ./internal/portacli/ -run 'Resolve|IsMAC' -v && go build ./...`
Expected: PASS + clean build (porta binary builds).

- [ ] **Step 6: Smoke-test serve manually**

```bash
go run ./cmd/porta serve --db /tmp/porta-smoke.db --port 6969 &
sleep 1
# Expect log line "porta: serving TFTP on udp :6969"; then:
kill %1
```

- [ ] **Step 7: Commit**

```bash
git add internal/portacli/ cmd/porta/main.go go.mod go.sum
git commit -m "feat(porta): cli scaffold — cobra root, node resolve, serve + UDP loop

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 10: Inspection commands — scan, ping, device show, container list, log

**Files:**
- Create: `internal/portacli/inspect.go` (defines `newScanCmd`, `newPingCmd`, `newLogCmd`, `newContainerCmd` (with `list`), and the `device` parent with `show`)
- Modify: `internal/portacli/stubs.go` (remove the symbols now defined here)
- Test: `internal/portacli/inspect_test.go`

- [ ] **Step 1: Write the failing test (output helpers)**

Inspection commands print; test the pure formatting helpers rather than stdout.

`internal/portacli/inspect_test.go`:

```go
package portacli

import (
	"testing"
)

func TestRelativeAge(t *testing.T) {
	if got := relativeAge(0, 1000); got != "never" {
		t.Errorf("never-seen → %q", got)
	}
	if got := relativeAge(940, 1000); got != "60s ago" {
		t.Errorf("60s → %q", got)
	}
	if got := relativeAge(1000-3600, 1000); got != "60m ago" {
		t.Errorf("60m → %q", got)
	}
}

func TestAppsFromObserved(t *testing.T) {
	apps, err := appsFromObserved(`{"apps":{"blink":{"crc":7,"runlevel":3}},"config":{}}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 1 || apps[0].Name != "blink" || apps[0].CRC != 7 || apps[0].Runlevel != 3 {
		t.Errorf("apps = %+v", apps)
	}
	// Empty / absent observed_state → no apps, no error.
	if apps, err := appsFromObserved(""); err != nil || len(apps) != 0 {
		t.Errorf("empty observed: %+v %v", apps, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/portacli/ -run 'RelativeAge|AppsFromObserved' -v`
Expected: FAIL — undefined helpers.

- [ ] **Step 3: Implement**

First remove the now-defined symbols from `internal/portacli/stubs.go` (keep only the ones still implemented in Task 11 — after this task, only nothing remains; `newDeviceCmd`/`newContainerCmd`/`newScanCmd`/`newPingCmd`/`newLogCmd` are all defined here; the `device set-poll-interval`/`set-max-offline`/`name` and `container install`/`uninstall` mutating subcommands are added in Task 11 by *extending* the parents defined here). Update `stubs.go` to define only the mutating-subcommand attach points as needed, or delete it entirely and let Task 11 add subcommands. Simplest: **delete `stubs.go`** and have Task 11 attach its subcommands onto the parents created here.

```bash
rm internal/portacli/stubs.go
```

`internal/portacli/inspect.go`:

```go
package portacli

import (
	"encoding/json"
	"fmt"

	"github.com/davidg238/porta/internal/store"
	"github.com/spf13/cobra"
)

// relativeAge renders an epoch-seconds timestamp relative to now.
func relativeAge(ts, now int64) string {
	if ts <= 0 {
		return "never"
	}
	d := now - ts
	switch {
	case d < 60:
		return fmt.Sprintf("%ds ago", d)
	case d < 3600:
		return fmt.Sprintf("%dm ago", d/60)
	case d < 86400:
		return fmt.Sprintf("%dh ago", d/3600)
	default:
		return fmt.Sprintf("%dd ago", d/86400)
	}
}

// App is one entry from a node's observed apps map.
type App struct {
	Name     string
	CRC      int64
	Runlevel int64
}

// appsFromObserved decodes the apps map from a cached observed_state JSON blob.
func appsFromObserved(observed string) ([]App, error) {
	if observed == "" {
		return nil, nil
	}
	var obj struct {
		Apps map[string]struct {
			CRC      int64 `json:"crc"`
			Runlevel int64 `json:"runlevel"`
		} `json:"apps"`
	}
	if err := json.Unmarshal([]byte(observed), &obj); err != nil {
		return nil, err
	}
	var out []App
	for name, a := range obj.Apps {
		out = append(out, App{Name: name, CRC: a.CRC, Runlevel: a.Runlevel})
	}
	return out, nil
}

// deviceFlag adds and reads the shared -d/--device flag.
func deviceFlag(cmd *cobra.Command, dst *string) {
	cmd.Flags().StringVarP(dst, "device", "d", "", "node name or MAC")
	cmd.MarkFlagRequired("device")
}

func newScanCmd() *cobra.Command {
	var includeNeverSeen bool
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "List nodes (online/offline)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			nodes, err := st.ListNodes()
			if err != nil {
				return err
			}
			now := nowSec()
			for _, n := range nodes {
				if !n.LastSeen.Valid && !includeNeverSeen {
					continue
				}
				status := "offline"
				if n.Online(now) {
					status = "online"
				}
				seen := relativeAge(0, now)
				if n.LastSeen.Valid {
					seen = relativeAge(n.LastSeen.Int64, now)
				}
				fmt.Printf("%-12s  %-16s  %-12s  %s\n", n.ID, n.Name, seen, status)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&includeNeverSeen, "include-never-seen", false, "show nodes that never contacted")
	return cmd
}

func newPingCmd() *cobra.Command {
	var device string
	cmd := &cobra.Command{
		Use:   "ping",
		Short: "Report whether a node is online",
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			id, err := resolveNodeID(st, device)
			if err != nil {
				return err
			}
			n, err := st.GetNode(id)
			if err != nil || n == nil {
				return fmt.Errorf("node %s not found", id)
			}
			if n.Online(nowSec()) {
				fmt.Printf("%s (%s): online\n", n.Name, id)
			} else {
				fmt.Printf("%s (%s): offline\n", n.Name, id)
			}
			return nil
		},
	}
	deviceFlag(cmd, &device)
	return cmd
}

func newLogCmd() *cobra.Command {
	var device string
	cmd := &cobra.Command{
		Use:   "log",
		Short: "Command audit history",
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			id, err := resolveNodeID(st, device)
			if err != nil {
				return err
			}
			cmds, err := st.CommandLog(id)
			if err != nil {
				return err
			}
			for _, c := range cmds {
				delivered := "pending"
				if c.DeliveredAt.Valid {
					delivered = "yes"
				}
				fmt.Printf("#%-4d %-18s delivered=%-7s %s\n", c.ID, c.Verb, delivered, c.Args)
			}
			return nil
		},
	}
	deviceFlag(cmd, &device)
	return cmd
}

// newDeviceCmd builds the `device` parent with the read-only `show` subcommand.
// Task 11 attaches set-poll-interval / set-max-offline / name.
func newDeviceCmd() *cobra.Command {
	parent := &cobra.Command{Use: "device", Short: "Per-node operations"}
	parent.AddCommand(newDeviceShowCmd())
	return parent
}

func newDeviceShowCmd() *cobra.Command {
	var device string
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show node details",
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			id, err := resolveNodeID(st, device)
			if err != nil {
				return err
			}
			n, err := st.GetNode(id)
			if err != nil || n == nil {
				return fmt.Errorf("node %s not found", id)
			}
			now := nowSec()
			fmt.Printf("id:            %s\n", n.ID)
			fmt.Printf("name:          %s\n", n.Name)
			fmt.Printf("kind:          %s\n", n.Kind)
			fmt.Printf("source_addr:   %s\n", n.SourceAddr)
			lastSeen := "never"
			if n.LastSeen.Valid {
				lastSeen = relativeAge(n.LastSeen.Int64, now)
			}
			fmt.Printf("last_seen:     %s\n", lastSeen)
			fmt.Printf("poll_interval: %ds\n", n.PollIntervalS)
			fmt.Printf("max_offline:   %ds\n", n.MaxOfflineS)
			fmt.Printf("observed:      %s\n", n.ObservedState)
			un, _ := st.UndeliveredCommands(id)
			fmt.Printf("undelivered:   %d command(s)\n", len(un))
			return nil
		},
	}
	deviceFlag(cmd, &device)
	return cmd
}

// newContainerCmd builds the `container` parent with the read-only `list`.
// Task 11 attaches install / uninstall.
func newContainerCmd() *cobra.Command {
	parent := &cobra.Command{Use: "container", Short: "Container operations"}
	parent.AddCommand(newContainerListCmd())
	return parent
}

func newContainerListCmd() *cobra.Command {
	var device string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List apps from the latest observed report",
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			id, err := resolveNodeID(st, device)
			if err != nil {
				return err
			}
			n, err := st.GetNode(id)
			if err != nil || n == nil {
				return fmt.Errorf("node %s not found", id)
			}
			apps, err := appsFromObserved(n.ObservedState)
			if err != nil {
				return err
			}
			for _, a := range apps {
				fmt.Printf("%-16s crc=%-12d runlevel=%d\n", a.Name, a.CRC, a.Runlevel)
			}
			return nil
		},
	}
	deviceFlag(cmd, &device)
	return cmd
}

var _ = store.DefaultPollIntervalS // keep the store import if unused elsewhere
```

Remove the `var _ = store.DefaultPollIntervalS` line if `store` is otherwise referenced (it is, via types) — it is only a guard against an unused import during incremental edits.

- [ ] **Step 4: Run tests + build to verify**

Run: `go test ./internal/portacli/ -v && go build ./...`
Expected: PASS + clean build.

- [ ] **Step 5: Commit**

```bash
git add internal/portacli/inspect.go internal/portacli/inspect_test.go
git rm internal/portacli/stubs.go
git commit -m "feat(porta): cli — scan, ping, device show, container list, log

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 11: Mutation commands — device set-poll-interval / set-max-offline / name; container install / uninstall

**Files:**
- Create: `internal/portacli/mutate.go`
- Modify: `internal/portacli/inspect.go` (attach the new subcommands onto the `device` and `container` parents)
- Test: `internal/portacli/mutate_test.go`

- [ ] **Step 1: Write the failing test (install builds the right run command)**

`internal/portacli/mutate_test.go`:

```go
package portacli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/davidg238/porta/internal/command"
	"github.com/davidg238/porta/internal/store"
)

func TestInstallRegistersPayloadAndEnqueuesRun(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(dir + "/m.db")
	defer st.Close()
	st.EnsureNode("aabbccddeeff", 1000)

	bin := filepath.Join(dir, "blink.bin")
	img := []byte("fake-image-bytes")
	if err := os.WriteFile(bin, img, 0o644); err != nil {
		t.Fatal(err)
	}
	wantCRC := int64(command.CRC32(img))

	if err := runInstall(st, "aabbccddeeff", "blink", bin, installOpts{
		Lifecycle: "run-loop", Runlevel: 3, Triggers: []string{"boot"}, IntervalS: 0,
	}, 1000); err != nil {
		t.Fatal(err)
	}

	// Payload registered under the computed CRC.
	got, _ := st.Payload(wantCRC)
	if string(got) != string(img) {
		t.Errorf("payload not registered under crc %d", wantCRC)
	}
	// A run command was enqueued with crc, size, lifecycle, triggers.
	c, _ := st.NextUndelivered("aabbccddeeff")
	if c == nil || c.Verb != "run" {
		t.Fatalf("expected run command, got %+v", c)
	}
	var args map[string]interface{}
	json.Unmarshal([]byte(c.Args), &args)
	if args["crc"].(float64) != float64(wantCRC) {
		t.Errorf("crc arg = %v, want %d", args["crc"], wantCRC)
	}
	if args["size"].(float64) != float64(len(img)) {
		t.Errorf("size arg = %v, want %d", args["size"], len(img))
	}
	if args["lifecycle"].(string) != "run-loop" {
		t.Errorf("lifecycle = %v", args["lifecycle"])
	}
	trig := args["triggers"].(map[string]interface{})
	if trig["boot"].(float64) != 1 {
		t.Errorf("triggers = %v", trig)
	}
}

func TestInstallRejectsNonBin(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(dir + "/m.db")
	defer st.Close()
	st.EnsureNode("aabbccddeeff", 1000)
	pod := filepath.Join(dir, "x.pod")
	os.WriteFile(pod, []byte("x"), 0o644)
	if err := runInstall(st, "aabbccddeeff", "x", pod, installOpts{Lifecycle: "run-once"}, 1000); err == nil {
		t.Error(".pod must be rejected in B1 (only .bin)")
	}
}

func TestUninstallEnqueuesStop(t *testing.T) {
	st, _ := store.Open(t.TempDir() + "/u.db")
	defer st.Close()
	st.EnsureNode("aabbccddeeff", 1000)
	if err := runUninstall(st, "aabbccddeeff", "blink", 1000); err != nil {
		t.Fatal(err)
	}
	c, _ := st.NextUndelivered("aabbccddeeff")
	if c == nil || c.Verb != "stop" || c.Args != `{"name":"blink"}` {
		t.Errorf("stop command wrong: %+v", c)
	}
}

func TestSetPollIntervalCachesAndEnqueues(t *testing.T) {
	st, _ := store.Open(t.TempDir() + "/p.db")
	defer st.Close()
	st.EnsureNode("aabbccddeeff", 1000)
	if err := runSetPollInterval(st, "aabbccddeeff", 45, 1000); err != nil {
		t.Fatal(err)
	}
	n, _ := st.GetNode("aabbccddeeff")
	if n.PollIntervalS != 45 {
		t.Errorf("poll_interval not cached: %d", n.PollIntervalS)
	}
	c, _ := st.NextUndelivered("aabbccddeeff")
	if c == nil || c.Verb != "set-poll-interval" {
		t.Errorf("expected set-poll-interval, got %+v", c)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/portacli/ -run 'Install|Uninstall|SetPoll' -v`
Expected: FAIL — undefined `runInstall` / `installOpts` / `runUninstall` / `runSetPollInterval`.

- [ ] **Step 3: Implement the action functions + cobra wiring**

`internal/portacli/mutate.go`:

```go
package portacli

import (
	"fmt"
	"os"
	"strings"

	"github.com/davidg238/porta/internal/command"
	"github.com/davidg238/porta/internal/store"
	"github.com/spf13/cobra"
)

type installOpts struct {
	CRC       int64 // 0 → compute from file
	IntervalS int64
	Triggers  []string
	Runlevel  int
	Lifecycle string
}

// runInstall reads a .bin, registers it under its CRC32-IEEE, and enqueues a run.
func runInstall(st *store.Store, id, name, path string, opts installOpts, now int64) error {
	if !strings.HasSuffix(path, ".bin") {
		return fmt.Errorf("unsupported file %q (B1 accepts only prebuilt .bin)", path)
	}
	img, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	crc := opts.CRC
	if crc == 0 {
		crc = int64(command.CRC32(img))
	}
	triggers, err := command.TriggersFromFlags(opts.Triggers, opts.IntervalS)
	if err != nil {
		return err
	}
	if len(triggers) == 0 {
		fmt.Printf("note: no triggers given — %q installed but not started\n", name)
	}
	runCmd, err := command.Run(command.RunSpec{
		Name: name, CRC: crc, Size: int64(len(img)),
		Triggers: triggers, Runlevel: opts.Runlevel, Lifecycle: opts.Lifecycle,
	})
	if err != nil {
		return err
	}
	if err := st.RegisterPayload(crc, name, img); err != nil {
		return err
	}
	cmdID, err := st.EnqueueCommand(id, runCmd.Verb, runCmd.ArgsJSON, "cli", now)
	if err != nil {
		return err
	}
	fmt.Printf("%s: registered %s@%d (%d B); enqueued run (command #%d)\n", id, name, crc, len(img), cmdID)
	return nil
}

func runUninstall(st *store.Store, id, name string, now int64) error {
	stop := command.Stop(name)
	cmdID, err := st.EnqueueCommand(id, stop.Verb, stop.ArgsJSON, "cli", now)
	if err != nil {
		return err
	}
	fmt.Printf("%s: enqueued stop %s (command #%d)\n", id, name, cmdID)
	return nil
}

func runSetPollInterval(st *store.Store, id string, secs, now int64) error {
	if err := st.SetPollInterval(id, secs); err != nil {
		return err
	}
	c := command.SetPollInterval(secs)
	_, err := st.EnqueueCommand(id, c.Verb, c.ArgsJSON, "cli", now)
	return err
}

// --- cobra wiring (attached to the parents from inspect.go) ---

func newContainerInstallCmd() *cobra.Command {
	var device string
	var opts installOpts
	var interval string
	cmd := &cobra.Command{
		Use:   "install <name> <file.bin>",
		Short: "Register a prebuilt image and enqueue run",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			id, err := resolveNodeID(st, device)
			if err != nil {
				return err
			}
			if err := st.EnsureNode(id, nowSec()); err != nil {
				return err
			}
			if interval != "" {
				if opts.IntervalS, err = command.ParseDurationSeconds(interval); err != nil {
					return err
				}
			}
			if opts.Lifecycle == "" {
				opts.Lifecycle = "run-once"
			}
			return runInstall(st, id, args[0], args[1], opts, nowSec())
		},
	}
	deviceFlag(cmd, &device)
	cmd.Flags().Int64Var(&opts.CRC, "crc", 0, "override the computed CRC32")
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
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			id, err := resolveNodeID(st, device)
			if err != nil {
				return err
			}
			st.EnsureNode(id, nowSec())
			return runUninstall(st, id, args[0], nowSec())
		},
	}
	deviceFlag(cmd, &device)
	return cmd
}

func newDeviceSetPollIntervalCmd() *cobra.Command {
	var device string
	cmd := &cobra.Command{
		Use:   "set-poll-interval <dur>",
		Short: "Enqueue a poll-interval change (and cache it)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			id, err := resolveNodeID(st, device)
			if err != nil {
				return err
			}
			st.EnsureNode(id, nowSec())
			secs, err := command.ParseDurationSeconds(args[0])
			if err != nil {
				return err
			}
			return runSetPollInterval(st, id, secs, nowSec())
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
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			id, err := resolveNodeID(st, device)
			if err != nil {
				return err
			}
			st.EnsureNode(id, nowSec())
			secs, err := command.ParseDurationSeconds(args[0])
			if err != nil {
				return err
			}
			return st.SetMaxOffline(id, secs)
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
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			id, err := resolveNodeID(st, device)
			if err != nil {
				return err
			}
			st.EnsureNode(id, nowSec())
			return st.SetNodeName(id, args[0])
		},
	}
	deviceFlag(cmd, &device)
	return cmd
}
```

Now attach these onto the parents in `internal/portacli/inspect.go`. Change `newDeviceCmd` and `newContainerCmd`:

```go
func newDeviceCmd() *cobra.Command {
	parent := &cobra.Command{Use: "device", Short: "Per-node operations"}
	parent.AddCommand(
		newDeviceShowCmd(),
		newDeviceSetPollIntervalCmd(),
		newDeviceSetMaxOfflineCmd(),
		newDeviceNameCmd(),
	)
	return parent
}

func newContainerCmd() *cobra.Command {
	parent := &cobra.Command{Use: "container", Short: "Container operations"}
	parent.AddCommand(
		newContainerListCmd(),
		newContainerInstallCmd(),
		newContainerUninstallCmd(),
	)
	return parent
}
```

- [ ] **Step 4: Run tests + full build to verify**

Run: `go test ./internal/portacli/ -v && go build ./...`
Expected: PASS + clean build.

- [ ] **Step 5: Commit**

```bash
git add internal/portacli/mutate.go internal/portacli/mutate_test.go internal/portacli/inspect.go
git commit -m "feat(porta): cli — container install/uninstall, device set-poll-interval/set-max-offline/name

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 12: Full-suite green + docs/CI refresh

**Files:**
- Modify: `CLAUDE.md` (note the new `cmd/st-devserver` binary), `.github/workflows/ci.yml` (verify; likely no change)

- [ ] **Step 1: Run the entire suite**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: PASS across every package — new porta packages AND the parked `internal/st/*` packages.

- [ ] **Step 2: Confirm CI still covers both binaries**

Read `.github/workflows/ci.yml`. It runs `go build ./...` + `go test ./...`, which already covers `cmd/porta`, `cmd/st-devserver`, and every package. No change needed unless the workflow pins specific package paths (it does not). If it does, add the new paths. Otherwise leave it.

- [ ] **Step 3: Update CLAUDE.md layout note**

In the `## Layout` section of `CLAUDE.md`, add a bullet documenting the parked binary so the next session knows where ST went:

```markdown
- `cmd/st-devserver/` + `internal/st/` — the **parked Smalltalk/berry server** (the
  former `cmd/porta`): `load:`/`dbg:*` verbs, debug UI, MCP, ST command encoding. Builds and
  keeps its tests green but is not developed further (re-enabled deliberately later).
```

- [ ] **Step 4: Commit**

```bash
git add CLAUDE.md .github/workflows/ci.yml
git commit -m "docs(porta): note parked cmd/st-devserver; confirm CI covers both binaries

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 13: Hardware checkpoint — cut jolly-pine over to the Go server

Manual end-to-end verification on real hardware. Not automated; run by the operator with agent guidance.

**Pre-req:** a prebuilt nodus `.bin` image for jolly-pine (built with the matching jag/SDK — SDK/chip coupling is the real risk per CLAUDE.md). The Toit-gateway config-self-heal soak ends here (self-heal is B2's concern).

- [ ] **Step 1: Build and start the Go server on the dev box**

```bash
go build -o /tmp/porta ./cmd/porta
/tmp/porta serve --db /tmp/porta-hw.db --port 6969
```

- [ ] **Step 2: Repoint jolly-pine to the dev box**

Update the node's `GATEWAY-HOST` to the dev box IP, reflash, and let it boot. (Per memory, jolly-pine = MAC `30aea41a6208`; the Go server will auto-name it `jolly-pine` — confirm parity.)

- [ ] **Step 3: Confirm contact**

```bash
/tmp/porta scan
# Expect jolly-pine listed, online, last-seen recent.
/tmp/porta device show -d jolly-pine
```

- [ ] **Step 4: Install + run an image, then verify it reports observed apps**

```bash
/tmp/porta container install -d jolly-pine --trigger boot --lifecycle run-once blink /path/to/blink.bin
/tmp/porta log -d jolly-pine            # run command pending → then delivered=yes after next poll
# wait for the node to poll, fetch payload, run, and report:
/tmp/porta container list -d jolly-pine # expect blink with its crc
/tmp/porta device show -d jolly-pine    # observed_state shows the running app
```

Expected end-to-end: the node drains the queue (command delivered), fetches the raw payload by CRC over TFTP, runs the image, and the next report shows the app in `container list` / observed_state. This is the B1 success criterion.

- [ ] **Step 5: Record the result**

Note the outcome (success / any divergence from the Toit gateway behavior) for the memory update. If a footgun fires (e.g. a drain/error collapse, or a delivered-but-not-run command), treat it as a `systematic-debugging` task before declaring B1 done.

---

## Self-review notes

- **Spec §1 (park ST + build porta):** Task 1 (park), Tasks 2–11 (porta core). ✓
- **Spec §2 (store schema incl. `kind` seam, defaults, data_log created):** Task 2 — schema includes `kind TEXT NOT NULL DEFAULT 'toit'`, `data_log` table + index, touch vs ensure, online rule. ✓
- **Spec §3 (TFTP resource surface — commands/payload/report, drain sentinel, mark-on-complete, WRQ rejection, data deferred):** Tasks 7 (transport) + 8 (handler). `data?id=` rejected via `AcceptWrite`. ✓
- **Spec §4 (verbs run/stop/set-poll-interval, flat encoding, scalar fidelity, trigger types, CRC32-IEEE, .bin install):** Tasks 4, 5, 6, 11. Verbs `set`→B2 / `set-console`→B3 absent by design. ✓
- **Spec §5 (report ingest — apps/config/health default {}, cache observed_state, append report, config stored-but-inert):** Task 8 `Write`. ✓
- **Spec §6 (CLI surface table):** Tasks 9 (serve), 10 (scan/ping/device show/container list/log), 11 (device set-poll-interval/set-max-offline/name, container install/uninstall). `device set`/`get`→B2, `set-console`/`monitor`→B3 absent by design. Deterministic naming ported (Task 3). ✓
- **Spec §7 (Go host tests mirroring Toit suites + REQUIRED drain regression test + HW checkpoint):** codec/crc/duration (Tasks 4–6), store CRUD (Task 2), names (Task 3), transport (Task 7), handler incl. `TestDrainVsErrorNeverCollapse` (Task 8), CLI (Tasks 9–11), HW (Task 13). ✓
- **Spec §8 (NOT in B1):** config plane, telemetry ingest/set-console/monitor, MCP/htmx, jag building, gw deployment — none implemented. ✓
- **Subtleties:** drain chokepoint returning `([]byte,error)` (Task 8 `readCommands`); mark only on transfer-complete (Task 8 `Complete`, Task 7 wiring); JSON scalar fidelity (Task 5 `EncodeWire` via `RawMessage`); touch vs ensure (Task 2); config a separate plane in observed_state (Task 8 stores config but never reconciles). ✓
- **Type consistency:** `store.Command{Verb, Args, …}`, `command.Command{Verb, ArgsJSON}`, `command.EncodeWire(verb, argsJSON)`, `tftp.Dispatcher{Read, AcceptWrite, Write, Complete}`, `handler.New(st, now)` are used consistently across tasks. The store field is `Args` (JSON string); the command package's field is `ArgsJSON`; the handler passes `cmd.Args` (store) into `command.EncodeWire`. ✓
```
