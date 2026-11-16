package tools

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"redmine-mcp/internal/format"
	"redmine-mcp/internal/redmine"
)

type logIn struct {
	IssueID  int     `json:"issue_id" jsonschema:"issue id (required)"`
	Hours    float64 `json:"hours" jsonschema:"hours spent, e.g. 1.5 (required)"`
	Activity string  `json:"activity,omitempty" jsonschema:"activity name or id; omit for the server default"`
	Comments string  `json:"comments,omitempty" jsonschema:"what the time was spent on"`
	SpentOn  string  `json:"spent_on,omitempty" jsonschema:"YYYY-MM-DD; defaults to today"`
}

func (d *Deps) logTime(ctx context.Context, in logIn) (string, error) {
	if err := d.guardWrite(); err != nil {
		return "", err
	}
	if in.IssueID <= 0 {
		return "", fmt.Errorf("issue_id is required")
	}
	if in.Hours <= 0 {
		return "", fmt.Errorf("hours must be greater than 0")
	}

	activity := ""
	var activityID *int
	if in.Activity != "" {
		n, err := d.M.Resolve(ctx, redmine.KActivity, in.Activity)
		if err != nil {
			return "", err
		}
		activity, activityID = nameOr(n, in.Activity), &n.ID
	} else if as, err := d.M.Activities(ctx); err == nil && len(as) > 0 {
		pick := as[0]
		for _, a := range as {
			if a.IsDefault {
				pick = a
				break
			}
		}
		activity, activityID = pick.Name, &pick.ID
	}

	t, err := d.C.CreateTimeEntry(ctx, redmine.TimeWrite{
		IssueID:    &in.IssueID,
		SpentOn:    in.SpentOn,
		Hours:      in.Hours,
		ActivityID: activityID,
		Comments:   in.Comments,
	})
	if err != nil {
		return "", err
	}

	on := t.SpentOn
	if on == "" {
		on = in.SpentOn
	}
	if on == "" {
		on = "today"
	}
	act := activity
	if act == "" && t.Activity != nil {
		act = t.Activity.Name
	}
	if act == "" {
		act = "default activity"
	}
	return fmt.Sprintf("#%d logged %sh on %s (%s). %s",
		in.IssueID, format.Num(t.Hours), on, act, d.issueURL(in.IssueID)), nil
}

type timeIn struct {
	Project string `json:"project,omitempty" jsonschema:"project name, identifier or id"`
	User    string `json:"user,omitempty" jsonschema:"user name, login, id, or me; omit for your own entries"`
	IssueID int    `json:"issue_id,omitempty"`
	From    string `json:"from,omitempty" jsonschema:"YYYY-MM-DD"`
	To      string `json:"to,omitempty" jsonschema:"YYYY-MM-DD"`
	Limit   int    `json:"limit,omitempty" jsonschema:"page size, 1-100 (default 25)"`
	Offset  int    `json:"offset,omitempty"`
}

func (d *Deps) listTimeEntries(ctx context.Context, in timeIn) (string, error) {
	q := redmine.TimeQuery{
		IssueID: in.IssueID,
		From:    in.From,
		To:      in.To,
		Limit:   d.limit(in.Limit),
		Offset:  in.Offset,
	}

	projectName := ""
	if in.Project != "" {
		n, err := d.M.Resolve(ctx, redmine.KProject, in.Project)
		if err != nil {
			return "", err
		}
		q.ProjectID, projectName = n.ID, nameOr(n, in.Project)
	}
	userName := ""
	if in.User != "" {
		n, err := d.M.Resolve(ctx, redmine.KUser, in.User)
		if err != nil {
			return "", err
		}
		q.UserID, userName = n.ID, nameOr(n, in.User)
	}

	entries, total, err := d.C.TimeEntries(ctx, q)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		msg := "no time entries matched."
		if in.User == "" {
			if u, err := d.C.CurrentUser(ctx); err == nil {
				msg += fmt.Sprintf(" Only entries visible to %s; pass user to query someone else.", u.Login)
			}
		} else {
			msg += " User filter: " + userName + "."
		}
		return msg, nil
	}

	var b strings.Builder
	b.WriteString("# id | spent_on | user | activity | hours | issue | comments\n")
	for _, e := range entries {
		issue := ""
		if e.Issue != nil {
			issue = "#" + strconv.Itoa(e.Issue.ID)
		}
		b.WriteString(format.Row(
			strconv.Itoa(e.ID),
			e.SpentOn,
			refName(e.User),
			refName(e.Activity),
			format.Num(e.Hours),
			issue,
			format.Truncate(e.Comments, 100),
		) + "\n")
	}
	fmt.Fprintf(&b, "# returned %d of %d (offset %d, limit %d)\n", len(entries), total, q.Offset, q.Limit)
	if in.User == "" {
		if u, err := d.C.CurrentUser(ctx); err == nil {
			fmt.Fprintf(&b, "# only entries visible to %s; pass user to query someone else\n", u.Login)
		}
	} else {
		fmt.Fprintf(&b, "# filtered by user %s\n", userName)
	}
	if projectName != "" {
		fmt.Fprintf(&b, "# filtered by project %s\n", projectName)
	}
	return b.String(), nil
}
