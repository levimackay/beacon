// Package cliclient is a thin HTTP client over the Beacon hub API, shared by
// the CLI and by anything else that needs to talk to a local hub from Go.
package cliclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/levimackay/beacon/internal/config"
	"github.com/levimackay/beacon/internal/protocol"
)

// ErrUnauthorized means the hub rejected our token.
var ErrUnauthorized = errors.New("the hub rejected this token")

// ErrUnreachable means we could not open a connection to the hub at all.
var ErrUnreachable = errors.New("hub unreachable")

// Client talks to a Beacon hub.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
	// LastLatency is the round-trip time of the most recent request.
	LastLatency time.Duration
}

// New builds a client from resolved configuration.
func New(c *config.Config) *Client {
	return &Client{
		BaseURL: c.BaseURL(),
		Token:   c.Token,
		HTTP:    &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encoding request: %w", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rdr)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	start := time.Now()
	resp, err := c.HTTP.Do(req)
	c.LastLatency = time.Since(start)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrUnreachable, c.BaseURL)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return ErrUnauthorized
	case resp.StatusCode == http.StatusTooManyRequests:
		return errors.New("rate limited by the hub")
	case resp.StatusCode >= 400:
		return fmt.Errorf("hub returned %s: %s", resp.Status, readError(resp.Body))
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	return nil
}

// readError pulls a short message out of an error body without letting a
// hostile or broken server flood the terminal.
func readError(r io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(r, 2048))
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(b, &e) == nil && e.Error != "" {
		return e.Error
	}
	return string(bytes.TrimSpace(b))
}

// Snapshot fetches the aggregate health view.
func (c *Client) Snapshot(ctx context.Context) (protocol.Snapshot, error) {
	var s protocol.Snapshot
	err := c.do(ctx, http.MethodGet, "/v1/snapshot", nil, &s)
	return s, err
}

// Targets lists everything the hub is watching.
func (c *Client) Targets(ctx context.Context) ([]protocol.Target, error) {
	var t []protocol.Target
	err := c.do(ctx, http.MethodGet, "/v1/targets", nil, &t)
	return t, err
}

// AddTarget creates or updates a target.
func (c *Client) AddTarget(ctx context.Context, t protocol.Target) error {
	return c.do(ctx, http.MethodPost, "/v1/targets", t, nil)
}

// DeleteTarget removes a target and its history.
func (c *Client) DeleteTarget(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/targets/"+url.PathEscape(id), nil, nil)
}

// Incidents lists incidents since the given time, newest first.
func (c *Client) Incidents(ctx context.Context, since time.Time, limit int) ([]protocol.Incident, error) {
	q := url.Values{}
	if !since.IsZero() {
		q.Set("since", since.UTC().Format(time.RFC3339))
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var in []protocol.Incident
	err := c.do(ctx, http.MethodGet, "/v1/incidents?"+q.Encode(), nil, &in)
	return in, err
}

// Diagnostics fetches the troubleshooting view and stamps the client-measured
// API latency onto it.
func (c *Client) Diagnostics(ctx context.Context) (protocol.Diagnostics, error) {
	var d protocol.Diagnostics
	err := c.do(ctx, http.MethodGet, "/v1/diagnostics", nil, &d)
	d.APILatencyMS = float64(c.LastLatency.Microseconds()) / 1000
	return d, err
}

// Raw fetches a path and returns the undecoded body, for `--json`.
func (c *Client) Raw(ctx context.Context, path string) ([]byte, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return nil, err
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		return raw, nil
	}
	return pretty.Bytes(), nil
}
