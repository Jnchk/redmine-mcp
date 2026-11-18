# redmine-mcp

[![CI](https://github.com/Jnchk/redmine-mcp/actions/workflows/ci.yml/badge.svg)](https://github.com/Jnchk/redmine-mcp/actions/workflows/ci.yml)

MCP server for Redmine 3.4.x and newer, written in Go. One process serves one Redmine server. Reads and writes
issues, comments, attachments, and time entries; resolves human names (`"In Progress"`, `"alice"`, `"me"`) to ids
server-side and returns compact plain text instead of raw JSON.

Full design: [docs/00_basic_design.md](docs/00_basic_design.md).

## Build

```bash
go build -o redmine-mcp ./cmd/redmine-mcp
go test ./...
```

Go 1.26.4. Dependencies: only `github.com/modelcontextprotocol/go-sdk`.

## Configure

| Variable | Default | Meaning |
|---|---|---|
| `REDMINE_URL` | required | `https://redmine.example.com` or `https://redmine.example.com/redmine` (sub-URI ok) |
| `REDMINE_API_KEY` | required | from **My account → API access key** |
| `REDMINE_INSECURE` | `false` | skip TLS verification (self-signed servers) |
| `REDMINE_READ_ONLY` | `false` | disable create/update/log_time |
| `REDMINE_TIMEOUT` | `45s` | per-request timeout |
| `REDMINE_DEFAULT_LIMIT` | `25` | page size when a tool omits `limit` |
| `REDMINE_META_TTL` | `5m` | metadata cache lifetime |
| `REDMINE_MAX_UPLOAD` | `25 MiB` | attachment size cap (bytes) |
| `REDMINE_TEXT_FORMATTING` | `textile` | `textile` or `markdown` (prompt hint only) |
| `MCP_TRANSPORT` | `stdio` | `stdio` or `http` |
| `MCP_ADDR` | `127.0.0.1:8080` | HTTP listen address(es), comma-separated (`127.0.0.1:8080,10.0.0.5:8080`) |
| `MCP_PATH` | `/mcp` | HTTP endpoint path |
| `MCP_TOKEN` | — | optional bearer token; when set, every request must send it. A non-loopback listener without one logs a warning |
| `MCP_UPLOAD_DIR` | `<tmp>/redmine-mcp-uploads` | staging directory, **one per instance** (`/tmp/redmine-mcp-neo`); empty disables the upload route |
| `MCP_UPLOAD_PATH` | `/upload` | upload route path; must differ from `MCP_PATH` |
| `MCP_UPLOAD_TTL` | `15m` | how long an unclaimed staged file survives |

Flags: `--transport`, `--addr`, `--read-only` override the env values.

The three `MCP_UPLOAD_*` rows are part of the staging-upload design and are not implemented yet.

On startup the server calls `GET /users/current.json`; if that fails it exits with the reason instead of letting a
client hit a broken server.

## Run

stdio (what MCP clients spawn):

```bash
REDMINE_URL=https://redmine.example.com REDMINE_API_KEY=xxx ./redmine-mcp
```

Streamable HTTP (stateless, JSON responses):

```bash
REDMINE_URL=https://redmine.example.com REDMINE_API_KEY=xxx \
MCP_TRANSPORT=http MCP_ADDR=127.0.0.1:8080 MCP_TOKEN=change-me ./redmine-mcp

# several listeners; a non-loopback entry without MCP_TOKEN is allowed but logs a warning
MCP_TRANSPORT=http MCP_ADDR=127.0.0.1:8080,10.0.0.5:8080 MCP_TOKEN=change-me ./redmine-mcp

# LAN deployment without a token (startup warning, no auth middleware)
MCP_TRANSPORT=http MCP_ADDR=0.0.0.0:8080 ./redmine-mcp
```

```bash
curl -s http://127.0.0.1:8080/mcp \
  -H 'Authorization: Bearer change-me' \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"curl","version":"0"}}}'
```

Client config (opencode example, stdio):

```json
{
  "mcp": {
    "servers": {
      "redmine": {
        "type": "local",
        "command": ["/path/to/redmine-mcp"],
        "environment": {
          "REDMINE_URL": "https://redmine.example.com",
          "REDMINE_API_KEY": "{env:REDMINE_API_KEY}"
        }
      }
    }
  }
}
```

## Attachments

`attach_paths` reads a path **on the process host** — the same machine as the client for stdio, the
server's machine for HTTP. When the client runs elsewhere, stage the file first:

```bash
# 1) upload: prints one line — the 32-hex staging id
#    run from the root of your mcp-gateway clone
python3 skills/mcp-upload/scripts/mcp_upload.py --server redmine --path ./report.pdf
# 9f1c0b7a4e2d4c3b8a5f6e7d0c1b2a39

# 2) attach: pass the id, not the file
#    create_issue(..., attach_upload_ids: ["9f1c0b7a4e2d4c3b8a5f6e7d0c1b2a39"])
```

The helper is the `mcp-upload` skill of [mcp-gateway](https://github.com/Jnchk/mcp-gateway), Python 3.8+
and standard library only; `--server redmine` names the gateway's upstream entry. It reads its own client
config `~/.config/mcp-gateway/client.json` (gateway `url` + `token`) and never touches the gateway's
`config.json`; `MCP_GATEWAY_CONFIG`, `MCP_GATEWAY_URL` and `MCP_GATEWAY_TOKEN` override it.

curl equivalent:

```bash
curl -sS -X POST "http://10.0.0.5:8080/upload?filename=report.pdf" \
  -H "Authorization: Bearer $MCP_TOKEN" \
  -H 'Content-Type: application/octet-stream' \
  --data-binary @report.pdf
```

| File lives on | Use |
|---|---|
| the MCP host (stdio, or a local HTTP server) | `attach_paths` — one call, no staging |
| another machine | `attach_upload_ids` — stage, then attach |

Rules: the id is **single-use**; unclaimed files expire after `MCP_UPLOAD_TTL` (15m); size is capped by
`REDMINE_MAX_UPLOAD` (25 MiB); uploads are refused in read-only mode. Design:
[docs/01_staging_upload.md](docs/01_staging_upload.md).

## Tools

| Tool | Writes | Notes |
|---|---|---|
| `whoami` | no | connectivity, login, read-only state, whether user names resolve |
| `list_projects` | no | id / identifier / name / status |
| `list_metadata` | no | trackers, statuses, priorities, activities, users |
| `search_issues` | no | filters + `q` full-text + `query_id` saved queries |
| `get_issue` | no | fields, description, attachments, relations, journals |
| `create_issue` | yes | optional attachments (`attach_paths` / `attach_upload_ids`) and `custom_fields` |
| `update_issue` | yes | fields, status, comment, progress, attachments, `custom_fields`; `confirm: true` to close or replace description |
| `log_time` | yes | hours against an issue |
| `list_time_entries` | no | whose entries are visible depends on permissions |

## Known limits

- Attaching on update is supported and **verified additive** on a live 3.4.x instance: existing attachments survive. The tool names every attachment it added, so a regression on another instance is visible. Use a test issue for the first run anyway.
- `private_notes` is sent when requested but 3.4 support is unverified (harmless if ignored).
- User-name resolution needs an admin API key (`/users.json` is admin-only); otherwise use numeric ids or `me`.
- `search_issues` with `q` cannot be combined with the other filters — different Redmine endpoint.
- Not yet tested against a live Redmine; the integration test in the design doc is still pending.

## License

MIT — see [LICENSE](LICENSE).
