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
