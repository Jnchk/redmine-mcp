package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"redmine-mcp/internal/redmine"
	"redmine-mcp/internal/upload"
)

// Deps is everything the tools need. One Deps belongs to one Redmine server.
type Deps struct {
	C            *redmine.Client
	M            *redmine.Meta
	ReadOnly     bool
	DefaultLimit int
	TextFormat   string // textile | markdown, for the user-facing hint
	// Upload stages client files for this instance; nil or disabled under stdio.
	Upload     *upload.Store
	UploadPath string // HTTP route, for whoami
}

func Register(srv *mcp.Server, d *Deps) {
	add(srv, &mcp.Tool{
		Name:        "whoami",
		Description: "Show the connected Redmine user, server URL, read-only mode, and whether user names can be resolved. Call this first.",
		Annotations: readOnly,
	}, d.whoami)

	add(srv, &mcp.Tool{
		Name:        "list_projects",
		Description: "List Redmine projects with id, identifier and name.",
		Annotations: readOnly,
	}, d.listProjects)

	add(srv, &mcp.Tool{
		Name:        "list_metadata",
		Description: "List trackers, statuses, priorities, time-entry activities, or users. Use it to discover valid names before creating or updating an issue.",
		Annotations: readOnly,
	}, d.listMetadata)

	add(srv, &mcp.Tool{
		Name: "search_issues",
		Description: "Find issues. Filters: project, status, assignee, tracker, priority, subject substring, updated_since, or a saved query_id. " +
			"Subject filters match the subject only; use q for full-text search over subject, description and notes (q cannot be combined with filters). " +
			"Defaults to open issues, 25 per page.",
		Annotations: readOnly,
	}, d.searchIssues)

	add(srv, &mcp.Tool{
		Name:        "get_issue",
		Description: "Read one or more issues: fields, description, attachments, relations and comment history.",
		Annotations: readOnly,
	}, d.getIssue)

	add(srv, &mcp.Tool{
		Name: "create_issue",
		Description: "Create an issue. project and subject are required; tracker, status, priority and assignee accept names or ids. " +
			"Attachments: attach_paths are files on the machine running this server; attach_upload_ids are ids returned by POST " +
			"on the upload route of this same server (single use, they expire). Use attach_upload_ids when the file is on the client machine. " +
			"custom_fields sets project-required fields by id (find the id in get_issue raw output).",
		Annotations: write(false),
	}, d.createIssue)

	add(srv, &mcp.Tool{
		Name: "update_issue",
		Description: "Update an issue's fields, status, assignee, progress or attachments, and/or add a comment via notes. " +
			"Closing the issue or replacing its description requires confirm: true. " +
			"Attachments: attach_paths are files on the machine running this server; attach_upload_ids are ids returned by POST " +
			"on the upload route of this same server (single use). Use attach_upload_ids when the file is on the client machine. " +
			"custom_fields sets custom fields by id; an empty value clears one.",
		Annotations: write(true),
	}, d.updateIssue)

	add(srv, &mcp.Tool{
		Name:        "log_time",
		Description: "Log spent hours against an issue. activity defaults to the server's default activity.",
		Annotations: write(false),
	}, d.logTime)

	add(srv, &mcp.Tool{
		Name:        "list_time_entries",
		Description: "List logged time for a project, user, issue and/or date range.",
		Annotations: readOnly,
	}, d.listTimeEntries)
}

// add adapts a string-returning handler to the SDK's typed tool API.
func add[In any](srv *mcp.Server, t *mcp.Tool, h func(context.Context, In) (string, error)) {
	mcp.AddTool(srv, t, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		s, err := h(ctx, in)
		if err != nil {
			return nil, nil, err
		}
		return text(s)
	})
}

func text(s string) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}, nil, nil
}

var readOnly = &mcp.ToolAnnotations{ReadOnlyHint: true}

func write(destructive bool) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{DestructiveHint: &destructive}
}

func (d *Deps) guardWrite() error {
	if d.ReadOnly {
		return fmt.Errorf("this server runs in read-only mode (REDMINE_READ_ONLY); write tools are disabled")
	}
	return nil
}

func (d *Deps) limit(n int) int {
	switch {
	case n <= 0:
		return d.DefaultLimit
	case n > 100:
		return 100
	default:
		return n
	}
}

func (d *Deps) issueURL(id int) string {
	return fmt.Sprintf("%s/issues/%d", d.C.Base(), id)
}

// refName is the display name of a nested {id,name}, or "" when absent.
func refName(r *redmine.Ref) string {
	if r == nil {
		return ""
	}
	return r.Name
}

// field renders "label: value" with an explicit dash for missing data.
func field(label string, r *redmine.Ref) string {
	if r == nil {
		return label + ": -"
	}
	return label + ": " + dash(r.Name)
}

func dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}
