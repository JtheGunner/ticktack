package youtrack

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/JtheGunner/ticktack/internal/sink"
)

const (
	projectsPageSize = 100
	// markerSearchLimit bounds how many issues of a project are inspected when
	// looking for a marker; standard tasks are old and few.
	markerSearchLimit = 100
)

// Projects lists every non-archived project, paging through the admin
// projects endpoint.
func (c *Client) Projects(ctx context.Context) ([]sink.ProjectRef, error) {
	var out []sink.ProjectRef
	for skip := 0; ; skip += projectsPageSize {
		var page []struct {
			ShortName string `json:"shortName"`
			Name      string `json:"name"`
			Archived  bool   `json:"archived"`
		}
		q := url.Values{
			"fields": {"shortName,name,archived"},
			"$top":   {strconv.Itoa(projectsPageSize)},
			"$skip":  {strconv.Itoa(skip)},
		}
		if err := c.do(ctx, http.MethodGet, "/api/admin/projects", q, nil, &page); err != nil {
			return nil, err
		}
		for _, p := range page {
			if p.Archived || p.ShortName == "" {
				continue
			}
			out = append(out, sink.ProjectRef{Key: p.ShortName, Name: p.Name})
		}
		if len(page) < projectsPageSize {
			return out, nil
		}
	}
}

// FindIssueByMarker returns the oldest issue of project that the token owner
// reported and whose description contains marker verbatim. Issues of other
// users are never returned: the marker is identical for every user, and a
// colleague's issue must never be adopted as one's own standard task. The
// server-side search only narrows the candidates; the reporter and marker
// checks happen here.
func (c *Client) FindIssueByMarker(ctx context.Context, project, marker string) (string, bool, error) {
	if marker == "" {
		return "", false, errors.New("youtrack: marker must not be empty")
	}
	me, err := c.ownLogin(ctx)
	if err != nil {
		return "", false, err
	}
	q := url.Values{
		"query":  {"project: " + project + " reporter: me " + searchWords(marker) + " sort by: created asc"},
		"fields": {"idReadable,description,reporter(login)"},
		"$top":   {strconv.Itoa(markerSearchLimit)},
	}
	var issues []struct {
		ID          string `json:"idReadable"`
		Description string `json:"description"`
		Reporter    struct {
			Login string `json:"login"`
		} `json:"reporter"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/issues", q, nil, &issues); err != nil {
		return "", false, err
	}
	for _, it := range issues {
		if it.Reporter.Login == me && strings.Contains(it.Description, marker) {
			return it.ID, true, nil
		}
	}
	return "", false, nil
}

// searchWords turns a marker such as "[ticktack:default-issue]" into plain
// search terms ("ticktack default-issue") that survive YouTrack's tokenising.
func searchWords(marker string) string {
	return strings.Join(strings.FieldsFunc(marker, func(r rune) bool {
		return r == '[' || r == ']' || r == ':'
	}), " ")
}

// CreateIssue creates an issue in project assigned to the token owner and
// returns its readable key.
func (c *Client) CreateIssue(ctx context.Context, project, summary, description string) (string, error) {
	var proj struct {
		ID string `json:"id"`
	}
	path := "/api/admin/projects/" + url.PathEscape(project)
	if err := c.do(ctx, http.MethodGet, path, url.Values{"fields": {"id"}}, nil, &proj); err != nil {
		return "", err
	}
	if proj.ID == "" {
		return "", errors.New("youtrack: project has no id")
	}
	login, err := c.ownLogin(ctx)
	if err != nil {
		return "", err
	}
	body := map[string]any{
		"project":     map[string]any{"id": proj.ID},
		"summary":     summary,
		"description": description,
		"customFields": []map[string]any{{
			"name":  "Assignee",
			"$type": "SingleUserIssueCustomField",
			"value": map[string]any{"login": login},
		}},
	}
	var out struct {
		ID string `json:"idReadable"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/issues", url.Values{"fields": {"idReadable"}}, body, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", errors.New("youtrack: create issue response has no key")
	}
	return out.ID, nil
}
