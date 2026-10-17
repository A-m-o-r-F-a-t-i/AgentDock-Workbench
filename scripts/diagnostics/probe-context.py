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


def semantic_tokens(path: str) -> list[str]:
    if not path.startswith("/"):
        raise ProbeError("context", "invalid_semantic_path")
    if path == "/":
        return []
    return [part.replace("~1", "/").replace("~0", "~") for part in path[1:].split("/")]


def semantic_get(root: dict, tokens: list[str]):
    node = root
    for token in tokens:
        if isinstance(node, dict):
            if token not in node:
                return None
            node = node[token]
        elif isinstance(node, list) and token.isdigit():
            index = int(token)
            if index >= len(node):
                return None
            node = node[index]
        else:
            return None
    return node


def semantic_set(root: dict, tokens: list[str], value) -> None:
    if not tokens:
        if not isinstance(value, dict):
            raise ProbeError("context", "invalid_semantic_root")
        root.clear()
        root.update(value)
        return
    node = root
    for index, token in enumerate(tokens):
        last = index == len(tokens) - 1
        if isinstance(node, dict):
            if last:
                node[token] = value
                return
            child = node.get(token)
            want_list = tokens[index + 1].isdigit()
            if not isinstance(child, (dict, list)):
                child = [] if want_list else {}
                node[token] = child
            node = child
            continue
        if isinstance(node, list) and token.isdigit():
            position = int(token)
            while len(node) <= position:
                node.append(None)
            if last:
                node[position] = value
                return
            child = node[position]
            want_list = tokens[index + 1].isdigit()
            if not isinstance(child, (dict, list)):
                child = [] if want_list else {}
                node[position] = child
            node = child
            continue
        raise ProbeError("context", "invalid_semantic_container")


def apply_semantic_part(root: dict, part: dict) -> None:
    path = part.get("path")
    if not isinstance(path, str):
        raise ProbeError("context", "invalid_semantic_page")
    tokens = semantic_tokens(path)
    if isinstance(part.get("text"), str):
        current = semantic_get(root, tokens)
        if current is None:
            current = ""
        if not isinstance(current, str):
            raise ProbeError("context", "invalid_semantic_text_target")
        start = part.get("byte_start", 0)
        if not isinstance(start, int) or len(current.encode("utf-8")) != start:
            raise ProbeError("context", "semantic_text_cursor_mismatch")
        semantic_set(root, tokens, current + part["text"])
        return
    if "value" not in part:
        raise ProbeError("context", "invalid_semantic_page")
    semantic_set(root, tokens, part["value"])


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
        context_call_id, context_request_id = "", ""
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
        structured: dict = {}
        source_parts: list[str] = []
        source_mode = False
        snapshot = ""
        for page_index in range(512):
            result = self.request("tools/call", params, "context")
            if result.get("isError"):
                raise ProbeError("context", "tool_reported_error", self.samples[-1]["request_id"])
            page = result.get("structuredContent")
            if not isinstance(page, dict):
                raise ProbeError("context", "structured_context_missing")
            transport = result.get("_meta", {}).get("agentdock/transport", {})
            if not isinstance(transport, dict):
                transport = {}
            current_call = page.get("call_id") or transport.get("call_id")
            current_request = page.get("request_id") or transport.get("request_id") or self.samples[-1]["request_id"]
            if not context_call_id and isinstance(current_call, str):
                context_call_id = current_call
            if not context_request_id and isinstance(current_request, str):
                context_request_id = current_request
            if source_mode:
                text = page.get("content")
                if not isinstance(text, str):
                    raise ProbeError("context", "invalid_source_page")
                source_parts.append(text)
                if sum(map(len, source_parts)) > 16 << 20:
                    raise ProbeError("context", "retained_context_exceeds_limit")
                if not page.get("has_more"):
                    try:
                        structured = json.loads("".join(source_parts))
                    except (ValueError, TypeError):
                        raise ProbeError("context", "invalid_retained_context") from None
                    if not isinstance(structured, dict):
                        raise ProbeError("context", "invalid_retained_context")
                    break
            else:
                for key, value in page.items():
                    if key != "sections":
                        structured[key] = value
                # Reconstruct JSON-pointer parts exactly without exporting private text.
                sections = page.get("sections", [])
                if not isinstance(sections, list):
                    raise ProbeError("context", "invalid_semantic_page")
                for part in sections:
                    if not isinstance(part, dict):
                        raise ProbeError("context", "invalid_semantic_page")
                    apply_semantic_part(structured, part)
                delivery = page.get("delivery", {})
                if isinstance(delivery, dict) and not delivery.get("continuation_required") and not page.get("has_more"):
                    break
                current = page.get("structured_output", {}).get("snapshot_id", "")
                if snapshot and current and current != snapshot:
                    raise ProbeError("context", "context_snapshot_changed")
                snapshot = current or snapshot
                cursor = page.get("next_cursor")
                if isinstance(cursor, str) and cursor:
                    if len(cursor) > 4096:
                        raise ProbeError("context", "invalid_cursor")
                    arguments = {"cursor": cursor}
                    if workdir:
                        arguments["workdir"] = workdir
                    params = {"name": "agentdock_context", "arguments": arguments, "_meta": params.get("_meta", {})}
                    continue
            continuation = page.get("continue_read") or page.get("structured_output", {}).get("continue_read", {})
            arguments = continuation.get("arguments", {})
            if continuation.get("tool") != "read_file" or not str(arguments.get("path", "")).startswith("activity://call/"):
                raise ProbeError("context", "required_continuation_missing")
            source_mode = True
            params = {"name": "read_file", "arguments": arguments, "_meta": params.get("_meta", {})}
        else:
            raise ProbeError("context", "context_page_limit_exceeded")
        required = ("runtime", "rules", "instruction_files", "workspace")
        missing = [key for key in required if key not in structured]
        if missing:
            raise ProbeError("context", "required_context_missing_" + "_".join(missing))
        diagnostics = structured.get("context_diagnostics", {})
        if not isinstance(diagnostics, dict) or not isinstance(structured["runtime"], dict):
            raise ProbeError("context", "invalid_context_shape")
        if diagnostics.get("complete") is not True:
            raise ProbeError("context", "context_incomplete")
        if structured.get("bootstrap_complete") is not True or structured.get("context_ready") is not True:
            raise ProbeError("context", "bootstrap_not_ready")
        if not context_call_id or not context_request_id:
            raise ProbeError("context", "correlation_missing")
        # Do not export rules, paths, token values, user messages or full results.
        return {"ok": True, "samples": self.samples, "core_version": structured["runtime"].get("version"),
                "call_id": context_call_id, "request_id": context_request_id,
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
