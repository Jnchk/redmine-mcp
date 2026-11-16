# redmine-mcp — Basic Design

| | |
|---|---|
| Status | Draft, ready to implement |
| Target | Redmine **3.4.x and newer** (single code path, no version detection) |
| Language | Go **1.26.4** |
| MCP SDK | `github.com/modelcontextprotocol/go-sdk` **v1.8.0** (official) |
| Transports | stdio **and** Streamable HTTP |
| Deployment | **one process = one Redmine server** |
| Module | `redmine-mcp` (rename to `github.com/<org>/redmine-mcp` when published) |

---

## 1. Scope

### Goals

1. Expose the Redmine operations people actually do — find, read, create, update, comment, log time — as MCP tools.
2. Work against **3.4.x** as the oldest supported target, and not break on 4.x/5.x/6.x.
3. Be usable over stdio (local clients spawning the binary) and over Streamable HTTP (shared/remote).
4. Accept human names for projects, statuses, trackers, priorities, activities and users.
5. Keep model-facing output small enough to be worth the context it costs.
6. Safe defaults: read-only switch, explicit confirmation for destructive changes, no secrets in logs.

### Non-goals (deliberately skipped)

- Multi-server in one process (issue ids collide; run two processes instead).
- Wiki, news, forums, documents, repositories, files, versions, groups, roles, custom-field CRUD.
- Reimplementing Redmine's filter DSL — use saved queries (`query_id`).
- Per-user auth on the HTTP transport (one shared token + one API key).
- Deleting issues (not in the 3.4 REST API; and it is a web-UI action anyway).
- Storing the API key in a config file.

---

## 2. Architecture

```
cmd/redmine-mcp/main.go        flags, wiring, signal handling
internal/config/config.go      env + flag parsing, validation
internal/redmine/              Redmine REST client (no MCP types)
    client.go                  do(), auth, base URL, timeouts, retries
    errors.go                  HTTP status -> typed error
    types.go                   JSON structs for the objects we use
    issues.go                  List/Get/Create/Update
    meta.go                    projects, trackers, statuses, priorities, activities, users
    time.go                    time entries
    upload.go                  POST /uploads.json (two-step attachment flow)
    resolve.go                 name -> id resolution + metadata cache
internal/tools/                MCP layer: schema in, compact text out
    tools.go                   registry, shared helpers, read-only guard
    issues.go                  search_issues, get_issue, create_issue, update_issue
    meta.go                    whoami, list_projects, list_metadata
    time.go                    log_time, list_time_entries
internal/format/format.go      table/field rendering, truncation
docs/00_basic_design.md        this file
```

Two hard rules about the layers:

- `internal/redmine` imports **nothing** from `mcp`. It is testable on its own.
- `internal/tools` never talks HTTP. It calls the client, then formats.

### Wiring

```go
srv := mcp.NewServer(&mcp.Implementation{Name: "redmine-mcp", Version: version}, &mcp.ServerOptions{
    Instructions: instructions, // Textile note, name resolution, read-only state
})
tools.Register(srv, client, cfg) // mcp.AddTool per tool

switch cfg.Transport {
case "stdio":
    err = srv.Run(ctx, &mcp.StdioTransport{})
case "http":
    handler := mcp.NewStreamableHTTPHandler(
        func(*http.Request) *mcp.Server { return srv }, // same server is allowed
        &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true},
    )
    mux := http.NewServeMux()
    mux.Handle(cfg.Path, authMiddleware(cfg.Token, handler))
    hs := &http.Server{Addr: cfg.Addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
    err = runWithShutdown(ctx, hs)
}
```

Notes on the SDK choices:

- **Shared `*mcp.Server` across HTTP sessions** is explicitly supported (`getServer` may return the same server).
- **`Stateless: true`**: no session map, no `Mcp-Session-Id`, survives restarts and multiple replicas. We have no subscriptions, sampling, or server-initiated messages, so nothing is lost. GET/DELETE return 405.
- **`JSONResponse: true`**: responses are `application/json` instead of an SSE stream. Easier to curl and to debug; the client still works.
- Request bodies are capped at 4 MiB by default — another reason attachments go by **path**, not base64.
- `mcp.AddTool` auto-infers input/output JSON Schema from the `In`/`Out` structs and auto-sets `IsError` when the handler returns a non-nil error. Handlers therefore return `(nil, Out{}, fmt.Errorf(...))` for expected failures; do not return a result *and* an error.

---

## 3. Configuration

Environment first (matches how MCP clients pass credentials), a few flags for transport only.

| Var / flag | Default | Meaning |
|---|---|---|
| `REDMINE_URL` | — (required) | `https://host` or `https://host/redmine` (sub-URI allowed). Trailing slash stripped. |
| `REDMINE_API_KEY` | — (required) | Sent as `X-Redmine-API-Key`. Never logged, never echoed back. |
| `REDMINE_INSECURE` | `false` | Skip TLS verification (self-signed corporate installs). |
| `REDMINE_TIMEOUT` | `45s` | Per-request client timeout. |
| `REDMINE_READ_ONLY` | `false` | Reject every write tool with a clear error. |
| `REDMINE_DEFAULT_LIMIT` | `25` | Page size when the caller omits `limit`; clamped to 1..100. |
| `REDMINE_META_TTL` | `5m` | Metadata cache lifetime. |
| `REDMINE_TEXT_FORMATTING` | `textile` | `textile` or `markdown`; only changes the guidance in the server instructions. |
| `REDMINE_MAX_UPLOAD` | `25 MiB` | Reject larger attachment paths before opening them. |
| `MCP_TRANSPORT` / `--transport` | `stdio` | `stdio` or `http`. |
| `MCP_ADDR` / `--addr` | `127.0.0.1:8080` | HTTP listen address. |
| `MCP_PATH` | `/mcp` | HTTP endpoint path. |
| `MCP_TOKEN` | — | If set, required as `Authorization: Bearer <token>`. **Mandatory** when `MCP_ADDR` is not loopback. |
| `--read-only` | — | Flag equivalent of `REDMINE_READ_ONLY`. |

Startup sequence:

1. Parse + validate config (fail fast on missing URL/key; warn on `http://`).
2. `GET /users/current.json` — proves key, TLS, sub-URI and permissions are right. Log `connected to <url> as <login> (<admin|non-admin>)` to stderr.
3. Register tools. If read-only, write tools are still listed but return an error explaining the mode (listing them helps the model plan, and the error is honest).

Base-URL pitfall (sub-URI installs are common on 3.4): **do not** use `url.ResolveReference` with a relative path — `https://host/redmine` + `issues.json` becomes `https://host/issues.json`. Build with `url.JoinPath(base, "issues.json")` or normalize the base to end in `/` once at startup.

---

## 4. Tool catalogue

Names are snake_case, no prefix (dedicated server). Every tool returns a single plain-text `TextContent` block (`Out = any` in the SDK, so no JSON wrapper around the prose). `raw: true` opts into the untouched Redmine JSON for debugging.

| Tool | Annotations | Purpose |
|---|---|---|
| `whoami` | read-only | Connected user, login, admin flag, server URL, read-only mode. First call on any new deployment. |
| `list_projects` | read-only | `id / identifier / name / status`, optional `q` substring filter. |
| `list_metadata` | read-only | `kind`: `trackers`, `statuses`, `priorities`, `activities`, `users`. Optional `q`. |
| `search_issues` | read-only | The main query tool. Filters + pagination + optional full-text `q`. |
| `get_issue` | read-only | One or more issues with description, attachments, relations, journals. |
| `create_issue` | write | Create with optional attachments. |
| `update_issue` | write, **destructive** | Fields, status, assignee, note, progress, attachments. |
| `log_time` | write | Log hours against an issue. |
| `list_time_entries` | read-only | Time entries for a project/user/date range. |

Nine tools. Adding a tenth needs a reason; `update_issue` is intentionally the only "change things" entry point so the model does not have to choose between `add_comment`, `set_status`, `assign`, etc.

### Inputs that matter

`search_issues`:

```
project, status (default "open"), assignee, tracker, priority,
subject (substring, maps to subject=~), updated_since (YYYY-MM-DD),
sort (default "updated_on:desc"), limit, offset, q, raw
```

- `status` accepts `open` / `closed` / `all` / a name / an id.
- `open` is sent as `status_id=open`; `closed` as `status_id=closed`; `all` as `status_id=*`.
- `q` performs a **full-text search** via `GET /search.json?q=...&issues=1&scope=all` and returns matches with snippets. It cannot be combined with the other filters (different endpoint) — return a clear error if both are given rather than silently dropping one.
- `query_id` is passed through verbatim; when present, other filters are ignored (Redmine semantics) and the output says so.

`get_issue`: `id` or `ids []int` (fetched sequentially; the list is normally short). `include`: `journals`, `attachments`, `relations`, `children` (default all).

`update_issue`: `id`, `status`, `assignee`, `priority`, `tracker`, `subject`, `description`, `notes`, `private_notes`, `progress`, `attach_paths []string`, `confirm bool`.
`confirm: true` is required for the two irreversible moves: moving the issue into a status whose `is_closed` is true, and replacing the description (the old text is lost). An empty update is rejected. Assignee, priority, tracker and progress changes do not need confirmation, or the tool becomes unusable in practice.

`create_issue`: `project` (required), `subject` (required), `description`, `tracker`, `priority`, `assignee`, `status`, `parent`, `start_date`, `due_date`, `estimated_hours`, `attach_paths`.

`attach_paths` are **local paths on the process host** (the MCP client's machine for stdio; the server's machine for HTTP). Document that in the tool description. Uploads are read with a size cap (`REDMINE_MAX_UPLOAD`, default 25 MiB) and streamed to `POST /uploads.json` as `application/octet-stream`.

---

## 5. Name → ID resolution (normative)

The model will write `status: "In Progress"`, `assignee: "alice"`, `project: "Website"`. Rejecting those and demanding ids is the single biggest source of failed tool calls. Rules, applied uniformly by `internal/redmine/resolve.go`:

1. **Numbers win.** If the value is an integer, or a string of digits (`"42"`), use it as the id. No lookup, no validation beyond the request itself.
2. **`me`** is special-cased for `assignee`/`user` → the id from `/users/current.json` (cached for the process lifetime).
3. **Resolve against a cached list**, per kind: projects (`/projects.json?limit=100&offset=`, paged to completion), trackers, issue statuses, priorities (`/enumerations/issue_priorities.json`), activities (`/enumerations/time_entry_activities.json`), users (`/users.json?limit=100`, best effort).
4. **Match order**, first hit wins:
   1. exact, case-insensitive on name;
   2. exact, case-insensitive on identifier (projects) or login (users);
   3. unique, case-insensitive **prefix** match;
   4. unique, case-insensitive **substring** match.
5. **Ambiguity is an error, never a guess.** On multiple hits return:
   `ambiguous status "progress": 2 matches — "In Progress" (2), "Progress review" (7). Pass an id or a longer name.`
6. **No match is an error that lists the real values**, capped at 20:
   `unknown tracker "Bugfix". Available: Bug (1), Feature (2), Support (3), ... . Use list_metadata for the full list.`
7. **Cache invalidation:** TTL `REDMINE_META_TTL`; also drop the cache entry for a resolved id when the follow-up call returns 404 (the object was renamed/deleted).
8. **Case folding is Unicode-aware** (`strings.ToLower` is fine), which makes Chinese/Japanese names work without a separate code path; substring matching covers the "partial name" habit.
9. **Defaulting:** a missing `tracker`/`priority`/`status` on create is **not** an error — Redmine applies the project/tracker default. A missing `project` on create **is** an error. Never invent a project.
10. **Users are best effort.** `/users.json` requires admin. If it returns 403, keep numeric ids and `me` working, and make name resolution return `cannot resolve user names: the API key lacks permission to list users; pass a numeric user id.` Do not fail the whole request.
11. **Cache, don't preload.** Load a metadata kind on first use, not at startup, so `whoami` stays fast even on a big instance.

The `whoami` output must state whether user-name resolution is available, so the model learns the rule instead of tripping over it.

---

## 6. Output format (normative)

Context is the budget. Default output is line-oriented plain text; JSON is opt-in.

1. **Lists are one record per line**, pipe-separated, with a single header line starting `#`:
   ```
   # id | status | tracker | priority | assignee | updated | subject
   1234 | In Progress | Bug | High | alice | 2026-11-14 08:12Z | login fails on SSO
   1235 | New | Feature | Normal | - | 2026-11-13 17:40Z | add CSV export
   # returned 2 of 2 (offset 0, limit 25)
   ```
2. **Every list ends with a count line** `# returned N of TOTAL (offset X, limit L)`. The model needs to know it is looking at a page; without it, it reports "there are 2 issues".
3. **Empty is explicit**: `no issues matched (project=website, status=open). Widen filters or try q for full-text search.` Never return an empty string or bare `ok`.
4. **Missing values render as `-`**, never `null`, `<nil>`, or an empty column that shifts the table.
5. **Timestamps are UTC, `YYYY-MM-DD HH:MMZ`.** No local-time conversion, no RFC3339 nanoseconds.
6. **Ids lead the line and are bare integers** so the model can chain `get_issue` without re-parsing labels.
7. **Truncation is visible and labelled:**
   - list `subject`: 120 chars → `… (truncated)`;
   - `description` in `get_issue`: 4000 chars → `… (truncated, 8123 chars total)`;
   - each journal note: 600 chars, latest 20 journals, then `# 23 more journal entries not shown (use offset or the web UI)`.
   Never silently drop content.
8. **`get_issue` is the only verbose tool** and uses labelled sections, not JSON: `#id subject`, a key/value block, then `[description]`, `[attachments]`, `[relations]`, `[journals]`.
9. **Attachments** render as `id | filename | size | url`, where the URL is `<REDMINE_URL>/attachments/download/<id>/<filename>` (downloadable without the API key only if the file is public — note that in the tool description, don't pretend otherwise).
10. **Write results are one line plus the URL**, and state what changed:
    `#1234 updated: status "In Progress" -> "Closed", note added (120 chars). https://redmine.example.com/issues/1234`
    and for create: `#1234 created (Bug, Website). https://redmine.example.com/issues/1234`
11. **No conversational filler.** No "Here are the issues you asked for", no markdown tables (pipe text is cheaper and survives every client), no emoji, no headings thicker than `h3.`-free plain text.
12. **`raw: true`** returns the untouched Redmine JSON, truncated at 128 KiB with an explicit note. Reserved for debugging; tool descriptions say so.
13. **Errors are one line, actionable, and never include the key or the full request body:**
    `redmine: 422 validation failed: Subject can't be blank; Tracker is not included in the list.`
    `redmine: 403 permission denied: you cannot view time entries for other users.`
14. **Never fabricate fields.** If 3.4 does not return `updated_by` or `story_points`, they do not appear in the output.

Rationale for 1–2: a 25-issue JSON response is ~6–8k tokens; the same lines are ~400. That difference decides whether an agent can afford a second query.

---

## 7. Redmine API mapping and 3.4 quirks

| Operation | Endpoint | Notes |
|---|---|---|
| Current user | `GET /users/current.json` | Works for non-admins. |
| Projects | `GET /projects.json?limit=100&offset=` | Page to completion; keep archived projects out of name resolution. |
| Trackers | `GET /trackers.json` | Global, no pagination. |
| Statuses | `GET /issue_statuses.json` | `is_closed` tells us which ids close an issue. |
| Priorities | `GET /enumerations/issue_priorities.json` | |
| Activities | `GET /enumerations/time_entry_activities.json` | Note the `enumerations/` prefix. |
| Issues list | `GET /issues.json` | `limit` max 100, default 25. `total_count` in every response. |
| Issue | `GET /issues/:id.json?include=journals,attachments,relations,children` | `include` is supported in 3.4. |
| Create issue | `POST /issues.json` `{"issue":{...}}` | Everything wrapped in `issue`. |
| Update issue | `PUT /issues/:id.json` `{"issue":{...}}` | `notes` alone is a comment. |
| Watchers | `POST/DELETE /issues/:id/watchers.json` | `{"user_id":N}`; add watchers in the same create via `watcher_user_ids`. |
| Search | `GET /search.json?q=&issues=1&scope=all&limit=` | Different shape: `results[].id/title/url/description/type`. |
| Upload | `POST /uploads.json`, `Content-Type: application/octet-stream` | Returns `{"upload":{"token":"..."}}`. |
| Time entries | `GET /time_entries.json`, `POST /time_entries.json` | Supports `limit`/`offset`; non-admins see only their own unless `user_id` is given. |

Traps to encode in code, not in comments:

- **Always suffix `.json`.** Missing it returns HTML with status 200 and the JSON decoder produces a confusing error.
- **Attachments on update.** `PUT /issues/:id.json` with `issue[uploads]` can replace the attachment set on some versions. Behaviour must be verified against a real 3.4; until then, `attach_paths` on update is implemented as "reference existing attachments by `id` + add the new tokens", and an integration test pins it.
- **No issue delete endpoint.** "Close" = `status_id` of a status with `is_closed: true`, discovered from `/issue_statuses.json`.
- **3.4 has no effective "grep description".** `subject=~word` filters subjects only; full text needs `/search.json`. Tool descriptions must say this.
- **Saved queries are the filter DSL.** `query_id=N` beats reimplementing 30 filter params; surface it.
- **Time entries**: admin vs non-admin visibility differs; `list_time_entries` without `user` must say whose entries it shows.
- **Unknown write fields are ignored, not rejected** (Rails `safe_attributes`), so sending a 5.x-only field to 3.4 is harmless but silently has no effect. Only send fields we can verify exist in 3.4; do not promise effects we cannot confirm.
- **Text formatting**: 3.4 defaults to **Textile**. Server instructions say "use Textile: `h3. ` headings with blank lines around them, `*bold*` with a space on both sides". A `REDMINE_TEXT_FORMATTING=markdown` switch flips the guidance; do not try to auto-detect (the settings endpoint is admin-only).
- **Dates** are `YYYY-MM-DD` in requests and `2006-01-02T15:04:05Z` (RFC3339) in responses; parse and re-render, never echo raw timestamps.
- **Numbers**: `estimated_hours` is a float, `done_ratio` an int, ids are ints, custom fields are `[{id,name,value}]` with `value` sometimes an array. Use `json.RawMessage` for `custom_fields` and don't interpret it.

---

## 8. Error model

One typed error from the client, mapped once in the tool layer:

| Condition | Message |
|---|---|
| DNS/TLS/timeout | `redmine: cannot reach <host>: <cause>. Check REDMINE_URL / REDMINE_INSECURE / network.` |
| 401 | `redmine: authentication failed (401). The API key is invalid, expired, or the user is locked.` |
| 403 | `redmine: permission denied (403): <Redmine's message if any>. This is an account permission, not a retryable error.` |
| 404 | `redmine: not found (404): issue 123 does not exist or is not visible to you.` + cache invalidation |
| 422 | `redmine: 422 validation failed: <errors joined by "; ">.` — verbatim from Redmine, never retried |
| 5xx | `redmine: Redmine returned 500 after 1 retry. This is a server-side problem; check <url>/admin/info.` |
| Read-only | `redmine: this server runs in read-only mode (REDMINE_READ_ONLY); write tools are disabled.` |

Retries: **GET only**, once, on 502/503/504 and on transport errors. Never retry POST/PUT — a retried create makes duplicate tickets.

---

## 9. Security

- API key only from the environment; never in `argv` (visible in `ps`), never in logs, never in tool output. `whoami` shows the login, not the key.
- HTTP transport: bind `127.0.0.1` by default; refuse to start on a non-loopback address without `MCP_TOKEN`. Bearer check uses `subtle.ConstantTimeCompare`.
- Cross-origin: keep the SDK's default localhost protection; do not disable it.
- `REDMINE_INSECURE` logs a warning on every start — it is a deliberate downgrade.
- Attachment paths: read only the given path, cap the size, refuse non-regular files. This is a trust boundary — `attach_paths` are user/model supplied, and for the HTTP transport they read the server's filesystem. Say so in the tool description; a future `REDMINE_UPLOAD_DIR` allowlist is the upgrade path.
- Write guard: `REDMINE_READ_ONLY` is checked in `internal/tools`, once, before dispatch.
- Reject `http://` Redmine URLs with a warning (not an error — some internal installs).

---

## 10. Testing

- **Unit tests, `httptest`**: each test builds its own fake 3.4 server inline (no shared fixture files yet). They pin the request shape (wrapped in `issue`, `.json` suffix, sub-URI prefix, headers) and the parser.
- **Tool-level end-to-end test**: `internal/tools/tools_test.go` registers the real tools on an `mcp.Server`, connects an in-memory MCP client, and calls every read tool plus create/update/read-only — this is the test that would have caught the nil-ref rendering bug.
- **Resolution tests**: table-driven for each rule in §5 — digits, `me`, exact, prefix, substring, ambiguous, unknown, 403-on-users.
- **Formatting tests**: golden strings for each tool's text output, including empty results, truncation, and the count line.
- **Integration (optional, tagged `//go:build integration`)**: `docker run -d -p 3000:3000 redmine:3.4` (sqlite default), wait for `/login`, run the read + write flows, including the attachment update case. Add a 5.x container to the same test to catch field drift. Do not put this in the default `go test` path.
- **Manual smoke**: `whoami` over stdio, then over HTTP with `curl -H 'Authorization: Bearer ...' -H 'Content-Type: application/json' -d '{"jsonrpc":"2.0",...}'`.

Definition of done for the first release: `go vet ./...`, `go test ./...` green, `whoami/search_issues/get_issue/create_issue/update_issue` work against a real 3.4 instance, and read-only mode blocks all three write tools.

---

## 11. Open questions

1. Attachment-on-update semantics on 3.4 — resolve with the integration test before exposing `attach_paths` on update. Until then, create-only attachments are shipped and update returns a clear "not supported yet" error.
2. Whether `private_notes` exists on 3.4's issue API — test against the container; if unsupported, drop the field and stop advertising it.
3. Whether to add a `--server <name>` config-file mode later. Deliberately deferred; one process per server covers the real use case.
