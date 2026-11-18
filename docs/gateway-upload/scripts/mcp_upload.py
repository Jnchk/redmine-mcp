#!/usr/bin/env python3
"""Stage one local file through mcp-gateway and print the upload id.

The id is what an upstream `create_issue` takes as `attach_upload_ids`. The file is POSTed to the
gateway's `/upload/<server>` route, which forwards it to the upstream with the upstream credentials, so
the client never talks to the upstream directly.

Standard library only; runs on Linux, macOS and Windows.

Exit codes: 0 ok, 2 usage/config, 3 HTTP error, 4 local file error.
"""

from __future__ import annotations

import argparse
import json
import os
import sys
from http.client import HTTPConnection, HTTPSConnection, RemoteDisconnected
from pathlib import Path
from urllib.parse import quote, urlsplit

DEFAULT_TIMEOUT = 60.0
CHUNK = 64 * 1024


def message(text: str, code: int) -> int:
    print(text, file=sys.stderr)
    return code


def config_path(explicit: str | None) -> Path:
    if explicit:
        return Path(explicit).expanduser()
    override = os.environ.get("MCP_GATEWAY_CONFIG")
    if override:
        return Path(override).expanduser()
    base = os.environ.get("XDG_CONFIG_HOME")
    root = Path(base).expanduser() if base else Path.home() / ".config"
    return root / "mcp-gateway" / "config.json"


def load_config(path: Path) -> dict:
    if not path.exists():
        raise SystemExit(message(f"upload: cannot read {path}", 2))
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise SystemExit(message(f"upload: cannot read {path}: {exc}", 2))
    if not isinstance(data, dict):
        raise SystemExit(message(f"upload: {path} must contain a JSON object", 2))
    return data


def listen_base(cfg: dict) -> str:
    """Return http://host:port for the first listen address, preferring loopback."""
    entries = cfg.get("listen") or []
    if isinstance(entries, str):
        entries = [entries]
    candidates: list[str] = []
    for entry in entries:
        for addr in str(entry).split(","):
            addr = addr.strip()
            if addr:
                candidates.append(addr)
    for addr in candidates:
        host, _, port = addr.rpartition(":")
        if not port:
            continue
        host = host.strip("[]")
        if host in ("", "0.0.0.0", "::"):
            host = "127.0.0.1"
        return f"http://{host}:{port}"
    raise SystemExit(message("upload: gateway config has no usable listen address", 2))


def post(path: Path, url: str, token: str | None, filename: str, timeout: float) -> tuple[int, bytes]:
    parts = urlsplit(url)
    target = parts.path or "/"
    if parts.query:
        target += "?" + parts.query
    size = path.stat().st_size
    headers = {
        "Content-Type": "application/octet-stream",
        "Content-Length": str(size),
        "Accept": "application/json",
    }
    if token:
        headers["Authorization"] = "Bearer " + token

    last: Exception | None = None
    for attempt in (1, 2):
        conn_cls = HTTPSConnection if parts.scheme == "https" else HTTPConnection
        conn = conn_cls(parts.hostname, parts.port, timeout=timeout)
        try:
            conn.putrequest("POST", target, skip_accept_encoding=True)
            for key, value in headers.items():
                conn.putheader(key, value)
            conn.endheaders()
            with path.open("rb") as fh:
                while True:
                    chunk = fh.read(CHUNK)
                    if not chunk:
                        break
                    conn.send(chunk)
            resp = conn.getresponse()
            return resp.status, resp.read()
        except (RemoteDisconnected, ConnectionResetError, BrokenPipeError) as exc:
            last = exc
            if attempt == 2:
                raise
        finally:
            conn.close()
    raise last if last else RuntimeError("unreachable")


def main(argv: list[str] | None = None) -> int:
    if hasattr(sys.stdout, "reconfigure"):
        sys.stdout.reconfigure(encoding="utf-8")

    ap = argparse.ArgumentParser(
        prog="mcp_upload.py",
        description="Stage a local file through mcp-gateway and print its upload id.",
    )
    ap.add_argument("--path", required=True, help="local file to upload")
    ap.add_argument("--server", default="redmine", help="upstream name in the gateway config (default: redmine)")
    ap.add_argument("--filename", help="name shown in Redmine (default: the file's basename)")
    ap.add_argument("--url", help="gateway base URL, e.g. http://127.0.0.1:8930")
    ap.add_argument("--token", help="gateway token; prefer the config or MCP_GATEWAY_TOKEN")
    ap.add_argument("--config", help="path to the gateway config.json")
    ap.add_argument("--timeout", type=float, default=DEFAULT_TIMEOUT, help="seconds (default 60)")
    ap.add_argument("--json", action="store_true", help="print the whole response object")
    args = ap.parse_args(argv)

    src = Path(args.path).expanduser()
    try:
        if not src.exists():
            return message(f"upload: no such file: {src}", 4)
        if not src.is_file():
            return message(f"upload: not a regular file: {src}", 4)
        if src.stat().st_size == 0:
            return message(f"upload: refusing to send an empty file: {src}", 4)
    except OSError as exc:
        return message(f"upload: cannot read {src}: {exc}", 4)

    cfg_file = config_path(args.config)
    cfg = load_config(cfg_file)
    servers = cfg.get("servers") or {}
    if not isinstance(servers, dict):
        return message(f"upload: {cfg_file} has no servers object", 2)
    if args.server not in servers:
        known = ", ".join(sorted(servers)) or "none"
        return message(f'upload: unknown server "{args.server}" in {cfg_file}; known: {known}', 2)

    base = args.url or os.environ.get("MCP_GATEWAY_URL") or listen_base(cfg)
    token = args.token or os.environ.get("MCP_GATEWAY_TOKEN") or cfg.get("token")

    name = args.filename or src.name
    url = base.rstrip("/") + "/upload/" + quote(args.server, safe="") + "?filename=" + quote(name, safe="")

    try:
        status, body = post(src, url, token, name, args.timeout)
    except OSError as exc:
        return message(f"upload: cannot reach {urlsplit(url).netloc}: {exc}", 3)
    except Exception as exc:  # noqa: BLE001 - one line for the operator, never the token
        return message(f"upload: request failed: {exc}", 3)

    text = body.decode("utf-8", "replace").strip()
    if status < 200 or status >= 300:
        return message(f"upload: HTTP {status}: {text.splitlines()[0] if text else 'no body'}", 3)

    try:
        payload = json.loads(text)
        upload_id = payload["id"]
    except (json.JSONDecodeError, KeyError, TypeError):
        return message(f"upload: unexpected response: {text[:200]}", 3)

    if args.json:
        print(json.dumps(payload, ensure_ascii=False))
    else:
        print(upload_id)
    return 0


if __name__ == "__main__":
    sys.exit(main())
