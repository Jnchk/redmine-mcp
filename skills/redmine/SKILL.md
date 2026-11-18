---
name: redmine
description: Read and write Redmine issues, projects, metadata, attachments and time entries. Use when the user asks about tickets, issues, projects, time logs, or says attach/send/upload a file to a ticket.
compatibility: Requires redmine-mcp, connected directly or as an upstream named "redmine" on mcp-gateway.
---

# Redmine

Call `whoami` first: it reports the server URL, the logged-in user, read-only mode, and whether user
names resolve.

## Mode

- Tools appear directly (`search_issues`, `create_issue`, …): call them as tools.
- Only `list_servers` / `list_tools` / `call_tool` exist: go through the gateway, tool names become
  arguments — `tools.gateway.call_tool({ server: "redmine", tool: "create_issue", arguments: {...} })`.
  `list_tools({ server: "redmine", full: true })` returns the exact schemas.
- Read-only mode rejects every write; report that instead of retrying another way.

## Task → tool

| Goal | Tool | Required / easy to miss |
|---|---|---|
| who am I, read-only? | `whoami` | — |
| find a project | `list_projects` | `status` filters projects, not issues |
| trackers, statuses, priorities, activities, users | `list_metadata` | without admin rights users degrade to numeric ids |
| search | `search_issues` | `subject` matches the subject only; `q` is full text and cannot be combined with filters; `query_id` ignores all other filters |
| read one or several issues | `get_issue` | `id` or `ids`; journals and attachments included |
| create | `create_issue` | `project` + `subject` |
| update, comment, close | `update_issue` | `id`; `confirm: true` to close or replace the description |
| book hours | `log_time` | `issue_id` + `hours`; `activity` from `list_metadata` |
| list hours | `list_time_entries` | visibility follows Redmine permissions |

Resolve names to ids through `list_metadata` / `list_projects` / `whoami`; do not guess an id. Closing
means setting a status with `is_closed`; there is no issue-delete tool. `description` is Textile unless
`whoami` says markdown.

## Attaching a file from this machine

`attach_paths` reads files on the machine running the **server**; use it only when the file is there.
Otherwise stage the file and pass the returned id — never base64 a file into a tool call.

```bash
# through mcp-gateway
python3 <mcp-gateway clone>/skills/mcp-upload/scripts/mcp_upload.py --server redmine --path ./report.pdf
# direct HTTP to redmine-mcp (token = that server's MCP_TOKEN)
curl -sS -X POST "http://<server>:8080/upload?filename=report.pdf" \
  -H "Authorization: Bearer <MCP_TOKEN>" --data-binary @report.pdf
```

```js
// direct stdio has no /upload route: only attach_paths works there
await tools.gateway.call_tool({ server: "redmine", tool: "create_issue",
  arguments: { project: "...", subject: "...", attach_upload_ids: ["<id>"] } })
```

- The id is single use and expires (15 minutes by default). A failed write keeps it for one retry;
  a successful one consumes it, so re-stage rather than reuse.
- `create_issue` and `update_issue` accept `attach_upload_ids`; other tools do not. Adding to an
  existing issue leaves its current attachments in place.
- Limits: the server's upload route is on by default; an empty `MCP_UPLOAD_DIR` disables it, and files
  are capped by `REDMINE_MAX_UPLOAD`.
