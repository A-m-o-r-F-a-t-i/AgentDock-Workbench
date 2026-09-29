"""Isolated host executor regressions; no Android installation or production paths."""
import base64
import importlib.util
import json
from pathlib import Path
import sys
import tempfile
import time
import unittest

ROOT = Path(__file__).resolve().parents[3]
SPEC = importlib.util.spec_from_file_location("host_executor", ROOT / "mobile/android/termux/agentdock_host_executor.py")
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)

class HostExecutorTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.executor = MODULE.Executor(self.root / "jobs", "/bin/sh")
        self.identity = "session-" + "a" * 24
    def tearDown(self):
        self.temp.cleanup()
    def request(self, action="observe", **values):
        return self.executor.control(dict(protocol=1, action=action, operation_id=self.identity, **values))
    def start(self, command, timeout=1500, tty=False):
        spec = dict(backend="termux_host", command=command, workdir=str(self.root), env={}, timeout_ms=timeout, tty=tty)
        self.request("start", spec=spec)
        return spec
    def terminal(self, timeout=7):
        stop = time.monotonic() + timeout
        while time.monotonic() < stop:
            result = self.request()
            if result["state"] in ("exited", "cancelled", "timeout", "not_started"):
                return result
            time.sleep(.03)
        self.fail("supervised command did not settle")
    def test_probe_and_missing_observation(self):
        self.assertTrue(self.request("probe")["ready"])
        self.assertEqual("unknown", self.request()["state"])
    def test_nonzero_exit_and_stream_separation(self):
        self.start("printf output; printf error >&2; exit 7")
        result = self.terminal()
        self.assertEqual(7, result["exit_code"])
        self.assertEqual(b"output", base64.b64decode(result["stdout"]))
        self.assertEqual(b"error", base64.b64decode(result["stderr"]))
    def test_duplicate_start_never_executes_twice(self):
        spec = self.start("printf x >> count.txt")
        self.terminal()
        self.request("start", spec=spec)
        self.assertEqual("x", (self.root / "count.txt").read_text())
        with self.assertRaises(MODULE.ExecutorError):
            self.request("start", spec=dict(spec, command="printf y >> count.txt"))
    def test_deadline_and_owned_cancellation(self):
        self.start("sleep 20", timeout=150)
        self.assertEqual("timeout", self.terminal()["state"])
    def test_explicit_cancel(self):
        self.start("sleep 20", timeout=10000)
        self.request(cancel=True)
        self.assertEqual("cancelled", self.terminal()["state"])
    def test_stdin_is_acknowledged_once(self):
        self.start("cat", timeout=4000)
        value = [{"sequence": 1, "data": base64.b64encode(b"hello\n").decode()}]
        self.request(input=value)
        self.request(input=value, eof=True)
        result = self.terminal()
        self.assertEqual(1, result["input_applied"])
        self.assertEqual(b"hello\n", base64.b64decode(result["stdout"]))
    def test_full_stdin_pipe_cannot_disable_deadline(self):
        self.start("sleep 20", timeout=300)
        value = [{"sequence": 1, "data": base64.b64encode(b"x" * 16384).decode()}]
        self.request(input=value)
        self.assertEqual("timeout", self.terminal()["state"])
    def test_non_consuming_output_cursor(self):
        self.start("printf 'hello world'")
        self.terminal()
        first = self.request(stdout_offset=6)
        second = self.request(stdout_offset=6)
        self.assertEqual(first["stdout"], second["stdout"])
        self.assertEqual(b"world", base64.b64decode(first["stdout"]))
    def test_tty_has_real_terminal(self):
        self.start("test -t 1 && printf tty", tty=True)
        result = self.terminal()
        self.assertEqual(0, result["exit_code"])
        self.assertIn(b"tty", base64.b64decode(result["stdout"]))
    def test_invalid_directories_and_ids(self):
        with self.assertRaises(MODULE.ExecutorError): self.executor.directory("../x")
        with self.assertRaises(MODULE.ExecutorError): MODULE.validate_spec(dict(command="id", workdir="relative", env={}, timeout_ms=1, tty=False))
    def test_no_secret_environment_inheritance(self):
        import os
        os.environ["AGENTDOCK_TEST_SECRET"] = "must-not-leak"
        try:
            self.start("printf '%s' \"${AGENTDOCK_TEST_SECRET-unset}\"")
            self.assertEqual(b"unset", base64.b64decode(self.terminal()["stdout"]))
        finally:
            os.environ.pop("AGENTDOCK_TEST_SECRET", None)

if __name__ == "__main__":
    unittest.main(verbosity=2)
