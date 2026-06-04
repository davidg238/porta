// Copyright (c) 2026 Ekorau LLC

// Package apiclient is the HTTP client for the porta control-plane API
// (internal/apisrv). It is cobra-free and store-free: the CLI's mutating
// commands use it to POST/PATCH the server instead of opening the store, which
// keeps the server the single writer (one trustworthy audit trail). It also
// carries one narrow read — NodeIdentity — for `porta run`'s SDK guard.
package apiclient

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
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
			return nil, errors.New(env.Error)
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

// DataRow is one telemetry row returned by the telemetry reads. Value is the
// typed scalar reconstructed from value_type: int64 for int/bool, float64 for
// float, nil for string & log rows (their payload is in Text).
type DataRow struct {
	ID        int64
	TS        int64
	Seq       int64
	Kind      string
	Name      string
	Value     any
	Text      string
	ValueType string
}

// wireRow is the on-the-wire shape; Value stays raw so typedValue can coerce it
// by value_type without losing int64 precision through a float.
type wireRow struct {
	ID        int64           `json:"id"`
	TS        int64           `json:"ts"`
	Seq       int64           `json:"seq"`
	Kind      string          `json:"kind"`
	Name      string          `json:"name"`
	Value     json.RawMessage `json:"value"`
	Text      string          `json:"text"`
	ValueType string          `json:"value_type"`
}

// typedValue coerces a raw JSON value to the Go type FormatLine expects for the
// given value_type: int64 for int/bool (falling back to float64 if the value
// arrived non-integer-shaped), float64 for float, nil otherwise (a JSON null,
// a string row, or an unknown tag — the payload then lives in Text).
func typedValue(valueType string, raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	switch valueType {
	case "float":
		var f float64
		if json.Unmarshal(raw, &f) == nil {
			return f
		}
	case "int", "bool":
		var i int64
		if json.Unmarshal(raw, &i) == nil {
			return i
		}
		var f float64
		if json.Unmarshal(raw, &f) == nil {
			return f
		}
	}
	return nil
}

// QueryTelemetryWindow reads the ts window [since, until] (until<=0 = unbounded)
// for sel, optionally filtered by kind and capped by limit. Used for monitor's
// initial look-back.
func (c *Client) QueryTelemetryWindow(sel string, since, until int64, kind string, limit int) ([]DataRow, error) {
	q := url.Values{}
	q.Set("since", strconv.FormatInt(since, 10))
	if until > 0 {
		q.Set("until", strconv.FormatInt(until, 10))
	}
	if kind != "" {
		q.Set("kind", kind)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	return c.getTelemetry(sel, q)
}

// QueryTelemetryAfter tails rows with id > after (ordered by id) for sel. Used
// for monitor --follow polls: exact dedup, no timestamp boundary case.
func (c *Client) QueryTelemetryAfter(sel string, after int64, kind string, limit int) ([]DataRow, error) {
	q := url.Values{}
	q.Set("after", strconv.FormatInt(after, 10))
	if kind != "" {
		q.Set("kind", kind)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	return c.getTelemetry(sel, q)
}

// getTelemetry GETs /api/nodes/{sel}/telemetry with q and decodes the rows.
func (c *Client) getTelemetry(sel string, q url.Values) ([]DataRow, error) {
	u := c.baseURL + "/api/nodes/" + url.PathEscape(sel) + "/telemetry"
	if enc := q.Encode(); enc != "" {
		u += "?" + enc
	}
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	data, err := c.do(req)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Rows []wireRow `json:"rows"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	out := make([]DataRow, 0, len(resp.Rows))
	for _, w := range resp.Rows {
		out = append(out, DataRow{
			ID: w.ID, TS: w.TS, Seq: w.Seq, Kind: w.Kind, Name: w.Name,
			Value: typedValue(w.ValueType, w.Value), Text: w.Text, ValueType: w.ValueType,
		})
	}
	return out, nil
}

// identityResp decodes just the chip/sdk fields of a GET /api/nodes/{sel} detail.
type identityResp struct {
	Chip string `json:"chip"`
	Sdk  string `json:"sdk"`
}

// NodeListItem is one row of ListNodes (GET /api/nodes). LastSeen is 0 when the
// node has never been heard from (matches the store's never-seen sentinel).
type NodeListItem struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	IP       string `json:"ip"`
	LastSeen int64  `json:"last_seen"`
	Online   bool   `json:"online"`
	Chip     string `json:"chip"`
	Sdk      string `json:"sdk"`
}

// ListNodes fetches the fleet list (GET /api/nodes).
func (c *Client) ListNodes() ([]NodeListItem, error) {
	req, err := http.NewRequest("GET", c.baseURL+"/api/nodes", nil)
	if err != nil {
		return nil, err
	}
	data, err := c.do(req)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Nodes []NodeListItem `json:"nodes"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	return resp.Nodes, nil
}

// NodeApp is one observed-apps entry in a node detail.
type NodeApp struct {
	Name     string `json:"Name"`
	CRC      int64  `json:"CRC"`
	Runlevel int64  `json:"Runlevel"`
}

// NodeDetailResp is the full node detail (GET /api/nodes/{sel}). Online and the
// relative-age inputs (LastSeen) come from the server; ObservedRaw + Undelivered
// back `porta device show`.
type NodeDetailResp struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Kind          string    `json:"kind"`
	IP            string    `json:"ip"`
	Online        bool      `json:"online"`
	Chip          string    `json:"chip"`
	Sdk           string    `json:"sdk"`
	Reset         string    `json:"reset"`
	ResetCode     *int64    `json:"reset_code"`
	PollIntervalS int64     `json:"poll_interval_s"`
	MaxOfflineS   int64     `json:"max_offline_s"`
	LastSeen      int64     `json:"last_seen"`
	LastReportAt  int64     `json:"last_report_at"`
	Apps          []NodeApp `json:"apps"`
	ObservedRaw   string    `json:"observed_raw"`
	Undelivered   int       `json:"undelivered"`
}

// NodeDetail fetches one node's full detail (GET /api/nodes/{sel}).
func (c *Client) NodeDetail(sel string) (*NodeDetailResp, error) {
	req, err := http.NewRequest("GET", c.baseURL+"/api/nodes/"+url.PathEscape(sel), nil)
	if err != nil {
		return nil, err
	}
	data, err := c.do(req)
	if err != nil {
		return nil, err
	}
	var r NodeDetailResp
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// CommandLogItem is one row of NodeCommands (GET /api/nodes/{sel}/commands).
// Delivered says whether the command has been picked up by the node.
type CommandLogItem struct {
	ID        int64  `json:"id"`
	Verb      string `json:"verb"`
	Args      string `json:"args"`
	IssuedAt  int64  `json:"issued_at"`
	IssuedBy  string `json:"issued_by"`
	Delivered bool   `json:"delivered"`
}

// NodeCommands fetches the recent command log for sel (GET /api/nodes/{sel}/commands).
// The server returns newest-first (capped); callers that need oldest-first must
// reverse.
func (c *Client) NodeCommands(sel string) ([]CommandLogItem, error) {
	req, err := http.NewRequest("GET", c.baseURL+"/api/nodes/"+url.PathEscape(sel)+"/commands", nil)
	if err != nil {
		return nil, err
	}
	data, err := c.do(req)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Commands []CommandLogItem `json:"commands"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	return resp.Commands, nil
}

// ConfigRow is one desired-vs-observed config row (GET /api/nodes/{sel}/config).
// It mirrors control.ConfigRow's exported fields; the *Present flags say whether
// each side carried the key.
type ConfigRow struct {
	Key             string `json:"key"`
	Desired         any    `json:"desired"`
	Observed        any    `json:"observed"`
	DesiredPresent  bool   `json:"desired_present"`
	ObservedPresent bool   `json:"observed_present"`
	Marker          string `json:"marker"`
	ReissueCount    int    `json:"reissue_count"`
}

// Config fetches the desired-vs-observed config rows for app on sel
// (GET /api/nodes/{sel}/config?app=<app>), backing `porta device get`.
func (c *Client) Config(sel, app string) ([]ConfigRow, error) {
	u := c.baseURL + "/api/nodes/" + url.PathEscape(sel) + "/config?app=" + url.QueryEscape(app)
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	data, err := c.do(req)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Config []ConfigRow `json:"config"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	return resp.Config, nil
}

// NodeIdentity fetches the node's reported chip/sdk (GET /api/nodes/{sel}), for
// `porta run`'s SDK guard. The full node-detail read stays deferred (S2). A node
// that exists but hasn't reported yet returns ("", "", nil); an unknown node
// surfaces the server's 404 error string. Other detail fields are ignored.
func (c *Client) NodeIdentity(sel string) (chip, sdk string, err error) {
	req, err := http.NewRequest("GET", c.baseURL+"/api/nodes/"+url.PathEscape(sel), nil)
	if err != nil {
		return "", "", err
	}
	data, err := c.do(req)
	if err != nil {
		return "", "", err
	}
	var r identityResp
	if err := json.Unmarshal(data, &r); err != nil {
		return "", "", err
	}
	return r.Chip, r.Sdk, nil
}
