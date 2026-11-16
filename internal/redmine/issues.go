package redmine

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

// IssueQuery mirrors the issue list filters we expose.
type IssueQuery struct {
	ProjectID    int
	StatusID     string // open, closed, *, or a numeric id
	AssignedToID int
	TrackerID    int
	PriorityID   int
	Subject      string // substring match on subject only
	UpdatedSince string // YYYY-MM-DD
	QueryID      int    // saved Redmine query; other filters are then ignored server-side
	Sort         string
	Limit        int
	Offset       int
}

func (q IssueQuery) values() url.Values {
	v := url.Values{}
	if q.ProjectID > 0 {
		v.Set("project_id", strconv.Itoa(q.ProjectID))
	}
	if q.StatusID != "" {
		v.Set("status_id", q.StatusID)
	}
	if q.AssignedToID > 0 {
		v.Set("assigned_to_id", strconv.Itoa(q.AssignedToID))
	}
	if q.TrackerID > 0 {
		v.Set("tracker_id", strconv.Itoa(q.TrackerID))
	}
	if q.PriorityID > 0 {
		v.Set("priority_id", strconv.Itoa(q.PriorityID))
	}
	if q.Subject != "" {
		v.Set("subject", "~"+q.Subject)
	}
	if q.UpdatedSince != "" {
		v.Set("updated_on", ">="+q.UpdatedSince)
	}
	if q.QueryID > 0 {
		v.Set("query_id", strconv.Itoa(q.QueryID))
	}
	if q.Sort != "" {
		v.Set("sort", q.Sort)
	}
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	if q.Offset > 0 {
		v.Set("offset", strconv.Itoa(q.Offset))
	}
	return v
}

func (c *Client) Issues(ctx context.Context, q IssueQuery) ([]Issue, int, error) {
	var r struct {
		Issues     []Issue `json:"issues"`
		TotalCount int     `json:"total_count"`
	}
	if err := c.get(ctx, "issues.json", q.values(), &r); err != nil {
		return nil, 0, err
	}
	return r.Issues, r.TotalCount, nil
}

func (c *Client) IssuesRaw(ctx context.Context, q IssueQuery) ([]byte, error) {
	return c.GetRaw(ctx, "issues.json", q.values())
}

func (c *Client) Issue(ctx context.Context, id int) (*Issue, error) {
	q := url.Values{"include": {"journals,attachments,relations,children"}}
	var r struct {
		Issue *Issue `json:"issue"`
	}
	if err := c.get(ctx, fmt.Sprintf("issues/%d.json", id), q, &r); err != nil {
		return nil, err
	}
	if r.Issue == nil {
		return nil, fmt.Errorf("redmine: empty response for issue #%d", id)
	}
	return r.Issue, nil
}

func (c *Client) IssueRaw(ctx context.Context, id int) ([]byte, error) {
	q := url.Values{"include": {"journals,attachments,relations,children"}}
	return c.GetRaw(ctx, fmt.Sprintf("issues/%d.json", id), q)
}

func (c *Client) CreateIssue(ctx context.Context, in IssueWrite) (*Issue, error) {
	var r struct {
		Issue *Issue `json:"issue"`
	}
	if err := c.post(ctx, "issues.json", map[string]any{"issue": in}, &r); err != nil {
		return nil, err
	}
	if r.Issue == nil {
		return nil, fmt.Errorf("redmine: empty response creating issue")
	}
	return r.Issue, nil
}

// UpdateIssue returns no body: Redmine answers 204 No Content.
func (c *Client) UpdateIssue(ctx context.Context, id int, in IssueWrite) error {
	return c.put(ctx, fmt.Sprintf("issues/%d.json", id), map[string]any{"issue": in}, nil)
}

func (c *Client) SearchIssues(ctx context.Context, q string, limit int) ([]SearchResult, int, error) {
	v := url.Values{"q": {q}, "issues": {"1"}, "scope": {"all"}}
	if limit > 0 {
		v.Set("limit", strconv.Itoa(limit))
	}
	var r struct {
		Results    []SearchResult `json:"results"`
		TotalCount int            `json:"total_count"`
	}
	if err := c.get(ctx, "search.json", v, &r); err != nil {
		return nil, 0, err
	}
	return r.Results, r.TotalCount, nil
}
