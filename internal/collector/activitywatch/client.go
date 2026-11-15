// Package activitywatch imports window and AFK events from a local
// ActivityWatch server and maps them to ticktack presence, gap and context
// events. It is strictly read-only: it only issues GET requests.
//
// Privacy carve-out: ticktack's only network call is the YouTrack sync, with
// one exception. This optional importer may talk to a loopback ActivityWatch
// server (localhost, 127.0.0.0/8 or ::1) and to nothing else. The rule is
// enforced three times: the URL is validated at config load and at client
// construction, redirects to non-loopback hosts are refused, and the dialer
// refuses every non-loopback IP address, so even a name that resolves
// elsewhere cannot connect.
//
// Window titles are never decoded: the event data struct has no title field,
// so a title never reaches an event, the store or a log line. Only the app
// name of a window event is kept.
//
// Idempotency: every event carries a deterministic Key built from bucket,
// timestamp and kind, and the store keeps at most one event per key.
// Presence samples sit on a fixed grid (multiples of the sample interval), so
// the samples of an AFK interval that ActivityWatch extends by heartbeat keep
// their keys. Re-importing any range, including overlapping ones, adds
// nothing new. The cursor is derived from the newest imported event of
// Source; see the importing command.
package activitywatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/JtheGunner/ticktack/internal/loopback"
)

const (
	// requestTimeout bounds every single request.
	requestTimeout = 15 * time.Second
	// maxBodyBytes caps a response so a misbehaving server cannot exhaust memory.
	maxBodyBytes = 32 << 20
	maxRedirects = 3
)

// Errors returned by Client.
var (
	// ErrUnreachable means the server could not be contacted.
	ErrUnreachable = errors.New("activitywatch server not reachable")
	// ErrBadResponse means the server answered with an unusable response.
	ErrBadResponse = errors.New("activitywatch sent an unusable response")
)

// Bucket describes an ActivityWatch bucket.
type Bucket struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Hostname string `json:"hostname"`
}

// RawEvent is an ActivityWatch event reduced to the fields ticktack may use.
// Titles are deliberately absent.
type RawEvent struct {
	Timestamp time.Time `json:"timestamp"`
	// Duration is in seconds.
	Duration float64 `json:"duration"`
	Data     struct {
		Status string `json:"status"`
		App    string `json:"app"`
	} `json:"data"`
}

// Client is a read-only ActivityWatch REST v0 client bound to a loopback URL.
type Client struct {
	base *url.URL
	http *http.Client
}

// Option customises a Client.
type Option func(*Client)

// WithHTTPClient replaces the HTTP client, for tests. The redirect policy is
// still enforced on a copy; the loopback dialer is the caller's concern.
func WithHTTPClient(c *http.Client) Option {
	return func(cl *Client) {
		clone := *c
		cl.http = &clone
	}
}

// New builds a client for rawURL, which must point at a loopback host.
func New(rawURL string, opts ...Option) (*Client, error) {
	base, err := loopback.CheckURL(rawURL)
	if err != nil {
		return nil, fmt.Errorf("activitywatch url: %w", err)
	}
	c := &Client{base: base}
	for _, opt := range opts {
		opt(c)
	}
	if c.http == nil {
		c.http = &http.Client{Transport: &http.Transport{
			// No proxy: loopback traffic must never leave the machine.
			Proxy:       nil,
			DialContext: (&net.Dialer{Timeout: 5 * time.Second, Control: loopback.Control}).DialContext,
		}}
	}
	c.http.CheckRedirect = checkRedirect
	return c, nil
}

func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return errors.New("too many redirects")
	}
	if _, err := loopback.CheckURL(req.URL.String()); err != nil {
		return fmt.Errorf("redirect refused: %w", err)
	}
	return nil
}

// Buckets lists all buckets of the server.
func (c *Client) Buckets(ctx context.Context) ([]Bucket, error) {
	var byID map[string]Bucket
	if err := c.get(ctx, "/api/0/buckets/", nil, &byID); err != nil {
		return nil, err
	}
	out := make([]Bucket, 0, len(byID))
	for id, b := range byID {
		b.ID = id
		out = append(out, b)
	}
	return out, nil
}

// Events returns up to limit events of a bucket overlapping [start, end],
// newest first (the server's order).
func (c *Client) Events(ctx context.Context, bucketID string, start, end time.Time, limit int) ([]RawEvent, error) {
	q := url.Values{}
	q.Set("start", start.UTC().Format(time.RFC3339Nano))
	q.Set("end", end.UTC().Format(time.RFC3339Nano))
	q.Set("limit", strconv.Itoa(limit))
	var events []RawEvent
	path := "/api/0/buckets/" + url.PathEscape(bucketID) + "/events"
	if err := c.get(ctx, path, q, &events); err != nil {
		return nil, err
	}
	return events, nil
}

// get performs a time-boxed GET and decodes the size-limited JSON body. Error
// text carries the path and status only, never the response body.
func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	u := c.base.JoinPath(path)
	u.RawPath = ""
	if query != nil {
		u.RawQuery = query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return fmt.Errorf("%w: build request for %s: %w", ErrBadResponse, path, err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: GET %s: %w", ErrUnreachable, path, urlErrorCause(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: GET %s: HTTP %d", ErrBadResponse, path, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return fmt.Errorf("%w: GET %s: read body: %w", ErrBadResponse, path, urlErrorCause(err))
	}
	if len(body) > maxBodyBytes {
		return fmt.Errorf("%w: GET %s: response larger than %d bytes", ErrBadResponse, path, maxBodyBytes)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%w: GET %s: malformed JSON", ErrBadResponse, path)
	}
	return nil
}

// urlErrorCause drops the request URL that *url.Error repeats, keeping the
// underlying cause.
func urlErrorCause(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}
