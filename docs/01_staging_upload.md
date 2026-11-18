# redmine-mcp — Staging Upload Design

| | |
|---|---|
| Status | **Implemented** (`internal/upload`; client = mcp-gateway's `mcp-upload` skill) |
| Applies to | HTTP transport only (stdio keeps `attach_paths`) |
| Related | `00_basic_design.md` (server contract), `../README.md` (usage), mcp-gateway `skills/mcp-upload` |
| Language | Go 1.26.4 (server), Python 3.8+ (client helper, other repo) |

---

## 1. Problem

An agent writes a file on its own machine (a log, a diff, a screenshot) and wants it on an issue.
Today `attach_paths` is a **path on the process host**: correct for stdio (client == server), useless for
HTTP when the client runs on a different machine — the server resolves the path against **its own**
filesystem and fails with "no such file".

There is no viable inline alternative either:

- MCP request bodies are capped at 4 MiB (`00_basic_design.md` §2), and a 25 MiB attachment becomes
  ~33 MiB of base64 — worse, it must pass through the model's context.
- Base64 costs roughly one token per 3 bytes; a 5 MiB file is ~1.7M tokens. Not a trade-off, a wall.

**Goal:** move bytes client → server over HTTP, so the model only ever passes a short id.

**Non-goals:** multipart, resumable/chunked uploads, presigned URLs, per-user staging, attachment
on `update_issue` (still blocked by the 3.4 semantics in `00_basic_design.md` §7).

---

## 2. Flow

```
client (agent host)                 redmine-mcp (server host)              Redmine
────────────────────                ─────────────────────────              ───────
1. python3 mcp_upload.py --server redmine
   --path ./report.pdf  ──POST──►  /upload?filename=report.pdf
                                   ├ auth, size cap
                                   ├ mkdir <id>/, write content + filename
                                   └ return {"id": "<id>", ...}
        ◄── id (32 hex chars) ──
2. model: create_issue(..., attach_upload_ids: ["<id>"])
                                   ├ claim content -> content.claimed
                                   ├ POST /uploads.json ──────────────►
                                   │                          ◄── token
                                   ├ POST /issues.json (uploads: [token]) ─►
                                   │                          ◄── issue #42
                                   └ delete <id>/
        ◄── "#42 created ..." ─────
```

The helper posts to the **gateway**, which forwards the body to this route with that instance's
credentials; `curl` (§10) reaches the route directly. The rest of the diagram is the route's shape.

Three properties that matter:

1. **The model never sees the bytes.** Steps 1 and 2 are separate calls; only a 32-character id crosses.
2. **The staging id is single-use.** A second claim fails; nothing is silently attached twice.
3. **Failure is cheap.** If step 2 never happens, the file is garbage-collected after `MCP_UPLOAD_TTL`.

---

## 3. HTTP endpoint (normative)

Not part of MCP JSON-RPC: it is a plain route on the same listener, so it does not touch the
stateless MCP session, and it works with any client (curl, Python, PowerShell).

```
POST {MCP_UPLOAD_PATH}?filename=<name>
Authorization: Bearer <MCP_TOKEN>          (required when MCP_TOKEN is set)
Content-Type: application/octet-stream
Body: raw file bytes
```

| Aspect | Rule |
|---|---|
| Method | `POST` only. `GET`/`PUT`/`DELETE` → `405`. |
| Default path | `/upload`; must differ from `MCP_PATH` (default `/mcp`). |
| Body cap | `REDMINE_MAX_UPLOAD` (default 25 MiB), enforced with `http.MaxBytesReader`; overflow → `413`. |
| Content-Type | `application/octet-stream` only; anything else → `415`. |
| `filename` | Optional query param, **UTF-8 percent-encoded** (`?filename=%E6%97%A5%E5%BF%97.txt`). Missing/blank → `upload.bin`. Stored verbatim in `<id>/filename`; sanitised only at the Redmine boundary (§4.7). |
| Path validation | `MCP_UPLOAD_PATH` must start with `/`, must not be `/`, and must differ from `MCP_PATH`; otherwise startup fails with the reason. |
| Reverse proxy | Nginx/Envoy default body limits (1 MiB) reject before this server sees the request. Raise `client_max_body_size` to at least `REDMINE_MAX_UPLOAD`. |
| Server timeouts | The HTTP server must allow more than `ReadHeaderTimeout` for the body: set `ReadTimeout`/`WriteTimeout` to at least 120 s, or leave them unset for the upload route. A 25 MiB body over a slow link otherwise dies mid-stream. |
| Disk full mid-stream | Abort the request, remove the half-written `<id>/` directory, return `507`. Never leave a truncated payload behind. |
| Success | `201` + `application/json` |
| Auth | When `MCP_TOKEN` is set, every request must send `Authorization: Bearer <token>` (constant-time compare) or get `401`. When it is empty the route is **open** — same as `/mcp`, and a non-loopback listener logs a startup warning. |
| Read-only | `403` when `REDMINE_READ_ONLY` is on: a staged file can only be used by a write tool. |
| Transport | Route is absent under stdio (`MCP_TRANSPORT=stdio`) → `404`. |
| Disable | `MCP_UPLOAD_DIR=""` disables the route → `404`. |

Response body:

```json
{
  "id": "9f1c0b7a4e2d4c3b8a5f6e7d0c1b2a39",
  "filename": "report.pdf",
  "size": 24812,
  "sha256": "e3b0c44298fc1c149afbf4c8996fb924...",
  "expires_at": "2026-11-17T10:31:00Z"
}
```

| Status | Body | Cause |
|---|---|---|
| `201` | JSON above | stored |
| `400` | `upload: empty body` | zero-byte upload; Redmine rejects empty attachments anyway |
| `401` | `upload: unauthorized` | missing/wrong bearer |
| `403` | `upload: disabled in read-only mode (REDMINE_READ_ONLY)` | read-only |
| `413` | `upload: file exceeds 25 MiB (REDMINE_MAX_UPLOAD)` | body too large |
| `415` | `upload: Content-Type must be application/octet-stream` | wrong type |
| `507` | `upload: staging directory is full or unwritable` | disk problem |

Error bodies stay one line, start with `upload:`, and never echo the token.

---

## 4. Staging store (normative)

```
$MCP_UPLOAD_DIR/                       (per instance, from the env file: /tmp/redmine-mcp-neo)
    9f1c0b7a4e2d4c3b8a5f6e7d0c1b2a39/
        content                        payload bytes, mode 0600
        filename                       original name, raw UTF-8, no trailing newline
        content.part                   transient, while the body streams in
        content.claimed                transient, while Redmine is called
```

One directory per upload instead of sidecar files: the name is stored as **content**, not as a path, so
nothing the client sends can ever become a filesystem name. It also removes the JSON metadata file and its
encoding questions — `filename` is whatever bytes arrived, read back as-is.

Rules:

1. **Id = 16 random bytes, hex.** Validate with `^[0-9a-f]{32}$` before touching the filesystem. The id is
   the only path component that ever comes from the network, and the regex makes traversal impossible.
2. **One directory per instance, set by the operator.** `MCP_UPLOAD_DIR` comes from that instance's env
   file (`/tmp/redmine-mcp-neo`, `/tmp/redmine-mcp-sh`); there is no derivation and no defaulting beyond
   `os.TempDir()`. The resolved absolute path is logged at startup. Two instances sharing a directory
   would let one claim the other's ids — operator error, visible in the log.
3. **Create the directory with `O_EXCL` semantics.** `os.Mkdir(<id>)` fails if it exists; never reuse an
   id. Write `content.part`, `fsync`, rename to `content`, then write `filename`. A directory without
   `content` is incomplete and reaped by the sweeper.
4. **Claim = rename `content` → `content.claimed`.** Atomic; fails when the file is gone, so two concurrent
   `create_issue` calls cannot both win. Follow with `os.Chtimes(now)`: rename preserves mtime, and without
   the touch an old upload claimed just now could be reaped mid-call.
5. **Delete on both outcomes.** Success → remove the whole `<id>/` directory. Redmine failure → same. The
   caller re-uploads; keeping a reusable id after a failure would make "single-use" a lie.
6. **TTL `MCP_UPLOAD_TTL`** (default `15m`). A sweeper runs at startup and every 60 s and removes every
   `<id>/` directory whose **mtime** is older than the TTL — complete, claimed, partial, or content-only.
   It stops with the server's context; shutdown leaves files for the next start to reap.
7. **Filename stays raw until the Redmine boundary.** It is stored as the bytes that arrived (UTF-8, no
   trailing newline) and sanitised only when building `issue[uploads][][filename]`: strip `/` and `\`, drop
   control characters, cap at 200 bytes, fallback `upload.bin`. Chinese filenames must survive intact;
   the sanitised value is data, never a path.
8. **Never log bytes.** Log `id`, size, sha256 (first 12), remote address, and later the issue id.
9. **No quotas per client.** The token is shared by design (`00_basic_design.md` §1 non-goals); the size
   cap plus TTL is the containment. There is no rate limit; add one only if the instance is exposed
   beyond a trusted network.
10. **Per-host directories.** Every host runs its own server with its own directory; there is no shared
    storage and no cross-host failover in the deployment model. Callers talk to one instance, so the two
    calls always land on the host that holds the file.
11. **POSIX modes only.** 0700/0600 are enforced on Linux/macOS. On Windows the directory inherits the
    user's `%TEMP%` ACL, which is per-user; do not claim more than that.

---

## 5. Tool surface change

`create_issue` gains one optional field; nothing else changes:

```
create_issue(project, subject, description, tracker, priority, assignee, status,
             parent, start_date, due_date, estimated_hours,
             attach_paths []string,        # existing, process-host paths
             attach_upload_ids []string)   # new, staging ids
```

| Rule | Behaviour |
|---|---|
| Validation | Each id must match `^[0-9a-f]{32}$`; otherwise `redmine: invalid upload id "<x>": expected 32 hex characters.` |
| Order | `attach_paths` first, then `attach_upload_ids`, preserving order inside each list. |
| Duplicates | De-duplicated before claiming; a repeated id attaches once. |
| Missing / expired | `redmine: upload id <id> not found or expired (MCP_UPLOAD_TTL, default 15m). Re-upload the file.` |
| Both empty | No attachment work at all (unchanged behaviour). |
| stdio | `redmine: attach_upload_ids needs the HTTP transport; use attach_paths or switch to MCP_TRANSPORT=http.` |
| Read-only | Blocked earlier by `guardWrite()`, same as today. |
| Output | Unchanged: `#42 created (Bug, Website), 2 attachments. <url>` |

`update_issue` gains both fields too (it had the same need). The append-vs-replace question from
`00_basic_design.md` §7 is **settled**: verified on a live 3.4.x instance that `issue[uploads]` is additive
(two successive updates, the earlier attachment stayed), and UTF-8 filenames survive.

Two pieces of model-facing text change with the code, or nobody will ever find the route:

1. **Tool description** of `create_issue`: name both inputs and their scope —
   "`attach_paths`: files on this server's host; `attach_upload_ids`: ids from `POST /upload` on the same
   server, single-use, expire in `MCP_UPLOAD_TTL`."
2. **Server instructions** (`mcp.ServerOptions.Instructions`): one line — "If the file lives on the
   client machine, stage it through mcp-gateway's `mcp-upload` skill and pass the id;
   never base64 a file into a tool call."

Neither text may mention the token or repeat the flow in prose; the helper's `--help` is the manual.

`whoami` gains one line so an operator can see the route without guessing:

```
staging: /upload -> /tmp/redmine-mcp-neo (ttl 15m)
staging: disabled
```

---

## 6. Configuration

| Var | Default | Meaning |
|---|---|---|
| `MCP_UPLOAD_DIR` | `os.TempDir()/redmine-mcp-uploads` | Staging directory. **Set one per instance**, e.g. `/tmp/redmine-mcp-neo`. Empty string disables the endpoint. Created on startup with mode 0700; the resolved absolute path is logged. |
| `MCP_UPLOAD_PATH` | `/upload` | Route path on the HTTP listener. Must differ from `MCP_PATH` and must not be `/`. |
| `MCP_UPLOAD_TTL` | `15m` | Time an unclaimed staged file survives. `0` means the default; a negative value fails startup. |
| `REDMINE_MAX_UPLOAD` | `25 MiB` | Reused: caps both `attach_paths` and uploads. One knob, one number to explain. |

These belong in the same env file that already holds `REDMINE_URL` / `MCP_TOKEN`, so one file describes one
instance:

```bash
# env/neo.env
REDMINE_URL=https://redmine.example.com
MCP_TRANSPORT=http
MCP_ADDR=127.0.0.1:8888,10.0.0.5:8888
# MCP_TOKEN=...                       # optional; omit on a trusted LAN (startup warning, no auth)
MCP_UPLOAD_DIR=/tmp/redmine-mcp-neo
```

One env file per instance. The `--server` name the helper uses is the gateway upstream name (§7.1); it is
deliberately **not** tied to any agent's config key, so the same name works
under every client.

Startup: create the directory, warn (not fail) when it lives on a world-writable path, remove expired
files, start the sweeper. No new flag.

---

## 7. Client helper — mcp-gateway's `mcp-upload` skill

The client side is **not** in this repository. It is the neutral `mcp-upload` skill of
[mcp-gateway](https://github.com/Jnchk/mcp-gateway): `skills/mcp-upload/scripts/mcp_upload.py`. It stages a
file for any upstream implementing that gateway's `POST /upload/<server>` route, so `--server redmine` is
just the gateway entry name. One copy in one repository, one release cadence; this repository documents
only the route it serves.

**Why Python:** the agent runs on Windows, macOS and Linux; a shell script would not. Stdlib only
(`urllib`, `argparse`, `pathlib`), no `pip install`, invoked as
`python3 skills/mcp-upload/scripts/mcp_upload.py` (`python` on Windows).

```
mcp_upload.py --path FILE --server NAME [--filename NAME]
              [--url URL] [--token TOKEN] [--config PATH] [--json] [--timeout SECONDS]
```

| Flag | Default | Notes |
|---|---|---|
| `--path` | required | Any local path. `pathlib` handles `/` and `\`. |
| `--server` | required | Upstream name as the gateway knows it: `redmine`. |
| `--filename` | basename of `--path` | Overrides the name Redmine shows. |
| `--url` | `$MCP_GATEWAY_URL`, then client config | **Gateway** base URL, e.g. `http://10.0.0.5:8930`; the helper appends `/upload/<server>`. |
| `--token` | `$MCP_GATEWAY_TOKEN`, then client config | Gateway token; never printed, never logged. |
| `--config` | `$MCP_GATEWAY_CONFIG`, else `~/.config/mcp-gateway/client.json` | The client's own file; no agent config is ever read. |
| `--json` | off | Print the full response object instead of the bare id. |
| `--timeout` | `60` | Seconds. |

### 7.1 Where the URL and token come from

The helper reads **its own client config**, never an agent config:

`$MCP_GATEWAY_CONFIG` → `$XDG_CONFIG_HOME/mcp-gateway/client.json` → `~/.config/mcp-gateway/client.json`
(Windows: `%USERPROFILE%\.config\mcp-gateway\client.json`); `--config PATH` overrides.

```json
{
  "url": "http://127.0.0.1:8930",
  "token": "28c8...",
  "servers": { "redmine": true, "redmine-sh": true }
}
```

`servers` is optional: when present, `--server` must name one of its keys. The file carries client
credentials only — gateway address and token. Upstream credentials (Redmine's API key included) live in
the gateway's own `config.json` and never reach the client. When `url` is absent the helper falls back to
the `listen` addresses in that same file.

Precedence, per value:

| # | URL | Token |
|---|---|---|
| 1 | `--url` | `--token` |
| 2 | `MCP_GATEWAY_URL` | `MCP_GATEWAY_TOKEN` |
| 3 | `client.json` → `url`, else `listen` | `client.json` → `token` |

Rules:

1. The URL is the **gateway**, not this server; the gateway forwards to `MCP_UPLOAD_PATH` (§6) with the
   upstream's credentials.
2. **`token` is optional** on both sides. When the gateway runs without one (§3), omit the field. When it
   is present, the helper sends `Authorization: Bearer <token>`; a mismatch surfaces as `401`.
3. A missing config file is an error (`exit 2`); flags and environment alone are not enough — the helper
   needs a gateway URL.
4. Nothing is ever written back to the file.

Deliberately not the agent config: four clients ship four formats (`opencode.json`, `.mcp.json`,
`mcp.json`, `config.toml`) with different interpolation rules, and that file is not ours.

Contract, because an agent parses it:

1. **stdout is exactly one line**: the id, or the JSON object with `--json`. Nothing else.
2. **stderr carries progress and errors**; exit codes are meaningful:

| Code | Meaning |
|---|---|
| `0` | printed an id |
| `2` | bad arguments (missing `--path`, unreadable client config, unknown `--server`, no usable URL) |
| `3` | HTTP error; stderr is the gateway's or server's one-line message verbatim |
| `4` | local file error (missing, directory, unreadable, empty) |

3. **Streams the body** in 64 KiB chunks with `Content-Length` from `os.stat` — no full load into RAM,
   no base64.
4. **One retry** on connection reset; a `POST /upload` is a fresh staging object, so the retry cannot
   duplicate an attachment.
5. **Never sends `Expect: 100-continue`** (some proxies in corporate networks mishandle it).
6. **UTF-8 output**, `sys.stdout.reconfigure(encoding="utf-8")` on 3.7+ where available; the id is ASCII
   anyway.
7. **Percent-encodes `--filename`** with `urllib.parse.quote(name, safe="")`; a Chinese filename must
   arrive intact (the server decodes it into `issue[uploads][][filename]`).
8. **Windows**: uses `pathlib` for the local path and `os.stat` for the size; no `/dev/stdin`, no shell
   redirection, no `curl` dependency.

The skill that wraps this is thin by design:

```
SKILL.md: when the file is not on the MCP host, run mcp-gateway's mcp-upload skill
          (skills/mcp-upload/scripts/mcp_upload.py) with --server redmine, take the id from stdout,
          pass it as attach_upload_ids. Never base64 a file into a tool call.
```

---

## 8. Security

| Concern | Mitigation |
|---|---|
| Unauthorised upload | When `MCP_TOKEN` is set: bearer token, constant-time compare, `401` otherwise. When it is unset the server is unauthenticated by design and logs a warning on a non-loopback listener (`config.UnauthenticatedListenAddr`). |
| Path traversal | No path comes from the network: the only variable component is `id` (regex-validated hex) and `filename` is stored as **file content**, never as a name. |
| Disk exhaustion | `REDMINE_MAX_UPLOAD` per request + `MCP_UPLOAD_TTL` sweeper + 0700/0600 modes. No per-token quota by design. |
| Staging used as a file drop | Files are only reachable through `attach_upload_ids`; there is no download route, and unclaimed files die in 15 minutes. |
| Token leakage | Never echoed; `upload:` errors carry no credentials; helper masks it. |
| Write escalation | The route is refused entirely in read-only mode. |
| 3.4 attachment-set replacement | Unchanged: uploads only feed `create_issue`, never `update_issue`. |

Trust boundary statement for `docs/00_basic_design.md` §9: **the upload route grants "stage a file", not
"attach a file"** — attaching still requires a write tool call, which the write guard controls.

---

## 9. Failure modes

| Situation | Server | Caller sees |
|---|---|---|
| Client dies mid-upload | the incomplete `<id>/` directory is removed by the sweeper | nothing; retry |
| `create_issue` called twice with one id | first wins the rename, second loses | `upload id <id> not found or expired ...` |
| Redmine upload fails | the staged directory is deleted | `redmine: ...` from the existing error model |
| Issue create fails after upload token obtained | staged directory deleted; Redmine has an orphan upload token, harmless | `redmine: 422 ...` |
| Server restarts between the two calls | files survive if `/tmp` is disk-backed; **a tmpfs or a host reboot clears them** | works, or a clear "not found or expired" and a re-upload |
| Clock skew | TTL compares against the server's own file mtime, never a client-supplied time | unaffected by client clocks |
| `MCP_UPLOAD_DIR` unwritable | `507` at upload time | `upload: staging directory is full or unwritable` |
| Reverse proxy body limit | proxy returns 413/HTML before this server sees the request | the helper reports the HTTP status and the first line of the body; raise `client_max_body_size` |
| Two instances share one `MCP_UPLOAD_DIR` | an id staged for one Redmine is claimable by the other | possible wrong-file attachment — one directory per instance is an env-file setting (§4.2), the startup log shows the path |

---

## 10. Testing

Unit (`httptest`, in-memory fake Redmine, same style as the existing suites):

1. upload → `201`, id matches `^[0-9a-f]{32}$`, `<id>/content` size equals the body, `<id>/filename` holds the
   raw name.
2. oversize body → `413`; no `<id>/` directory left behind.
3. wrong `Content-Type` → `415`; unsigned/wrong bearer → `401`.
4. read-only mode → `403`.
5. `MCP_UPLOAD_DIR=""` → `404`.
6. filename `../../etc/passwd` → stored as data, then sanitised to `passwd` for Redmine; never a path.
7. `create_issue` with `attach_upload_ids` → two POSTs observed (`/uploads.json`, `/issues.json`), the staged
   directory is gone afterwards.
8. same id twice → second call fails, one attachment total.
9. expired id (TTL 1 ms in config) → clear error.
10. `attach_paths` and `attach_upload_ids` together → order preserved.
11. sweeper removes complete, partial and claimed `<id>/` directories older than the TTL, and leaves fresh ones.
12. UTF-8 filename from the query (`?filename=%E6%97%A5%E5%BF%97.txt`) reaches the issue payload as `日志.txt`.
13. two instances with different `MCP_UPLOAD_DIR` cannot see each other's ids.
14. client aborts mid-body → the incomplete `<id>/` directory is removed, no attachment possible.
15. zero-byte body → `400 upload: empty body`, nothing stored.
16. `whoami` prints `staging: <path> -> <dir> (ttl 15m)`, or `staging: disabled` when the dir is empty.

Helper — lives in mcp-gateway as `skills/mcp-upload/scripts/`, tests included there:

17. against a stub HTTP server: prints one line, exit `0`.
18. HTTP 413 → exit `3`, stderr contains the server message, stdout empty.
19. missing file → exit `4`.
20. token never appears in stdout/stderr.
21. `--filename 日志.txt` is percent-encoded on the wire and decoded by the server stub.
22. `--server redmine` resolves `url` and `token` from a fixture `client.json`; without `token` no
    `Authorization` header is sent; an unknown `--server` exits `2` and lists the known names.

Manual smoke:

```bash
curl -sS -X POST "http://127.0.0.1:8080/upload?filename=report.pdf" \
  -H "Authorization: Bearer $MCP_TOKEN" \
  -H 'Content-Type: application/octet-stream' \
  --data-binary @report.pdf
# from your mcp-gateway clone
python3 skills/mcp-upload/scripts/mcp_upload.py --server redmine --path ./report.pdf \
  --url http://127.0.0.1:8930 --token "$GATEWAY_TOKEN"
```

---

## 11. Compatibility and rollout

- **Additive.** Existing clients keep using `attach_paths`; no tool loses a field.
- The route is inert until called. `MCP_UPLOAD_DIR=""` turns it off completely for conservative
  deployments.
- `docs/00_basic_design.md` gains the three config vars, the `attach_upload_ids` input, and the security
  sentence, in the same change that lands the code (per `AGENTS.md`).
- Same pattern is portable to the sibling servers (`reviewboard` diff upload, `obsitrace` note import):
  keep the endpoint shape identical and add the upstream to the gateway. **One** helper serves them all —
  it lives in `mcp-gateway/skills/mcp-upload/` and takes `--server`; vendoring a copy into each repo was
  the earlier plan and was dropped (§7), because copies drift.
- The helper reads only its own `~/.config/mcp-gateway/client.json` plus environment, never an agent's
  config, so it works the same under OpenCode, Claude Code, Cursor or a plain shell.

---

## 12. Open questions

1. `PUT /upload` (idempotent retry) instead of `POST`? Deferred: `POST` + one client retry is enough, and
   `PUT` would need a caller-chosen id.
2. `GET /upload/<id>` for status checks? Deferred: the helper prints the id immediately; a status route
   adds a second trust surface for no current need.
3. Rate limiting? Not implemented. The size cap, TTL and shared token are the containment; revisit if an
   instance is exposed beyond a trusted network.

**Resolved**

- Helper location: mcp-gateway's `skills/mcp-upload/` (single source, `--server` selects the upstream) —
  not vendored here (§7).
- Reuse after a failed attach: refused; the staged directory is deleted and the caller re-uploads (§4.5).
- Transport encoding: raw `application/octet-stream`, **no multipart** — the only caller is the helper and
  `curl --data-binary` (§3).
- Token: flags → environment → the helper's own `client.json`, where it is optional and entered once;
  **no agent config is read** (§7.1). The server accepts unauthenticated requests when `MCP_TOKEN` is
  unset (§3).
- Replication: not in the deployment model. Each host runs its own instance with a local directory (§4.10).

---

## 13. As built

| Path | Role |
|---|---|
| `internal/upload/store.go` | staging store: Save / Claim / Remove / Sweep / Run, `safeName`, `ValidID` |
| `internal/upload/handler.go` | `POST` route, status mapping (`413` / `415` / `400` / `403` / `507`) |
| `internal/upload/store_test.go` | save/claim/remove, limits, sweep, disabled, handler matrix |
| `internal/redmine/upload.go` | `UploadFile(ctx, name, r, size)`; `Upload(path)` now delegates |
| `internal/tools/issues.go` | `attach_upload_ids`, `claimUploads` / `releaseUploads` |
| `internal/tools/meta.go` | `whoami` prints `staging: …` |
| `cmd/redmine-mcp/main.go` | store creation, sweeper goroutine, route registration, startup log |
| `skills/redmine/SKILL.md` | the agent-facing recipe for this server |
| mcp-gateway `skills/mcp-upload/` | the helper (stdlib, streaming) and its tests — other repository |

Verified end to end: real binary + fake Redmine + real helper — upload → `create_issue` → Redmine receives
`uploads[0].token`, staging directory removed, `whoami` shows the staging line.
