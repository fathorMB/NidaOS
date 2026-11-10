// Package client calls the API of the core service. The nida command
// uses it. It connects through the local socket (decision D18) or through
// HTTP with a token (decision D17).
package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultSocket is the local socket of the core service.
const DefaultSocket = "/run/nidaos/core.sock"

// Client calls the core service.
type Client struct {
	// Base is the start of each URL, for example "http://nida.local:8470".
	Base  string
	Token string
	HTTP  *http.Client
	// Stream is the client of the live streams: it has no timeout.
	Stream *http.Client
}

// NewSocket makes a client for the local socket.
func NewSocket(path string) *Client {
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", path)
	}
	return &Client{
		Base:   "http://local",
		HTTP:   &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{DialContext: dial}},
		Stream: &http.Client{Transport: &http.Transport{DialContext: dial}},
	}
}

// NewHTTP makes a client for a server address and a token.
func NewHTTP(base, token string) *Client {
	return &Client{
		Base:   strings.TrimRight(base, "/"),
		Token:  token,
		HTTP:   &http.Client{Timeout: 30 * time.Second},
		Stream: &http.Client{},
	}
}

// APIError is an error response of the service.
type APIError struct {
	Status  int
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("the service answered %d", e.Status)
	}
	return e.Message
}

func (c *Client) newRequest(ctx context.Context, method, path string, body any) (*http.Request, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	return req, nil
}

// Do sends a request and decodes the JSON response into out (nil: no body).
func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
	req, err := c.newRequest(ctx, method, path, body)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return connError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return readError(resp)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Get is Do with GET.
func (c *Client) Get(ctx context.Context, path string, q url.Values, out any) error {
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	return c.Do(ctx, http.MethodGet, path, nil, out)
}

func readError(resp *http.Response) error {
	e := &APIError{Status: resp.StatusCode}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	_ = json.Unmarshal(b, e)
	return e
}

// connError makes the usual connection errors easy to read.
func connError(err error) error {
	var op *net.OpError
	if errors.As(err, &op) {
		if op.Net == "unix" {
			return fmt.Errorf("cannot open the local socket of nida-core (%v). Is the service on? Are you root or in the group nida? From another computer, use --server and NIDA_TOKEN", op.Err)
		}
		return fmt.Errorf("cannot connect to nida-core: %v", op.Err)
	}
	return err
}

// Event is one event of a stream.
type Event struct {
	ID   string
	Name string
	Data []byte
}

// ErrStreamEnd tells that the service closed the stream.
var ErrStreamEnd = errors.New("the stream stopped")

// Subscribe opens a stream and calls fn for each event until ctx stops,
// the stream stops, or fn gives an error. lastID is the value of the
// header Last-Event-ID (empty: none).
func (c *Client) Subscribe(ctx context.Context, path string, q url.Values, lastID string, fn func(Event) error) error {
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	req, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	resp, err := c.Stream.Do(req)
	if err != nil {
		return connError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return readError(resp)
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	var ev Event
	var data bytes.Buffer
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if ev.Name != "" || data.Len() > 0 {
				if ev.Name == "" {
					ev.Name = "message"
				}
				ev.Data = append([]byte(nil), data.Bytes()...)
				if err := fn(ev); err != nil {
					return err
				}
			}
			ev, data = Event{}, bytes.Buffer{}
		case strings.HasPrefix(line, ":"):
			// A comment: the heartbeat of the service.
		default:
			field, value, _ := strings.Cut(line, ":")
			value = strings.TrimPrefix(value, " ")
			switch field {
			case "id":
				ev.ID = value
			case "event":
				ev.Name = value
			case "data":
				if data.Len() > 0 {
					data.WriteByte('\n')
				}
				data.WriteString(value)
			}
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("%w: %v", ErrStreamEnd, err)
	}
	return ErrStreamEnd
}
