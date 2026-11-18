package redmine

import "strings"

// Ref is the {id,name} object Redmine nests everywhere.
type Ref struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Identifier string `json:"identifier,omitempty"`
	Login      string `json:"login,omitempty"`
	IsDefault  bool   `json:"is_default,omitempty"`
}

type Status struct {
	Ref
	IsClosed bool `json:"is_closed"`
}

type Project struct {
	Ref
	Description string `json:"description,omitempty"`
	Status      int    `json:"status,omitempty"`
	Parent      *Ref   `json:"parent,omitempty"`
}

// User deliberately omits api_key, which /users/current.json may include.
type User struct {
	ID        int    `json:"id"`
	Login     string `json:"login"`
	Firstname string `json:"firstname"`
	Lastname  string `json:"lastname"`
	Mail      string `json:"mail,omitempty"`
	Admin     bool   `json:"admin,omitempty"`
}

func (u *User) Name() string {
	if n := strings.TrimSpace(u.Firstname + " " + u.Lastname); n != "" {
		return n
	}
	return u.Login
}

type Issue struct {
	ID             int           `json:"id"`
	Project        *Ref          `json:"project,omitempty"`
	Tracker        *Ref          `json:"tracker,omitempty"`
	Status         *Ref          `json:"status,omitempty"`
	Priority       *Ref          `json:"priority,omitempty"`
	Author         *Ref          `json:"author,omitempty"`
	AssignedTo     *Ref          `json:"assigned_to,omitempty"`
	Parent         *Ref          `json:"parent,omitempty"`
	Subject        string        `json:"subject"`
	Description    string        `json:"description,omitempty"`
	StartDate      string        `json:"start_date,omitempty"`
	DueDate        string        `json:"due_date,omitempty"`
	DoneRatio      int           `json:"done_ratio,omitempty"`
	EstimatedHours *float64      `json:"estimated_hours,omitempty"`
	SpentHours     float64       `json:"spent_hours,omitempty"`
	CreatedOn      string        `json:"created_on,omitempty"`
	UpdatedOn      string        `json:"updated_on,omitempty"`
	ClosedOn       string        `json:"closed_on,omitempty"`
	Journals       []Journal     `json:"journals,omitempty"`
	Attachments    []Attachment  `json:"attachments,omitempty"`
	Relations      []Relation    `json:"relations,omitempty"`
	Children       []Issue       `json:"children,omitempty"`
	CustomFields   []CustomField `json:"custom_fields,omitempty"`
}

type Journal struct {
	ID           int    `json:"id"`
	User         *Ref   `json:"user,omitempty"`
	Notes        string `json:"notes,omitempty"`
	CreatedOn    string `json:"created_on,omitempty"`
	PrivateNotes bool   `json:"private_notes,omitempty"`
}

type Attachment struct {
	ID          int    `json:"id"`
	Filename    string `json:"filename"`
	Filesize    int64  `json:"filesize"`
	ContentType string `json:"content_type,omitempty"`
	CreatedOn   string `json:"created_on,omitempty"`
	Author      *Ref   `json:"author,omitempty"`
}

type Relation struct {
	ID           int    `json:"id"`
	IssueID      int    `json:"issue_id"`
	IssueToID    int    `json:"issue_to_id"`
	RelationType string `json:"relation_type"`
}

type CustomField struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Value any    `json:"value"`
}

type TimeEntry struct {
	ID        int     `json:"id"`
	Project   *Ref    `json:"project,omitempty"`
	Issue     *Ref    `json:"issue,omitempty"`
	User      *Ref    `json:"user,omitempty"`
	Activity  *Ref    `json:"activity,omitempty"`
	Hours     float64 `json:"hours"`
	Comments  string  `json:"comments,omitempty"`
	SpentOn   string  `json:"spent_on,omitempty"`
	CreatedOn string  `json:"created_on,omitempty"`
}

type SearchResult struct {
	ID          int    `json:"id"`
	Title       string `json:"title"`
	Type        string `json:"type"`
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
}

// Upload references a file already POSTed to /uploads.json.
type Upload struct {
	Token    string `json:"token,omitempty"`
	Filename string `json:"filename,omitempty"`
}

// CustomFieldValue is one custom field in a write request. Redmine ignores unknown ids; a
// multi-value field takes a JSON array, which is why Value is `any`.
type CustomFieldValue struct {
	ID    int `json:"id"`
	Value any `json:"value"`
}

// IssueWrite is the request body for create and update. nil means "unchanged".
type IssueWrite struct {
	ProjectID      *int               `json:"project_id,omitempty"`
	TrackerID      *int               `json:"tracker_id,omitempty"`
	StatusID       *int               `json:"status_id,omitempty"`
	PriorityID     *int               `json:"priority_id,omitempty"`
	Subject        *string            `json:"subject,omitempty"`
	Description    *string            `json:"description,omitempty"`
	AssignedToID   *int               `json:"assigned_to_id,omitempty"`
	ParentID       *int               `json:"parent_issue_id,omitempty"`
	StartDate      *string            `json:"start_date,omitempty"`
	DueDate        *string            `json:"due_date,omitempty"`
	EstimatedHours *float64           `json:"estimated_hours,omitempty"`
	DoneRatio      *int               `json:"done_ratio,omitempty"`
	Notes          *string            `json:"notes,omitempty"`
	PrivateNotes   *bool              `json:"private_notes,omitempty"`
	Uploads        []Upload           `json:"uploads,omitempty"`
	CustomFields   []CustomFieldValue `json:"custom_fields,omitempty"`
}

// TimeWrite is the request body for POST /time_entries.json.
type TimeWrite struct {
	IssueID    *int    `json:"issue_id,omitempty"`
	ProjectID  *int    `json:"project_id,omitempty"`
	SpentOn    string  `json:"spent_on,omitempty"`
	Hours      float64 `json:"hours"`
	ActivityID *int    `json:"activity_id,omitempty"`
	Comments   string  `json:"comments,omitempty"`
	UserID     *int    `json:"user_id,omitempty"`
}
