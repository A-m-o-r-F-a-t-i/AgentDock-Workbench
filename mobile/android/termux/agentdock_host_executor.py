#!/data/data/com.termux/files/usr/bin/python
"""Opt-in Termux host executor. Separate from deployment operations and secrets.

RUN_COMMAND invokes short control requests; an owned supervisor runs each command.
Operation IDs are never replayed, including after a supervisor or APK restart.
"""
from __future__ import annotations
import base64
import hashlib
import json
import os
from pathlib import Path
import re
import select
import signal
import subprocess
import sys
import threading
import time

PROTOCOL = 1
MAX_REQUEST = 96 * 1024
MAX_OUTPUT = 4 * 1024 * 1024
MAX_CHUNK = 16 * 1024
MAX_JOBS = 128
IDENTITY = re.compile(r"session-[a-f0-9]{24}\Z")
TERMUX_SHELL = "/data/data/com.termux/files/usr/bin/bash"

class ExecutorError(Exception):
    pass

def read_json(path: Path, limit: int = MAX_REQUEST) -> dict:
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(fd, "rb") as source:
        raw = source.read(limit + 1)
    if len(raw) > limit:
        raise ExecutorError("record_too_large")
    value = json.loads(raw)
    if not isinstance(value, dict):
        raise ExecutorError("invalid_record")
    return value

def atomic(path: Path, value: dict) -> None:
    raw = json.dumps(value, ensure_ascii=False, separators=(",", ":"), allow_nan=False).encode()
    if len(raw) > MAX_REQUEST:
        raise ExecutorError("record_too_large")
    if path.is_symlink():
        raise ExecutorError("unsafe_record")
    temporary = path.with_name(path.name + "." + os.urandom(8).hex() + ".tmp")
    fd = os.open(temporary, os.O_CREAT | os.O_EXCL | os.O_WRONLY | os.O_NOFOLLOW, 0o600)
    try:
        with os.fdopen(fd, "wb") as target:
            target.write(raw)
            target.flush()
            os.fsync(target.fileno())
        os.replace(temporary, path)
    finally:
        temporary.unlink(missing_ok=True)

def validate_spec(value: dict) -> dict:
    if set(value) - {"backend", "command", "workdir", "env", "timeout_ms", "tty"}:
        raise ExecutorError("invalid_spec")
    command, cwd, env = value.get("command"), value.get("workdir"), value.get("env") or {}
    if not isinstance(command, str) or not 0 < len(command.encode()) <= 16384 or "\0" in command:
        raise ExecutorError("invalid_command")
    if not isinstance(cwd, str) or not cwd.startswith("/") or os.path.normpath(cwd) != cwd or "\0" in cwd:
        raise ExecutorError("invalid_workdir")
    timeout = value.get("timeout_ms")
    if type(timeout) is not int or not 1 <= timeout <= 86400000 or type(value.get("tty")) is not bool:
        raise ExecutorError("invalid_timeout_or_tty")
    if not isinstance(env, dict) or len(env) > 64:
        raise ExecutorError("invalid_environment")
    for key, item in env.items():
        if not isinstance(key, str) or not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]{0,127}", key) or not isinstance(item, str) or "\0" in item:
            raise ExecutorError("invalid_environment")
    if sum(len(k.encode()) + len(v.encode()) for k, v in env.items()) > 16384:
        raise ExecutorError("environment_too_large")
    return dict(value, env=env)

class Executor:
    def __init__(self, root: Path, shell: str = TERMUX_SHELL):
        self.root, self.shell = root, shell
        if root.is_symlink():
            raise ExecutorError("unsafe_root")
        root.mkdir(mode=0o700, parents=True, exist_ok=True)
        os.chmod(root, 0o700)

    def directory(self, identity: str) -> Path:
        if not isinstance(identity, str) or not IDENTITY.fullmatch(identity):
            raise ExecutorError("invalid_operation_id")
        target = self.root / identity
        if target.is_symlink():
            raise ExecutorError("unsafe_operation")
        return target

    def start(self, identity: str, spec: dict) -> None:
        spec = validate_spec(spec)
        if spec.get("backend") != "termux_host":
            raise ExecutorError("wrong_backend")
        target = self.directory(identity)
        fingerprint = hashlib.sha256(json.dumps(spec, sort_keys=True).encode()).hexdigest()
        if target.exists():
            previous = read_json(target / "request.json")
            if previous["fingerprint"] != fingerprint:
                raise ExecutorError("operation_conflict")
            return  # Never launch a second process for an existing operation.
        entries = list(self.root.iterdir())
        if len(entries) >= MAX_JOBS:
            # Preserve active/unknown records; callers explicitly inspect them.
            for entry in entries:
                if entry.is_symlink() or not entry.is_dir():
                    continue
                try:
                    state = read_json(entry / "state.json")
                    if state.get("state") not in ("exited", "cancelled", "timeout", "not_started") or time.time() - state.get("updated", 0) < 3600:
                        continue
                    for item in entry.iterdir():
                        if item.is_file() and not item.is_symlink():
                            item.unlink()
                    entry.rmdir()
                except (OSError, ValueError, ExecutorError):
                    continue
            if len(list(self.root.iterdir())) >= MAX_JOBS:
                raise ExecutorError("capacity")
        target.mkdir(mode=0o700)
        atomic(target / "request.json", {"spec": spec, "shell": self.shell, "fingerprint": fingerprint})
        atomic(target / "state.json", {"state": "queued", "input_applied": 0, "updated": time.time()})
        # The supervisor owns command timeouts even when the APK or Core vanishes.
        # No Core/worker credentials are inherited through this request or argv.
        try:
            subprocess.Popen([sys.executable, str(Path(__file__).resolve()), "--supervise", str(target)],
                             stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                             start_new_session=True, close_fds=True)
        except OSError:
            atomic(target / "state.json", {"state": "not_started", "exit_code": -1, "input_applied": 0, "updated": time.time()})
            raise ExecutorError("supervisor_start_failed")

    def control(self, request: dict) -> dict:
        if request.get("protocol") != PROTOCOL:
            raise ExecutorError("protocol_mismatch")
        if request.get("action") == "probe":
            return {"protocol": PROTOCOL, "ready": Path(self.shell).is_file(), "uid": os.getuid(), "state": "verified" if Path(self.shell).is_file() else "shell_missing"}
        identity = request.get("operation_id")
        target = self.directory(identity)
        action = request.get("action")
        if action == "start":
            self.start(identity, request["spec"])
        elif action != "observe":
            raise ExecutorError("invalid_action")
        if not target.exists():
            return {"state": "unknown", "input_applied": 0, "stdout": "", "stderr": "", "missing": True}
        if request.get("cancel") is True:
            atomic(target / "cancel.json", {"cancel": True})
        inputs = request.get("input") or []
        if not isinstance(inputs, list) or len(inputs) > 4:
            raise ExecutorError("input_limit")
        for item in inputs:
            seq = item.get("sequence")
            if type(seq) is not int or not 1 <= seq <= 1000000:
                raise ExecutorError("invalid_input_sequence")
            data = base64.b64decode(item.get("data", ""), validate=True)
            if len(data) > MAX_CHUNK:
                raise ExecutorError("input_limit")
            file = target / f"input-{seq:08d}.json"
            delivered = target / f"delivering-{seq:08d}.json"
            state = read_json(target / "state.json")
            if seq <= state.get("input_applied", 0):
                continue
            if file.exists() or delivered.exists():
                previous = read_json(file if file.exists() else delivered)
                if previous != item:
                    raise ExecutorError("input_conflict")
                continue
            # A durable reservation is installed before the supervisor can write
            # bytes. Existing reservations are not replayed after a crash.
            raw = json.dumps(item).encode()
            fd = os.open(file, os.O_CREAT | os.O_EXCL | os.O_WRONLY | os.O_NOFOLLOW, 0o600)
            with os.fdopen(fd, "wb") as stream:
                stream.write(raw); stream.flush(); os.fsync(stream.fileno())
        if request.get("eof") is True:
            atomic(target / "eof.json", {"eof": True})
        state = read_json(target / "state.json")
        result = {"state": state["state"], "input_applied": state.get("input_applied", 0), "output_limited": state.get("output_limited", False)}
        remaining = False
        for name in ("stdout", "stderr"):
            offset = request.get(name + "_offset", 0)
            if type(offset) is not int or not 0 <= offset <= MAX_OUTPUT:
                raise ExecutorError("invalid_output_offset")
            file = target / (name + ".bin")
            data = b""
            if file.exists():
                fd = os.open(file, os.O_RDONLY | os.O_NOFOLLOW)
                with os.fdopen(fd, "rb") as stream:
                    length = os.fstat(stream.fileno()).st_size
                    if offset > length:
                        raise ExecutorError("output_cursor_ahead")
                    stream.seek(offset); data = stream.read(MAX_CHUNK)
                    remaining |= offset + len(data) < length
            result[name] = base64.b64encode(data).decode()
        if remaining and state["state"] in ("exited", "cancelled", "timeout", "not_started"):
            result["state"] = "running"  # Drain retained bytes before terminal receipt.
        elif "exit_code" in state:
            result["exit_code"] = state["exit_code"]
        if result["state"] in ("queued", "running"):
            stamp = state.get("supervisor_stamp")
            if stamp and process_stamp(state.get("supervisor_pid", 0)) != stamp:
                result["state"] = "unknown"
        return result

def process_stamp(pid: int) -> str:
    try:
        if type(pid) is not int or pid <= 1:
            return ""
        fields = Path(f"/proc/{pid}/stat").read_text().rsplit(") ", 1)[1].split()
        boot = Path("/proc/sys/kernel/random/boot_id").read_text().strip()
        return f"{pid}:{fields[19]}:{boot}"
    except (OSError, IndexError):
        return ""

def supervise(target: Path) -> None:
    record = read_json(target / "request.json")
    spec = validate_spec(record["spec"])
    state = {"state": "running", "input_applied": 0, "supervisor_pid": os.getpid(),
             "supervisor_stamp": process_stamp(os.getpid()), "updated": time.time()}
    environment = {key: value for key, value in os.environ.items() if key in ("PATH", "HOME", "PREFIX", "LANG", "LC_ALL", "TMPDIR", "TERM")}
    environment.update(spec["env"])
    master = slave = None
    child = None
    try:
        if spec["tty"]:
            master, slave = os.openpty()
            child = subprocess.Popen([record["shell"], "-lc", spec["command"]], cwd=spec["workdir"], env=environment,
                                     stdin=slave, stdout=slave, stderr=slave, start_new_session=True, close_fds=True)
            os.close(slave); slave = None
            input_fd = master
            readers = [(master, "stdout")]
        else:
            child = subprocess.Popen([record["shell"], "-lc", spec["command"]], cwd=spec["workdir"], env=environment,
                                     stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True, close_fds=True)
            input_fd = child.stdin.fileno()
            readers = [(child.stdout.fileno(), "stdout"), (child.stderr.fileno(), "stderr")]
        atomic(target / "state.json", state)
        deadline = time.monotonic() + spec["timeout_ms"] / 1000
        limited = threading.Event()
        stop_readers = threading.Event()
        os.set_blocking(input_fd, False)
        lock = threading.Lock()
        total = [0]
        def drain(fd: int, name: str) -> None:
            stream_fd = os.open(target / (name + ".bin"), os.O_CREAT | os.O_EXCL | os.O_WRONLY | os.O_NOFOLLOW, 0o600)
            with os.fdopen(stream_fd, "wb", buffering=0) as output:
                while not stop_readers.is_set():
                    try:
                        readable, _, _ = select.select([fd], [], [], 0.1)
                        if not readable: continue
                        data = os.read(fd, 16384)
                    except BlockingIOError: continue
                    except OSError: break
                    if not data: break
                    with lock:
                        kept = data[:max(0, MAX_OUTPUT - total[0])]
                        total[0] += len(kept)
                        if len(kept) < len(data): limited.set()
                    output.write(kept)
        threads = [threading.Thread(target=drain, args=reader, daemon=True) for reader in readers]
        for thread in threads: thread.start()
        cancelled = timed_out = False
        input_closed = False
        pending_input = None
        while child.poll() is None:
            cancelled = (target / "cancel.json").exists()
            timed_out = time.monotonic() >= deadline
            if cancelled or timed_out or limited.is_set():
                os.killpg(child.pid, signal.SIGTERM)
                try: child.wait(timeout=1)
                except subprocess.TimeoutExpired: os.killpg(child.pid, signal.SIGKILL)
                break
            seq = state["input_applied"] + 1
            file = target / f"input-{seq:08d}.json"
            if pending_input is None and file.exists() and not input_closed:
                delivering = target / f"delivering-{seq:08d}.json"
                os.replace(file, delivering)
                data = base64.b64decode(read_json(delivering)["data"], validate=True)
                pending_input = (seq, delivering, memoryview(data))
            if pending_input is not None:
                seq, delivering, view = pending_input
                try:
                    written = os.write(input_fd, view) if view else 0
                except BlockingIOError:
                    written = 0
                view = view[written:]
                if view:
                    pending_input = (seq, delivering, view)
                else:
                    state["input_applied"] = seq
                    state["updated"] = time.time()
                    atomic(target / "state.json", state)
                    delivering.unlink()
                    pending_input = None
            if not spec["tty"] and not input_closed and pending_input is None and (target / "eof.json").exists() and not list(target.glob("input-*.json")):
                child.stdin.close(); input_closed = True
            time.sleep(0.025)
        code = child.wait(timeout=3)
        # No detached descendants are left behind by this finite owned session.
        try: os.killpg(child.pid, signal.SIGKILL)
        except ProcessLookupError: pass
        for thread in threads: thread.join(timeout=2)
        stop_readers.set()
        for thread in threads: thread.join(timeout=0.3)
        state.update(state="timeout" if timed_out else "cancelled" if cancelled else "exited",
                     exit_code=code if code >= 0 else min(255, 128 - code), output_limited=limited.is_set(), updated=time.time())
    except (OSError, ValueError, ExecutorError, subprocess.SubprocessError):
        if child is None:
            state.update(state="not_started", exit_code=-1)
        else:
            try: os.killpg(child.pid, signal.SIGKILL); child.wait(timeout=3)
            except (OSError, subprocess.SubprocessError): pass
            state.update(state="unknown")
    finally:
        if master is not None:
            try: os.close(master)
            except OSError: pass
        if slave is not None:
            try: os.close(slave)
            except OSError: pass
        atomic(target / "state.json", state)


def main() -> None:
    if len(sys.argv) == 3 and sys.argv[1] == "--supervise":
        supervise(Path(sys.argv[2])); return
    request = {}
    try:
        raw = sys.stdin.buffer.read(MAX_REQUEST + 1)
        if len(raw) > MAX_REQUEST: raise ExecutorError("request_too_large")
        request = json.loads(raw)
        if not isinstance(request, dict): raise ExecutorError("invalid_request")
        nonce = request.get("nonce", "")
        if not isinstance(nonce, str) or not re.fullmatch(r"[a-f0-9]{32}", nonce): raise ExecutorError("invalid_nonce")
        node = request.get("node_id", "")
        if not isinstance(node, str) or not 1 <= len(node) <= 96: raise ExecutorError("invalid_node")
        root = Path.home() / ".agentdock-workbench" / "executor" / hashlib.sha256(node.encode()).hexdigest()[:24]
        result = Executor(root).control(request)
        response = {"protocol": PROTOCOL, "nonce": nonce, "ok": True, "result": result}
    except (OSError, ValueError, TypeError, KeyError, ExecutorError):
        response = {"protocol": PROTOCOL, "nonce": request.get("nonce", "") if isinstance(request, dict) else "", "ok": False, "error": "host_request_failed"}
    print(json.dumps(response, separators=(",", ":"), ensure_ascii=True))

if __name__ == "__main__":
    main()
