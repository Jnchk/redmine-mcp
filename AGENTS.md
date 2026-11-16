# AGENTS.md

MCP server (Go) exposing one Redmine 3.4+ instance. Design: `docs/00_basic_design.md`. Usage: `README.md`.

## Commands

```bash
go build ./...
go test ./...
go vet ./...
gofmt -w .    # the only formatter; no Makefile, no CI, no golangci config

go test ./internal/tools -run TestWriteTools -v   # single test
go test ./internal/redmine -run TestResolve       # one rule set
```

`go.mod` pins `go 1.26.4`; keep it. Sole direct dependency: `github.com/modelcontextprotocol/go-sdk`.

## MCP rules that are easy to break

- **stdout is the JSON-RPC wire under the stdio transport.** One `fmt.Println` corrupts the session. Logs go through `slog` to stderr (as `main` does). Never write to stdout in server code.
- Tools are registered in `internal/tools/tools.go` `Register`. Handlers go through `add()`, which returns one `TextContent` block; return an `error` and the SDK sets `IsError` for you. Do not wrap output in JSON — the compact text format is the product.
- Input schema is inferred from the `In` struct: `jsonschema` tags are descriptions, **no `omitempty` = required**, pointers = optional. There is no hand-written schema.
- HTTP transport is deliberately `Stateless: true, JSONResponse: true`. Do not add per-connection state; both transports serve the same `*mcp.Server`.
- Tool must work over both transports. Quick stdio check: pipe `initialize`, `notifications/initialized`, `tools/list`, `tools/call` lines into the binary and keep stdin open ~2s (closing stdin cancels in-flight calls).
- Annotations matter: read tools use `readOnly`; writes use `write(destructive)`. A new write tool also needs the `guardWrite()` call.

## Architecture boundaries

- `internal/redmine` must not import `mcp`. `internal/tools` must not do HTTP. `internal/format` renders, nothing else.
- New Redmine call: method on `Client` (`internal/redmine`), type in `types.go`, then a tool in `internal/tools`. Never fetch from a tool handler directly.
- All requests go through `Client.do`: sets `X-Redmine-API-Key`, appends `.json`, retries **GET only** once. Do not retry POST/PUT — it creates duplicate issues.
- `Meta` is the only name→id path (`Resolve`). Any new filter accepting a name must resolve through it, never match locally.
- `whoami` is the startup contract: `main` calls `/users/current.json` and exits on failure. Keep that behavior.

## Redmine 3.4 traps already handled — don't regress

- Base URL may contain a sub-URI (`https://redmine.example.com/redmine`). `Client` joins by string concat; `url.ResolveReference` would drop the prefix.
- 422 bodies carry `errors[]`; surface those strings verbatim.
- `subject` filtering matches the subject only; `q` uses `/search.json` and cannot be combined with filters.
- Textile is the 3.4 default. Only the prompt hint changes with `REDMINE_TEXT_FORMATTING`.
- Closing = a status with `is_closed`; there is no issue-delete API.
- `/users.json` is admin-only: degrade to numeric ids, never fail the whole call.
- Attaching to an **existing** issue is intentionally rejected (3.4 may replace the attachment set on `PUT`). Do not "fix" it without a real-server test.

## Tests

- Every test builds its own fake Redmine with `httptest` — there is no `testdata/` or fixture directory.
- `internal/tools/tools_test.go` is the real end-to-end: registers tools on an `mcp.Server` and calls them through an in-memory MCP client. New tool → add assertions there.
- No test has run against a live Redmine. Do not state or imply 3.4 compatibility is verified; `docs/00_basic_design.md` §10 has the pending docker integration test.

## Conventions

- Compact, line-oriented output only: `#` header, `-` for empty, UTC `YYYY-MM-DD HH:MMZ`, footer `# returned N of TOTAL`. Truncation must be labelled.
- Errors are one line starting `redmine:`, actionable, and never contain the API key.
- When behavior or a public tool schema changes, update `docs/00_basic_design.md` in the same change — it is kept in sync with the code.
