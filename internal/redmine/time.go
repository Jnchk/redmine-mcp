package redmine

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

type TimeQuery struct {
	ProjectID int
	UserID    int
	IssueID   int
	From      string // YYYY-MM-DD
	To        string
	Limit     int
	Offset    int
}

func (q TimeQuery) values() url.Values {
	v := url.Values{}
	if q.ProjectID > 0 {
		v.Set("project_id", strconv.Itoa(q.ProjectID))
	}
	if q.UserID > 0 {
		v.Set("user_id", strconv.Itoa(q.UserID))
	}
	if q.IssueID > 0 {
		v.Set("issue_id", strconv.Itoa(q.IssueID))
	}
	if q.From != "" {
		v.Set("from", q.From)
	}
	if q.To != "" {
		v.Set("to", q.To)
	}
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	if q.Offset > 0 {
		v.Set("offset", strconv.Itoa(q.Offset))
	}
	return v
}

func (c *Client) TimeEntries(ctx context.Context, q TimeQuery) ([]TimeEntry, int, error) {
	var r struct {
		TimeEntries []TimeEntry `json:"time_entries"`
		TotalCount  int         `json:"total_count"`
	}
	if err := c.get(ctx, "time_entries.json", q.values(), &r); err != nil {
		return nil, 0, err
	}
	return r.TimeEntries, r.TotalCount, nil
}

func (c *Client) CreateTimeEntry(ctx context.Context, in TimeWrite) (*TimeEntry, error) {
	var r struct {
		TimeEntry *TimeEntry `json:"time_entry"`
	}
	if err := c.post(ctx, "time_entries.json", map[string]any{"time_entry": in}, &r); err != nil {
		return nil, err
	}
	if r.TimeEntry == nil {
		return nil, fmt.Errorf("redmine: empty response creating time entry")
	}
	return r.TimeEntry, nil
}
