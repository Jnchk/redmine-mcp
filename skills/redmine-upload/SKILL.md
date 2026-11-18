---
name: redmine-upload
description: Attach a file that lives on this machine to a Redmine issue through the mcp-gateway staging route. Use whenever the user says "attach/send/upload this file to the ticket" and the file is on this host.
---

# Attaching a local file to a Redmine issue (through mcp-gateway)

`create_issue`'s `attach_paths` reads files on the **upstream server's** host. When the file is on this
machine and the upstream is elsewhere, stage it through the gateway; the bytes travel over HTTP and never
through the model's context.

The helper is the neutral `mcp-upload` skill (in the `mcp-gateway` repository). When installed it lives at
`~/.config/opencode/skills/mcp-upload/scripts/mcp_upload.py`; this skill only documents the Redmine side.

## Two steps

```bash
python3 ~/.config/opencode/skills/mcp-upload/scripts/mcp_upload.py --server redmine --path ./report.pdf
# 9f1c0b7a4e2d4c3b8a5f6e7d0c1b2a39
```

Then pass that id through the gateway:

```js
await tools.gateway.call_tool({
  server: "redmine",
  tool: "create_issue",
  arguments: { project: "...", subject: "...", attach_upload_ids: ["9f1c0b7a4e2d4c3b8a5f6e7d0c1b2a39"] }
})
```

## Rules

- **Never base64 a file into a tool call.** The route exists exactly for that.
- The script reads `~/.config/mcp-gateway/config.json` (or `$MCP_GATEWAY_CONFIG`): it uses `listen`,
  `token`, and the `servers` map. `--server` must name one of those upstreams.
- The id is **single use**, expires (`MCP_UPLOAD_TTL`, 15 minutes by default), and belongs to the
  upstream instance that staged it. Do not reuse it on another `server`.
- Do not pass `--token` on the command line: it lands in shell history. It is read from the config.
- If the gateway and the upstream run on the same host, `attach_paths` is simpler and needs no script:

  ```js
  await tools.gateway.call_tool({ server: "redmine", tool: "create_issue",
    arguments: { project: "...", subject: "...", attach_paths: ["/abs/path/report.pdf"] } })
  ```

- `create_issue` and `update_issue` both accept `attach_upload_ids`; other tools do not. Adding to an
  existing issue leaves its current attachments in place:

  ```js
  await tools.gateway.call_tool({ server: "redmine", tool: "update_issue",
    arguments: { id: 17151, attach_upload_ids: ["9f1c0b7a4e2d4c3b8a5f6e7d0c1b2a39"] } })
  ```
- On failure the staged copy is deleted: upload again rather than retrying the id.

## Exit codes

`0` printed an id · `2` usage/config · `3` HTTP error (stderr has the server's message) · `4` local file
error.
