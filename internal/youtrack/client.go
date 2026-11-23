// Package youtrack implements sink.TimeSink on top of the YouTrack REST API.
//
// Work item ids handed out by this package have the form "<issue>/<itemId>"
// (for example "ABC-1/196-7") because YouTrack addresses a work item through
// its issue. Callers treat the id as opaque.
package youtrack

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/JtheGunner/ticktack/internal/sink"
)

// ErrInvalidID reports a work item id that this client cannot have issued.
// The other typed errors live in package sink.
var ErrInvalidID = errors.New("youtrack: invalid work item id")

const (
	defaultTimeout    = 30 * time.Second
	defaultMaxRetries = 3
	maxRetryAfter     = 60 * time.Second
	issuesPageSize    = 100
	// maxResponseBytes bounds how much of one response body is read.
	maxResponseBytes = 8 << 20
)

// WorkItemType is a work item type configured for a project.
type WorkItemType struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Issue is a YouTrack issue in short form.
type Issue struct {
	ID      string `json:"idReadable"`
	Summary string `json:"summary"`
	// Updated is the last update time in milliseconds since the Unix epoch.
	Updated int64 `json:"updated"`
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient replaces the default HTTP client (30 s timeout).
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		if h != nil {
			c.http = h
		}
	}
}

// WithLocation sets the time zone used to pick the instant sent for a calendar
// date. It should match the token owner's YouTrack profile time zone.
func WithLocation(loc *time.Location) Option {
	return func(c *Client) {
		if loc != nil {
			c.loc = loc
		}
	}
}

// WithBackoff overrides the wait before retry attempt n (1-based).
func WithBackoff(f func(attempt int) time.Duration) Option {
	return func(c *Client) { c.backoff = f }
}

// WithMaxRetries sets how often a 429 or 5xx response is retried. Create
// requests are only ever retried after a 429.
func WithMaxRetries(n int) Option {
	return func(c *Client) { c.maxRetries = max(n, 0) }
}

// WithLogger enables debug logging of requests (method, path, status only).
func WithLogger(l *slog.Logger) Option {
	return func(c *Client) {
		if l != nil {
			c.log = l
		}
	}
}

// Client talks to one YouTrack instance. It is safe for concurrent use.
type Client struct {
	baseURL    string
	token      string
	http       *http.Client
	loc        *time.Location
	backoff    func(attempt int) time.Duration
	sleep      func(ctx context.Context, d time.Duration) error
	maxRetries int
	log        *slog.Logger

	mu    sync.Mutex
	types map[string][]WorkItemType // by project key
	login string                    // token owner, cached by ownLogin
}

var _ sink.TimeSink = (*Client)(nil)

// New creates a client for the YouTrack instance at baseURL, authenticating
// with a permanent token.
func New(baseURL, token string, opts ...Option) (*Client, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, errors.New("youtrack: base URL is required")
	}
	if token == "" {
		return nil, errors.New("youtrack: token is required")
	}
	c := &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		token:      token,
		http:       &http.Client{Timeout: defaultTimeout},
		loc:        time.Local,
		backoff:    defaultBackoff,
		sleep:      sleepContext,
		maxRetries: defaultMaxRetries,
		log:        slog.New(slog.DiscardHandler),
		types:      map[string][]WorkItemType{},
	}
	for _, o := range opts {
		o(c)
	}
	return c, nil
}

// String never includes the token.
func (c *Client) String() string { return "youtrack.Client{" + c.baseURL + "}" }

// GoString never includes the token.
func (c *Client) GoString() string { return c.String() }

func defaultBackoff(attempt int) time.Duration {
	return min(time.Duration(1<<attempt)*250*time.Millisecond, 8*time.Second)
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Me returns the login of the token owner. It is the cheapest authenticated
// call and serves as a token check.
func (c *Client) Me(ctx context.Context) (string, error) {
	var out struct {
		Login string `json:"login"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/users/me", url.Values{"fields": {"login"}}, nil, &out); err != nil {
		return "", err
	}
	return out.Login, nil
}

// Create books w and returns its id. The work item type name is resolved to
// the project's type id first. The request is not repeated after a server or
// transport error: sink.ErrOutcomeUnclear then tells the caller that the item may
// exist and has to be looked up by its marker.
func (c *Client) Create(ctx context.Context, w sink.WorkItem) (string, error) {
	project, err := projectKey(w.Issue)
	if err != nil {
		return "", err
	}
	body := map[string]any{
		"date":     c.dateMillis(w),
		"duration": map[string]any{"minutes": w.Minutes},
		"text":     w.Text,
	}
	if w.Type != "" {
		typeID, err := c.typeID(ctx, project, w.Type)
		if err != nil {
			return "", err
		}
		body["type"] = map[string]any{"id": typeID}
	}
	var out struct {
		ID string `json:"id"`
	}
	path := "/api/issues/" + url.PathEscape(w.Issue) + "/timeTracking/workItems"
	if err := c.doCreate(ctx, path, url.Values{"fields": {"id"}}, body, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", errors.New("youtrack: create response has no id")
	}
	return w.Issue + "/" + out.ID, nil
}

// Update replaces duration, text and date of an existing work item. The type
// is left unchanged.
func (c *Client) Update(ctx context.Context, id string, w sink.WorkItem) error {
	issue, itemID, err := splitID(id)
	if err != nil {
		return err
	}
	body := map[string]any{
		"date":     c.dateMillis(w),
		"duration": map[string]any{"minutes": w.Minutes},
		"text":     w.Text,
	}
	return c.do(ctx, http.MethodPost, itemPath(issue, itemID), url.Values{"fields": {"id"}}, body, nil)
}

// Delete removes a work item.
func (c *Client) Delete(ctx context.Context, id string) error {
	issue, itemID, err := splitID(id)
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodDelete, itemPath(issue, itemID), nil, nil, nil)
}

// ownLogin returns the login of the token owner, fetched once per client.
func (c *Client) ownLogin(ctx context.Context) (string, error) {
	c.mu.Lock()
	cached := c.login
	c.mu.Unlock()
	if cached != "" {
		return cached, nil
	}
	login, err := c.Me(ctx)
	if err != nil {
		return "", err
	}
	if login == "" {
		return "", errors.New("youtrack: current user has no login")
	}
	c.mu.Lock()
	c.login = login
	c.mu.Unlock()
	return login, nil
}

// FindByMarker returns the first work item of issue authored by the token
// owner whose text contains marker verbatim. Items of other users are ignored:
// marker keys are identical for every user, and a colleague's item must never
// be adopted. YouTrack does not deduplicate, so matching happens client-side.
func (c *Client) FindByMarker(ctx context.Context, issue, marker string) (string, bool, error) {
	if marker == "" {
		return "", false, errors.New("youtrack: marker must not be empty")
	}
	if _, err := projectKey(issue); err != nil {
		return "", false, err
	}
	me, err := c.ownLogin(ctx)
	if err != nil {
		return "", false, err
	}
	type workItem struct {
		ID     string `json:"id"`
		Text   string `json:"text"`
		Author struct {
			Login string `json:"login"`
		} `json:"author"`
	}
	path := "/api/issues/" + url.PathEscape(issue) + "/timeTracking/workItems"
	q := url.Values{"fields": {"id,text,author(login)"}}
	var found string
	err = getAllPages(ctx, c, path, q, func(it workItem) string { return it.ID }, func(it workItem) bool {
		if it.Author.Login == me && strings.Contains(it.Text, marker) {
			found = issue + "/" + it.ID
			return true
		}
		return false
	})
	if err != nil {
		return "", false, err
	}
	return found, found != "", nil
}

// WorkItemTypes lists the work item types of a project (by project key).
// It returns sink.ErrTimeTrackingDisabled if time tracking is off for the project.
func (c *Client) WorkItemTypes(ctx context.Context, project string) ([]WorkItemType, error) {
	c.mu.Lock()
	cached, ok := c.types[project]
	c.mu.Unlock()
	if ok {
		return cached, nil
	}
	var settings struct {
		Enabled       bool           `json:"enabled"`
		WorkItemTypes []WorkItemType `json:"workItemTypes"`
	}
	path := "/api/admin/projects/" + url.PathEscape(project) + "/timeTrackingSettings"
	q := url.Values{"fields": {"enabled,workItemTypes(id,name)"}}
	if err := c.do(ctx, http.MethodGet, path, q, nil, &settings); err != nil {
		return nil, err
	}
	if !settings.Enabled {
		return nil, fmt.Errorf("project %s: %w", project, sink.ErrTimeTrackingDisabled)
	}
	c.mu.Lock()
	c.types[project] = settings.WorkItemTypes
	c.mu.Unlock()
	return settings.WorkItemTypes, nil
}

// MyOpenIssues returns issues assigned to the token owner in state "In Progress".
func (c *Client) MyOpenIssues(ctx context.Context) ([]Issue, error) {
	q := url.Values{
		"query":  {"for: me State: {In Progress}"},
		"fields": {"idReadable,summary,updated"},
		"$top":   {strconv.Itoa(issuesPageSize)},
	}
	var issues []Issue
	if err := c.do(ctx, http.MethodGet, "/api/issues", q, nil, &issues); err != nil {
		return nil, err
	}
	return issues, nil
}

func (c *Client) typeID(ctx context.Context, project, name string) (string, error) {
	types, err := c.WorkItemTypes(ctx, project)
	if err != nil {
		return "", err
	}
	for _, t := range types {
		if t.Name == name {
			return t.ID, nil
		}
	}
	for _, t := range types {
		if strings.EqualFold(t.Name, name) {
			return t.ID, nil
		}
	}
	return "", fmt.Errorf("type %q in project %s: %w", name, project, sink.ErrWorkItemTypeNotFound)
}

// dateMillis returns local noon of the work item's date: safely inside the
// intended day whatever zone YouTrack uses to truncate it.
func (c *Client) dateMillis(w sink.WorkItem) int64 {
	d := w.Date
	return time.Date(d.Year, d.Month, d.Day, 12, 0, 0, 0, c.loc).UnixMilli()
}

func projectKey(issue string) (string, error) {
	i := strings.LastIndex(issue, "-")
	if i <= 0 || i == len(issue)-1 {
		return "", fmt.Errorf("youtrack: invalid issue key %q", issue)
	}
	return issue[:i], nil
}

func splitID(id string) (issue, itemID string, err error) {
	issue, itemID, ok := strings.Cut(id, "/")
	if !ok || issue == "" || itemID == "" {
		return "", "", fmt.Errorf("%q: %w", id, ErrInvalidID)
	}
	return issue, itemID, nil
}

func itemPath(issue, itemID string) string {
	return "/api/issues/" + url.PathEscape(issue) + "/timeTracking/workItems/" + url.PathEscape(itemID)
}

// do performs one idempotent logical request with bounded retries on 429 and
// 5xx. Errors are derived from the status code only; 403 and 404 bodies are
// never read because a proxy replaces them with HTML.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	return c.send(ctx, method, path, query, body, out, true)
}

// doCreate performs a POST that creates an object. YouTrack offers no
// idempotency key, so the request is sent again only after a 429, which
// rejects it before anything is stored. Any failure that leaves open whether
// the object exists is returned as sink.ErrOutcomeUnclear.
func (c *Client) doCreate(ctx context.Context, path string, query url.Values, body, out any) error {
	return c.send(ctx, http.MethodPost, path, query, body, out, false)
}

func (c *Client) send(ctx context.Context, method, path string, query url.Values, body, out any, idempotent bool) error {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return fmt.Errorf("youtrack: encode request: %w", err)
		}
	}
	target := c.baseURL + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}

	for attempt := 0; ; attempt++ {
		status, retryAfter, err := c.once(ctx, method, target, payload, out)
		c.log.Debug("youtrack request", "method", method, "path", path, "status", status, "attempt", attempt+1)
		if err == nil {
			return nil
		}
		retryable := status == http.StatusTooManyRequests || (idempotent && status >= 500)
		if !retryable || attempt >= c.maxRetries {
			if !idempotent && mayHaveBeenStored(status) {
				return fmt.Errorf("%s %s: %w: %w", method, path, sink.ErrOutcomeUnclear, err)
			}
			return fmt.Errorf("%s %s: %w", method, path, err)
		}
		wait := c.backoff(attempt + 1)
		if retryAfter > 0 {
			wait = min(retryAfter, maxRetryAfter)
		}
		if serr := c.sleep(ctx, wait); serr != nil {
			return fmt.Errorf("%s %s: %w", method, path, serr)
		}
	}
}

// mayHaveBeenStored reports whether a failed request with this status can
// still have taken effect: no response at all (status 0), an unreadable
// success response, or a server or proxy error. A 4xx is a definite rejection.
func mayHaveBeenStored(status int) bool {
	return status < 300 || status >= 500
}

// once sends a single request. status is 0 if no response was received.
func (c *Client) once(ctx context.Context, method, target string, payload []byte, out any) (status int, retryAfter time.Duration, err error) {
	var rdr io.Reader
	if payload != nil {
		rdr = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, rdr)
	if err != nil {
		return 0, 0, errors.New("youtrack: build request failed")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		// Context errors stay matchable; the url.Error text holds no credentials.
		return 0, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	body := io.LimitReader(resp.Body, maxResponseBytes)

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		if out == nil {
			return resp.StatusCode, 0, nil
		}
		if err := json.NewDecoder(body).Decode(out); err != nil {
			return resp.StatusCode, 0, fmt.Errorf("youtrack: decode response: %w", err)
		}
		return resp.StatusCode, 0, nil
	case resp.StatusCode == http.StatusUnauthorized:
		return resp.StatusCode, 0, sink.ErrUnauthorized
	case resp.StatusCode == http.StatusForbidden:
		return resp.StatusCode, 0, sink.ErrForbidden
	case resp.StatusCode == http.StatusNotFound:
		return resp.StatusCode, 0, sink.ErrNotFound
	case resp.StatusCode == http.StatusTooManyRequests:
		return resp.StatusCode, parseRetryAfter(resp.Header.Get("Retry-After")), sink.ErrRateLimited
	default:
		return resp.StatusCode, 0, fmt.Errorf("youtrack: unexpected status %d", resp.StatusCode)
	}
}

func parseRetryAfter(v string) time.Duration {
	secs, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || secs <= 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}
