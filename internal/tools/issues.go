package tools

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"redmine-mcp/internal/format"
	"redmine-mcp/internal/redmine"
	"redmine-mcp/internal/upload"
)

type searchIn struct {
	Project      string `json:"project,omitempty" jsonschema:"project name, identifier or id"`
	Status       string `json:"status,omitempty" jsonschema:"open (default), closed, all, or a status name/id"`
	Assignee     string `json:"assignee,omitempty" jsonschema:"user name, login, id, or me"`
	Tracker      string `json:"tracker,omitempty" jsonschema:"tracker name or id"`
	Priority     string `json:"priority,omitempty" jsonschema:"priority name or id"`
	Subject      string `json:"subject,omitempty" jsonschema:"substring filter on the subject only"`
	UpdatedSince string `json:"updated_since,omitempty" jsonschema:"only issues updated on/after this date, YYYY-MM-DD"`
	QueryID      int    `json:"query_id,omitempty" jsonschema:"Redmine saved query id; when set, the server ignores the other filters"`
	Sort         string `json:"sort,omitempty" jsonschema:"e.g. updated_on:desc (default), priority:desc"`
	Q            string `json:"q,omitempty" jsonschema:"full-text search over subject, description and notes; cannot be combined with other filters"`
	Limit        int    `json:"limit,omitempty" jsonschema:"page size, 1-100 (default 25)"`
	Offset       int    `json:"offset,omitempty" jsonschema:"pagination offset"`
	Raw          bool   `json:"raw,omitempty" jsonschema:"return the untouched Redmine JSON instead of the compact table"`
}

func (d *Deps) searchIssues(ctx context.Context, in searchIn) (string, error) {
	if in.Q != "" {
		if in.Project != "" || in.Status != "" || in.Assignee != "" || in.Tracker != "" ||
			in.Priority != "" || in.Subject != "" || in.UpdatedSince != "" || in.QueryID > 0 {
			return "", fmt.Errorf("q (full-text search) cannot be combined with the other filters: it uses a different Redmine endpoint")
		}
		limit := d.limit(in.Limit)
		results, total, err := d.C.SearchIssues(ctx, in.Q, limit)
		if err != nil {
			return "", err
		}
		if len(results) == 0 {
			return fmt.Sprintf("no issues matched %q. Full-text search covers subject, description and notes; try different words.", in.Q), nil
		}
		var b strings.Builder
		b.WriteString("# id | title | snippet\n")
		for _, r := range results {
			b.WriteString(format.Row(strconv.Itoa(r.ID), r.Title, format.Truncate(r.Description, 120)) + "\n")
		}
		fmt.Fprintf(&b, "# returned %d of %d (limit %d)\n", len(results), total, limit)
		return b.String(), nil
	}

	q := redmine.IssueQuery{
		Subject:      in.Subject,
		UpdatedSince: in.UpdatedSince,
		QueryID:      in.QueryID,
		Sort:         in.Sort,
		Limit:        d.limit(in.Limit),
		Offset:       in.Offset,
	}
	if q.Sort == "" {
		q.Sort = "updated_on:desc"
	}

	var err error
	if in.Project != "" {
		var p redmine.Named
		if p, err = d.M.Resolve(ctx, redmine.KProject, in.Project); err != nil {
			return "", err
		}
		q.ProjectID = p.ID
	}
	if in.Tracker != "" {
		var t redmine.Named
		if t, err = d.M.Resolve(ctx, redmine.KTracker, in.Tracker); err != nil {
			return "", err
		}
		q.TrackerID = t.ID
	}
	if in.Priority != "" {
		var p redmine.Named
		if p, err = d.M.Resolve(ctx, redmine.KPriority, in.Priority); err != nil {
			return "", err
		}
		q.PriorityID = p.ID
	}
	if in.Assignee != "" {
		var a redmine.Named
		if a, err = d.M.Resolve(ctx, redmine.KUser, in.Assignee); err != nil {
			return "", err
		}
		q.AssignedToID = a.ID
	}
	if q.StatusID, err = d.statusParam(ctx, in.Status); err != nil {
		return "", err
	}

	if in.Raw {
		data, err := d.C.IssuesRaw(ctx, q)
		if err != nil {
			return "", err
		}
		return rawClip(data), nil
	}

	issues, total, err := d.C.Issues(ctx, q)
	if err != nil {
		return "", err
	}
	if len(issues) == 0 {
		status := in.Status
		if status == "" {
			status = "open"
		}
		return fmt.Sprintf("no issues matched (status=%s). Widen the filters or use q for full-text search.", status), nil
	}

	var b strings.Builder
	b.WriteString("# id | status | tracker | priority | assignee | updated | subject\n")
	for _, is := range issues {
		b.WriteString(format.Row(
			strconv.Itoa(is.ID),
			refName(is.Status),
			refName(is.Tracker),
			refName(is.Priority),
			refName(is.AssignedTo),
			format.When(is.UpdatedOn),
			format.Truncate(is.Subject, 120),
		) + "\n")
	}
	fmt.Fprintf(&b, "# returned %d of %d (offset %d, limit %d)\n", len(issues), total, q.Offset, q.Limit)
	if in.QueryID > 0 {
		fmt.Fprintf(&b, "# query_id=%d: the other filters were ignored by the server\n", in.QueryID)
	}
	return b.String(), nil
}

// statusParam maps open/closed/all or a status name to the issues.json status_id value.
func (d *Deps) statusParam(ctx context.Context, s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "open":
		return "open", nil
	case "closed":
		return "closed", nil
	case "all", "*":
		return "*", nil
	}
	n, err := d.M.Resolve(ctx, redmine.KStatus, s)
	if err != nil {
		return "", err
	}
	return strconv.Itoa(n.ID), nil
}

type getIn struct {
	ID  int   `json:"id,omitempty" jsonschema:"issue id"`
	IDs []int `json:"ids,omitempty" jsonschema:"several issue ids; use instead of id"`
	Raw bool  `json:"raw,omitempty" jsonschema:"return the untouched Redmine JSON (single id only)"`
}

func (d *Deps) getIssue(ctx context.Context, in getIn) (string, error) {
	ids := in.IDs
	if in.ID > 0 {
		ids = append([]int{in.ID}, ids...)
	}
	if len(ids) == 0 {
		return "", fmt.Errorf("id or ids is required")
	}
	if in.Raw {
		if len(ids) != 1 {
			return "", fmt.Errorf("raw accepts a single issue id")
		}
		data, err := d.C.IssueRaw(ctx, ids[0])
		if err != nil {
			return "", err
		}
		return rawClip(data), nil
	}

	blocks := make([]string, 0, len(ids))
	for _, id := range ids {
		is, err := d.C.Issue(ctx, id)
		if err != nil {
			return "", err
		}
		blocks = append(blocks, renderIssue(d, is))
	}
	return strings.Join(blocks, "\n\n"), nil
}

func renderIssue(d *Deps, is *redmine.Issue) string {
	var b strings.Builder
	fmt.Fprintf(&b, "#%d %s\n", is.ID, is.Subject)
	b.WriteString(format.Row(
		field("project", is.Project),
		field("tracker", is.Tracker),
		field("status", is.Status),
		field("priority", is.Priority),
	) + "\n")
	b.WriteString(format.Row(
		field("author", is.Author),
		field("assignee", is.AssignedTo),
		"created: "+dash(format.When(is.CreatedOn)),
		"updated: "+dash(format.When(is.UpdatedOn)),
	) + "\n")

	hours := "-"
	if is.EstimatedHours != nil || is.SpentHours > 0 {
		est := "-"
		if is.EstimatedHours != nil {
			est = format.Num(*is.EstimatedHours)
		}
		hours = format.Num(is.SpentHours) + "/" + est
	}
	b.WriteString(format.Row(
		fmt.Sprintf("progress: %d%%", is.DoneRatio),
		"due: "+dash(is.DueDate),
		"hours spent/est: "+hours,
	) + "\n")

	if strings.TrimSpace(is.Description) != "" {
		b.WriteString("\n[description]\n" + format.Clip(is.Description, 4000) + "\n")
	}
	if len(is.Attachments) > 0 {
		b.WriteString("\n[attachments]\nid | filename | size | url\n")
		for _, a := range is.Attachments {
			b.WriteString(format.Row(
				strconv.Itoa(a.ID), a.Filename, format.Size(a.Filesize),
				d.C.Base()+"/attachments/download/"+strconv.Itoa(a.ID)+"/"+url.PathEscape(a.Filename),
			) + "\n")
		}
	}
	if len(is.Relations) > 0 {
		b.WriteString("\n[relations]\n")
		for _, r := range is.Relations {
			other := r.IssueToID
			if other == is.ID {
				other = r.IssueID
			}
			fmt.Fprintf(&b, "%s #%d\n", r.RelationType, other)
		}
	}
	if len(is.Children) > 0 {
		b.WriteString("\nchildren:")
		for _, c := range is.Children {
			fmt.Fprintf(&b, " #%d", c.ID)
		}
		b.WriteString("\n")
	}

	var notes []redmine.Journal
	for _, j := range is.Journals {
		if strings.TrimSpace(j.Notes) != "" {
			notes = append(notes, j)
		}
	}
	if len(notes) > 0 {
		b.WriteString("\n[journals]\n")
		start := 0
		if len(notes) > 20 {
			start = len(notes) - 20
			fmt.Fprintf(&b, "# showing the last 20 of %d entries\n", len(notes))
		}
		for _, j := range notes[start:] {
			fmt.Fprintf(&b, "%s %s: %s\n", format.When(j.CreatedOn), dash(refName(j.User)), format.Clip(j.Notes, 600))
		}
	}
	return b.String()
}

type createIn struct {
	Project         string          `json:"project" jsonschema:"project name, identifier or id (required)"`
	Subject         string          `json:"subject" jsonschema:"issue subject (required)"`
	Description     string          `json:"description,omitempty" jsonschema:"issue description; content format is textile unless the server says otherwise"`
	Tracker         string          `json:"tracker,omitempty" jsonschema:"tracker name or id"`
	Priority        string          `json:"priority,omitempty" jsonschema:"priority name or id"`
	Assignee        string          `json:"assignee,omitempty" jsonschema:"user name, login, id, or me"`
	Status          string          `json:"status,omitempty" jsonschema:"initial status name or id; omit to use the tracker default"`
	ParentID        int             `json:"parent_issue_id,omitempty" jsonschema:"parent issue id"`
	StartDate       string          `json:"start_date,omitempty" jsonschema:"YYYY-MM-DD"`
	DueDate         string          `json:"due_date,omitempty" jsonschema:"YYYY-MM-DD"`
	EstimatedHours  float64         `json:"estimated_hours,omitempty"`
	AttachPaths     []string        `json:"attach_paths,omitempty" jsonschema:"local file paths on the machine running this server"`
	AttachUploadIDs []string        `json:"attach_upload_ids,omitempty" jsonschema:"ids returned by POST on the upload route; use for files on the client machine (single use)"`
	CustomFields    []customFieldIn `json:"custom_fields,omitempty" jsonschema:"project-required custom fields, e.g. 功能模块 (id 23); Redmine ignores ids it does not use"`
}

// customFieldIn is one custom field in create/update requests. Multi-value fields are not
// modelled: pass the single value Redmine expects, or use the web UI.
type customFieldIn struct {
	ID    int `json:"id" jsonschema:"custom field id (required); read it from get_issue raw output"`
	Value any `json:"value" jsonschema:"value as Redmine stores it; a list field may need an array, e.g. [\"UTM\"]"`
}

func (d *Deps) createIssue(ctx context.Context, in createIn) (string, error) {
	if err := d.guardWrite(); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.Project) == "" {
		return "", fmt.Errorf("project is required")
	}
	if strings.TrimSpace(in.Subject) == "" {
		return "", fmt.Errorf("subject is required")
	}

	project, err := d.M.Resolve(ctx, redmine.KProject, in.Project)
	if err != nil {
		return "", err
	}
	w := redmine.IssueWrite{ProjectID: &project.ID, Subject: &in.Subject}

	if in.Description != "" {
		w.Description = &in.Description
	}
	if in.StartDate != "" {
		w.StartDate = &in.StartDate
	}
	if in.DueDate != "" {
		w.DueDate = &in.DueDate
	}
	if in.ParentID > 0 {
		w.ParentID = &in.ParentID
	}
	if in.EstimatedHours > 0 {
		w.EstimatedHours = &in.EstimatedHours
	}
	if in.Tracker != "" {
		n, err := d.M.Resolve(ctx, redmine.KTracker, in.Tracker)
		if err != nil {
			return "", err
		}
		w.TrackerID = &n.ID
	}
	if in.Priority != "" {
		n, err := d.M.Resolve(ctx, redmine.KPriority, in.Priority)
		if err != nil {
			return "", err
		}
		w.PriorityID = &n.ID
	}
	if in.Assignee != "" {
		n, err := d.M.Resolve(ctx, redmine.KUser, in.Assignee)
		if err != nil {
			return "", err
		}
		w.AssignedToID = &n.ID
	}
	if in.Status != "" {
		n, err := d.M.Resolve(ctx, redmine.KStatus, in.Status)
		if err != nil {
			return "", err
		}
		w.StatusID = &n.ID
	}
	for _, cf := range in.CustomFields {
		if cf.ID <= 0 {
			return "", fmt.Errorf("custom_fields: id is required")
		}
		w.CustomFields = append(w.CustomFields, redmine.CustomFieldValue{ID: cf.ID, Value: cf.Value})
	}

	for _, path := range in.AttachPaths {
		up, err := d.C.Upload(ctx, path)
		if err != nil {
			return "", err
		}
		w.Uploads = append(w.Uploads, up)
	}

	staged, used, err := d.claimUploads(ctx, in.AttachUploadIDs)
	if err != nil {
		d.releaseUploads(used)
		return "", err
	}
	defer d.releaseUploads(used)
	w.Uploads = append(w.Uploads, staged...)

	is, err := d.C.CreateIssue(ctx, w)
	if err != nil {
		return "", err
	}

	parts := []string{}
	for _, v := range []string{refName(is.Tracker), refName(is.Project)} {
		if v != "" {
			parts = append(parts, v)
		}
	}
	where := ""
	if len(parts) > 0 {
		where = " (" + strings.Join(parts, ", ") + ")"
	}
	return fmt.Sprintf("#%d created%s. %s", is.ID, where, d.issueURL(is.ID)), nil
}

type updateIn struct {
	ID              int             `json:"id" jsonschema:"issue id (required)"`
	Status          string          `json:"status,omitempty" jsonschema:"new status name or id"`
	Assignee        string          `json:"assignee,omitempty" jsonschema:"user name, login, id, or me"`
	Priority        string          `json:"priority,omitempty" jsonschema:"priority name or id"`
	Tracker         string          `json:"tracker,omitempty" jsonschema:"tracker name or id"`
	Subject         string          `json:"subject,omitempty" jsonschema:"replacement subject"`
	Description     string          `json:"description,omitempty" jsonschema:"replacement description; requires confirm because it overwrites the old text"`
	Notes           string          `json:"notes,omitempty" jsonschema:"comment to add to the issue"`
	PrivateNotes    *bool           `json:"private_notes,omitempty" jsonschema:"mark the accompanying note as private"`
	Progress        *int            `json:"progress,omitempty" jsonschema:"done ratio 0-100"`
	AttachPaths     []string        `json:"attach_paths,omitempty" jsonschema:"add files that live on the machine running this server"`
	AttachUploadIDs []string        `json:"attach_upload_ids,omitempty" jsonschema:"add files staged via the upload route; use for files on the client machine (single use)"`
	CustomFields    []customFieldIn `json:"custom_fields,omitempty" jsonschema:"set custom fields by id, e.g. 功能模块 (23); an empty value clears the field"`
	Confirm         bool            `json:"confirm,omitempty" jsonschema:"required to close the issue or replace its description"`
}

func (d *Deps) updateIssue(ctx context.Context, in updateIn) (string, error) {
	if err := d.guardWrite(); err != nil {
		return "", err
	}
	if in.ID <= 0 {
		return "", fmt.Errorf("id is required")
	}

	var (
		w       redmine.IssueWrite
		changes []string
	)

	for _, path := range in.AttachPaths {
		up, err := d.C.Upload(ctx, path)
		if err != nil {
			return "", err
		}
		w.Uploads = append(w.Uploads, up)
		changes = append(changes, "attachment "+up.Filename)
	}
	staged, used, err := d.claimUploads(ctx, in.AttachUploadIDs)
	if err != nil {
		d.releaseUploads(used)
		return "", err
	}
	defer d.releaseUploads(used)
	for _, up := range staged {
		w.Uploads = append(w.Uploads, up)
		changes = append(changes, "attachment "+up.Filename)
	}

	if in.Status != "" {
		n, err := d.M.Resolve(ctx, redmine.KStatus, in.Status)
		if err != nil {
			return "", err
		}
		statuses, err := d.M.Statuses(ctx)
		if err != nil {
			return "", err
		}
		closed := false
		for _, s := range statuses {
			if s.ID == n.ID {
				closed = s.IsClosed
			}
		}
		if closed && !in.Confirm {
			return "", fmt.Errorf("closing issue #%d requires confirm: true (status %q is a closed status); confirm with the user first", in.ID, nameOr(n, in.Status))
		}
		w.StatusID = &n.ID
		changes = append(changes, "status -> "+nameOr(n, in.Status))
	}
	if in.Assignee != "" {
		n, err := d.M.Resolve(ctx, redmine.KUser, in.Assignee)
		if err != nil {
			return "", err
		}
		w.AssignedToID = &n.ID
		changes = append(changes, "assignee -> "+nameOr(n, in.Assignee))
	}
	if in.Priority != "" {
		n, err := d.M.Resolve(ctx, redmine.KPriority, in.Priority)
		if err != nil {
			return "", err
		}
		w.PriorityID = &n.ID
		changes = append(changes, "priority -> "+nameOr(n, in.Priority))
	}
	if in.Tracker != "" {
		n, err := d.M.Resolve(ctx, redmine.KTracker, in.Tracker)
		if err != nil {
			return "", err
		}
		w.TrackerID = &n.ID
		changes = append(changes, "tracker -> "+nameOr(n, in.Tracker))
	}
	if in.Subject != "" {
		w.Subject = &in.Subject
		changes = append(changes, "subject replaced")
	}
	if in.Description != "" {
		if !in.Confirm {
			return "", fmt.Errorf("replacing the description of issue #%d requires confirm: true: the old text is lost", in.ID)
		}
		w.Description = &in.Description
		changes = append(changes, "description replaced")
	}
	if in.Progress != nil {
		if *in.Progress < 0 || *in.Progress > 100 {
			return "", fmt.Errorf("progress must be 0..100, got %d", *in.Progress)
		}
		w.DoneRatio = in.Progress
		changes = append(changes, fmt.Sprintf("progress -> %d%%", *in.Progress))
	}
	for _, cf := range in.CustomFields {
		if cf.ID <= 0 {
			return "", fmt.Errorf("custom_fields: id is required")
		}
		w.CustomFields = append(w.CustomFields, redmine.CustomFieldValue{ID: cf.ID, Value: cf.Value})
		changes = append(changes, fmt.Sprintf("custom field %d -> %v", cf.ID, cf.Value))
	}
	if in.Notes != "" {
		w.Notes = &in.Notes
		w.PrivateNotes = in.PrivateNotes
		changes = append(changes, fmt.Sprintf("note added (%d chars)", len(in.Notes)))
	}

	if len(changes) == 0 {
		return "", fmt.Errorf("nothing to update: pass at least one field, status or notes")
	}
	if err := d.C.UpdateIssue(ctx, in.ID, w); err != nil {
		return "", err
	}
	return fmt.Sprintf("#%d updated: %s. %s", in.ID, strings.Join(changes, "; "), d.issueURL(in.ID)), nil
}

func nameOr(n redmine.Named, fallback string) string {
	if n.Name != "" {
		return n.Name
	}
	return fallback
}

// claimUploads reserves and uploads each staging id, de-duplicating first. A claimed id is
// single-use; the caller must release them whether the issue is created or not.
func (d *Deps) claimUploads(ctx context.Context, ids []string) ([]redmine.Upload, []string, error) {
	if len(ids) == 0 {
		return nil, nil, nil
	}
	if d.Upload == nil || !d.Upload.Enabled() {
		return nil, nil, fmt.Errorf("attach_upload_ids needs the HTTP transport with MCP_UPLOAD_DIR set; use attach_paths instead")
	}
	seen := make(map[string]bool, len(ids))
	var ups []redmine.Upload
	var used []string
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if !upload.ValidID(id) {
			return nil, used, fmt.Errorf("invalid upload id %q: expected 32 hex characters", id)
		}
		e, err := d.Upload.Claim(id)
		if err != nil {
			return nil, used, fmt.Errorf("upload id %s not found or expired (MCP_UPLOAD_TTL %s); re-upload the file", id, d.Upload.TTL())
		}
		used = append(used, id)
		f, err := os.Open(e.Path)
		if err != nil {
			return nil, used, fmt.Errorf("upload id %s: %w", id, err)
		}
		up, err := d.C.UploadFile(ctx, e.Filename, f, e.Size)
		f.Close()
		if err != nil {
			return nil, used, err
		}
		ups = append(ups, up)
	}
	return ups, used, nil
}

// releaseUploads deletes claimed staging directories. Failure is not worth reporting: the sweeper
// removes anything left behind.
func (d *Deps) releaseUploads(ids []string) {
	for _, id := range ids {
		_ = d.Upload.Remove(id)
	}
}

// rawClip bounds the raw JSON mode so a huge response cannot eat the context.
func rawClip(data []byte) string {
	const max = 128 << 10
	if len(data) <= max {
		return string(data)
	}
	return string(data[:max]) + fmt.Sprintf("\n… (raw JSON truncated, %d bytes total)", len(data))
}
