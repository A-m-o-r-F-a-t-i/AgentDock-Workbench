import importlib.util
import json
from pathlib import Path
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import unittest

spec = importlib.util.spec_from_file_location("context_probe", Path(__file__).parents[1]/"diagnostics"/"probe-context.py")
probe = importlib.util.module_from_spec(spec)
spec.loader.exec_module(probe)

class ProbeTests(unittest.TestCase):
    def setUp(self):
        self.calls = []
        self.mode = "normal"
        owner = self
        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args): pass
            def do_POST(self):
                owner.calls.append(self.path)
                body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
                if owner.mode == "redirect":
                    self.send_response(302); self.send_header("Location", "/leak"); self.end_headers(); return
                if owner.mode == "broken":
                    self.send_response(200); self.end_headers(); self.wfile.write(b"invalid"); return
                method = body["method"]
                if method == "notifications/initialized":
                    self.send_response(202); self.end_headers(); return
                if method == "initialize": result = {"protocolVersion": "2025-11-25"}
                elif method == "tools/list": result = {"tools": [{"name": "agentdock_context"}]}
                else:
                    result = {"structuredContent": {"runtime": {"version":"fixture"}, "rules":["private rule"],
                        "skills":[],"instruction_files":{},"workspace":{},"context_diagnostics":{"complete":True},
                        "agentdock_guidance":{"request_id":self.headers[probe.HEADER],"call_id":"call_fixture"}}}
                    if owner.mode == "tool_error": result["isError"] = True
                encoded = json.dumps({"jsonrpc":"2.0","id":body["id"],"result":result}).encode()
                self.send_response(200); self.send_header(probe.HEADER,self.headers[probe.HEADER]); self.end_headers(); self.wfile.write(encoded)
        self.server = ThreadingHTTPServer(("127.0.0.1",0),Handler)
        self.thread = threading.Thread(target=self.server.serve_forever,daemon=True); self.thread.start()
        self.endpoint = f"http://127.0.0.1:{self.server.server_port}/mcp"
    def tearDown(self):
        self.server.shutdown(); self.server.server_close(); self.thread.join()
    def test_success_is_bounded_and_does_not_export_rules(self):
        report=probe.Probe(self.endpoint,"test-token").run(summary=True)
        self.assertTrue(report["ok"]); self.assertEqual(len(self.calls),4)
        self.assertNotIn("private rule",json.dumps(report)); self.assertNotIn("test-token",json.dumps(report))
    def test_redirect_never_forwards_credentials(self):
        self.mode="redirect"
        with self.assertRaises(probe.ProbeError) as error: probe.Probe(self.endpoint,"test-token").run()
        self.assertEqual(error.exception.code,"http_302"); self.assertEqual(self.calls,["/mcp"])
    def test_invalid_response_is_not_retried(self):
        self.mode="broken"
        with self.assertRaises(probe.ProbeError): probe.Probe(self.endpoint).run()
        self.assertEqual(len(self.calls),1)
    def test_tool_failure_is_not_retried(self):
        self.mode="tool_error"
        with self.assertRaises(probe.ProbeError) as error: probe.Probe(self.endpoint).run()
        self.assertEqual(error.exception.stage,"context"); self.assertEqual(len(self.calls),4)
    def test_endpoint_and_payload_limits(self):
        for endpoint in ["http://remote.example/mcp","https://user:pass@remote.example/mcp","https://host/mcp?token=x","https://host:bad/mcp"]:
            with self.assertRaises(probe.ProbeError): probe.validate_endpoint(endpoint)
        with self.assertRaises(probe.ProbeError): probe.Probe(self.endpoint,"a"*8193)

if __name__=="__main__": unittest.main()
