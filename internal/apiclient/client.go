// Package apiclient is the write-side HTTP client for the porta control-plane
// API (internal/apisrv). It is cobra-free and store-free: the CLI's mutating
// commands use it to POST/PATCH the server instead of opening the store, which
// keeps the server the single writer (one trustworthy audit trail).
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
