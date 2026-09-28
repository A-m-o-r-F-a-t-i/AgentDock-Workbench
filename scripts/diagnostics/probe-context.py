#!/usr/bin/env python3
"""Authenticated, bounded MCP context probe. Never retries a tools/call request."""
from __future__ import annotations

import argparse
import ipaddress
import http.client
import json
import pathlib
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

MAX_RESPONSE_BYTES = 2 << 20
CAPABILITY = "agentdock/context-response-v1"
HEADER = "X-AgentDock-Request-Id"


class ProbeError(Exception):
    def __init__(self, stage: str, code: str, request_id: str = "") -> None:
        super().__init__(code)
        self.stage, self.code, self.request_id = stage, code, request_id


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None  # urllib raises HTTPError; no redirected request is sent.


def validate_endpoint(endpoint: str) -> str:
    try:
        parsed = urllib.parse.urlsplit(endpoint)
        _ = parsed.port
    except ValueError:
        raise ProbeError("configuration", "invalid_endpoint") from None
    if parsed.username or parsed.password or parsed.query or parsed.fragment:
        raise ProbeError("configuration", "invalid_endpoint")
    if parsed.path != "/mcp" or not parsed.hostname:
        raise ProbeError("configuration", "endpoint_must_end_in_mcp")
    try:
        loopback = ipaddress.ip_address(parsed.hostname).is_loopback
    except ValueError:
        loopback = parsed.hostname.lower() == "localhost"
    if parsed.scheme != "https" and not (parsed.scheme == "http" and loopback):
        raise ProbeError("configuration", "https_required_for_remote_endpoint")
    return endpoint


class Probe:
    def __init__(self, endpoint: str, token: str = "", timeout: float = 15) -> None:
        self.endpoint = validate_endpoint(endpoint)
        if not 0 < timeout <= 120 or len(token) > 8192 or any(c in token for c in "\r\n\x00"):
            raise ProbeError("configuration", "invalid_timeout_or_token")
        self.token, self.timeout = token, timeout
        self.opener = urllib.request.build_opener(NoRedirect())
        self.session, self.protocol = "", ""
        self.samples: list[dict] = []

    def request(self, method: str, params: dict, stage: str, notification: bool = False) -> dict:
        request_id = "req_" + uuid.uuid4().hex
        payload = {"jsonrpc": "2.0", "method": method, "params": params}
        if not notification:
            payload["id"] = request_id
        headers = {"Content-Type": "application/json", "Accept": "application/json, text/event-stream", HEADER: request_id}
        if self.token:
            headers["Authorization"] = "Bearer " + self.token
        if self.protocol:
            headers["MCP-Protocol-Version"] = self.protocol
        if self.session:
            headers["Mcp-Session-Id"] = self.session
        req = urllib.request.Request(self.endpoint, data=json.dumps(payload).encode(), headers=headers, method="POST")
        start = time.monotonic()
        try:
            with self.opener.open(req, timeout=self.timeout) as response:
                raw = response.read(MAX_RESPONSE_BYTES + 1)
                echoed = response.headers.get(HEADER, "")
                session = response.headers.get("Mcp-Session-Id", "")
                status = response.status
        except urllib.error.HTTPError as error:
            error.close()
            raise ProbeError(stage, "http_" + str(error.code), request_id) from None
        except (urllib.error.URLError, TimeoutError, OSError, http.client.HTTPException):
            # A transport error does not establish whether the backend ran.
            raise ProbeError(stage, "transport_outcome_unknown_no_retry", request_id) from None
        if len(raw) > MAX_RESPONSE_BYTES:
            raise ProbeError(stage, "response_exceeds_limit", request_id)
        if session:
            if len(session) > 256 or not session.isascii() or any(ord(c) < 33 or ord(c) > 126 for c in session):
                raise ProbeError(stage, "invalid_session_header", request_id)
            self.session = session
        self.samples.append({"stage": stage, "request_id": request_id, "correlation_echoed": echoed == request_id,
                             "http_status": status, "http_body_bytes": len(raw), "elapsed_ms": round((time.monotonic()-start)*1000, 2)})
        if notification and not raw:
            return {}
        try:
            data = json.loads(raw)
        except (ValueError, UnicodeError):
            raise ProbeError(stage, "invalid_json_response", request_id) from None
        if not isinstance(data, dict) or data.get("jsonrpc") != "2.0" or data.get("id") != request_id:
            raise ProbeError(stage, "invalid_jsonrpc_response", request_id)
        if data.get("error") is not None:
            raise ProbeError(stage, "jsonrpc_error", request_id)
        result = data.get("result")
        if not isinstance(result, dict):
            raise ProbeError(stage, "invalid_result", request_id)
        return result

    def run(self, workdir: str = "", summary: bool = False) -> dict:
        initialized = self.request("initialize", {"protocolVersion": "2025-11-25", "capabilities": {},
                                   "clientInfo": {"name": "agentdock-context-probe", "version": "1"}}, "initialize")
        protocol = initialized.get("protocolVersion", "")
        if not isinstance(protocol, str) or len(protocol) > 40 or not all(c in "0123456789-" for c in protocol) or not protocol:
            raise ProbeError("initialize", "invalid_protocol_version")
        self.protocol = protocol
        self.request("notifications/initialized", {}, "initialized_notification", True)
        cursor, found = "", False
        for _ in range(8):
            result = self.request("tools/list", {"cursor": cursor} if cursor else {}, "tools_list")
            tools = result.get("tools")
            if not isinstance(tools, list):
                raise ProbeError("tools_list", "invalid_catalog")
            found = found or any(isinstance(tool, dict) and tool.get("name") == "agentdock_context" for tool in tools)
            cursor = result.get("nextCursor", "")
            if not isinstance(cursor, str) or len(cursor) > 4096:
                raise ProbeError("tools_list", "invalid_cursor")
            if found or not cursor:
                break
        if not found:
            raise ProbeError("tool_resolution", "context_tool_not_found_in_bounded_catalog")
        params: dict = {"name": "agentdock_context", "arguments": {"workdir": workdir} if workdir else {}}
        if summary:
            params["_meta"] = {CAPABILITY: {"structured": True, "text": "summary"}}
        result = self.request("tools/call", params, "context")
        if result.get("isError"):
            raise ProbeError("context", "tool_reported_error", self.samples[-1]["request_id"])
        structured = result.get("structuredContent")
        if not isinstance(structured, dict):
            raise ProbeError("context", "structured_context_missing")
        required = ("runtime", "rules", "skills", "instruction_files", "workspace")
        if any(key not in structured for key in required):
            raise ProbeError("context", "required_context_missing")
        diagnostics = structured.get("context_diagnostics", {})
        if not isinstance(diagnostics, dict) or not isinstance(structured["runtime"], dict):
            raise ProbeError("context", "invalid_context_shape")
        if diagnostics.get("complete") is not True:
            raise ProbeError("context", "context_incomplete")
        guidance = structured.get("agentdock_guidance", {})
        if not isinstance(guidance, dict):
            raise ProbeError("context", "invalid_guidance_shape")
        # Do not export rules, paths, token values, user messages or full results.
        return {"ok": True, "samples": self.samples, "core_version": structured["runtime"].get("version"),
                "call_id": guidance.get("call_id"), "request_id": guidance.get("request_id"),
                "context_complete": True, "context_response": result.get("_meta", {}).get(CAPABILITY, {})}


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--endpoint", required=True)
    parser.add_argument("--token-file", type=pathlib.Path)
    parser.add_argument("--timeout", type=float, default=15)
    parser.add_argument("--workdir", default="")
    parser.add_argument("--summary", action="store_true")
    args = parser.parse_args()
    try:
        token = ""
        if args.token_file:
            with args.token_file.open("r", encoding="utf-8") as stream:
                token = stream.read(8193)
            if len(token) > 8192:
                raise ProbeError("configuration", "token_file_exceeds_limit")
            token = token.strip()
        report = Probe(args.endpoint, token, args.timeout).run(args.workdir, args.summary)
    except ProbeError as error:
        print(json.dumps({"ok": False, "stage": error.stage, "code": error.code,
                          "request_id": error.request_id, "retryable": False}))
        return 1
    except (OSError, UnicodeError):
        print(json.dumps({"ok": False, "stage": "configuration", "code": "token_file_unreadable", "retryable": False}))
        return 1
    print(json.dumps(report, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    sys.exit(main())
